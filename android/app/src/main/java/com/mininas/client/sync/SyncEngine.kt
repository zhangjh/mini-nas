package com.mininas.client.sync

import android.content.Context
import android.net.Uri
import androidx.documentfile.provider.DocumentFile
import com.mininas.client.MiniNasApp
import com.mininas.client.data.prefs.SyncHistoryEntry
import com.mininas.client.data.prefs.SyncTaskDef
import com.mininas.client.util.PathUtil
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.util.UUID

/**
 * Walks a SAF document tree and uploads every file to the configured server
 * path, mirroring the directory structure. Reports progress via [progressBus]
 * and persists per-file results to [SyncStore].
 */
class SyncEngine(
    private val app: MiniNasApp,
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val running: MutableMap<String, Job> = mutableMapOf()

    private val _isRunning: MutableStateFlow<Boolean> = MutableStateFlow(false)
    val isRunning: StateFlow<Boolean> = _isRunning.asStateFlow()

    fun start(task: SyncTaskDef): String {
        if (running[task.id]?.isActive == true) return task.id
        val runId = UUID.randomUUID().toString()
        val job = scope.launch {
            _isRunning.value = true
            try {
                runSync(task, runId)
            } finally {
                _isRunning.value = false
                running.remove(task.id)
            }
        }
        running[task.id] = job
        return runId
    }

    fun cancel(taskId: String) {
        running[taskId]?.cancel()
        running.remove(taskId)
        app.syncProgress.update(taskId) { it.copy(phase = TaskProgress.Phase.CANCELLED) }
    }

    private suspend fun runSync(task: SyncTaskDef, runId: String) {
        val bus = app.syncProgress
        bus.update(task.id) { it.copy(phase = TaskProgress.Phase.SCANNING, message = "扫描文件…") }

        val root = DocumentFile.fromTreeUri(app, Uri.parse(task.sourceUri))
        if (root == null || !root.exists()) {
            bus.update(task.id) { it.copy(phase = TaskProgress.Phase.FAILED, message = "无法访问源目录") }
            return
        }
        val files = mutableListOf<DocEntry>()
        collect(root, "", files)
        if (files.isEmpty()) {
            bus.update(task.id) {
                it.copy(phase = TaskProgress.Phase.DONE, total = 0, current = 0, message = "目录为空")
            }
            return
        }

        bus.update(task.id) {
            it.copy(
                phase = TaskProgress.Phase.UPLOADING,
                total = files.size,
                current = 0,
                succeeded = 0,
                failed = 0,
                message = "开始上传",
            )
        }

        val history = mutableListOf<SyncHistoryEntry>()
        var succeeded = 0
        var failed = 0
        files.forEachIndexed { idx, entry ->
            val cur = bus.get(task.id)
            if (cur?.phase == TaskProgress.Phase.CANCELLED) return@forEachIndexed
            bus.update(task.id) {
                it.copy(
                    current = idx + 1,
                    currentFile = entry.relativePath,
                    fileProgress = 0f,
                    message = "上传 ${entry.relativePath}",
                )
            }
            val targetDir = PathUtil.join(task.targetPath, entry.relativeParent)
            // Ensure target directory exists (best-effort, ignore "already exists" errors).
            if (entry.relativeParent.isNotEmpty()) {
                runCatching { app.fileRepo.mkdir(targetDir) }
            }
            val result = runCatching {
                app.fileRepo.uploadFile(
                    path = targetDir,
                    file = entry.cachedFile,
                    mimeType = entry.mimeType,
                    onProgress = { p ->
                        bus.update(task.id) { it.copy(fileProgress = p) }
                    },
                )
            }
            if (result.isSuccess) {
                succeeded++
                history.add(
                    SyncHistoryEntry(
                        id = UUID.randomUUID().toString(),
                        taskId = task.id,
                        relativePath = entry.relativePath,
                        status = "success",
                        size = entry.cachedFile.length(),
                    )
                )
            } else {
                failed++
                history.add(
                    SyncHistoryEntry(
                        id = UUID.randomUUID().toString(),
                        taskId = task.id,
                        relativePath = entry.relativePath,
                        status = "failed",
                        message = result.exceptionOrNull()?.message ?: "上传失败",
                        size = entry.cachedFile.length(),
                    )
                )
            }
            bus.update(task.id) { it.copy(succeeded = succeeded, failed = failed) }
            // Clean up cached copy.
            entry.cachedFile.delete()
        }

        app.syncStore.appendHistory(history)
        val finalPhase = when {
            failed == 0 -> TaskProgress.Phase.DONE
            succeeded == 0 -> TaskProgress.Phase.FAILED
            else -> TaskProgress.Phase.DONE
        }
        bus.update(task.id) {
            it.copy(
                phase = finalPhase,
                current = files.size,
                fileProgress = 1f,
                message = "完成：成功 $succeeded，失败 $failed",
            )
        }
    }

    private fun collect(node: DocumentFile, parent: String, out: MutableList<DocEntry>) {
        node.listFiles().forEach { child ->
            val name = child.name ?: return@forEach
            if (child.isDirectory) {
                collect(child, if (parent.isEmpty()) name else "$parent/$name", out)
            } else if (child.isFile) {
                // Cache to a temp file so OkHttp can stream with progress.
                val cached = java.io.File.createTempFile("sync_", "_${sanitize(name)}", app.cacheDir)
                runCatching {
                    app.contentResolver.openInputStream(child.uri)?.use { input ->
                        cached.outputStream().use { input.copyTo(it) }
                    }
                }.onFailure { cached.delete(); return@forEach }
                out.add(
                    DocEntry(
                        relativeParent = parent,
                        relativePath = if (parent.isEmpty()) name else "$parent/$name",
                        cachedFile = cached,
                        mimeType = child.type ?: "application/octet-stream",
                    )
                )
            }
        }
    }

    private fun sanitize(name: String): String =
        name.replace(Regex("[^A-Za-z0-9._-]"), "_").take(32).ifEmpty { "file" }

    private data class DocEntry(
        val relativeParent: String,
        val relativePath: String,
        val cachedFile: java.io.File,
        val mimeType: String,
    )
}
