package com.mininas.client.sync

import android.net.Uri
import androidx.documentfile.provider.DocumentFile
import com.mininas.client.MiniNasApp
import com.mininas.client.data.prefs.SyncHistoryEntry
import com.mininas.client.data.prefs.SyncTaskDef
import com.mininas.client.data.prefs.SyncedFileEntry
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
 *
 * Incremental sync: a per-task manifest of already-uploaded files (path,
 * size, lastModified) is kept in [SyncStore]. On subsequent runs, files
 * whose size and lastModified match the manifest are skipped without any
 * network request. Files not in the manifest fall back to a server-side
 * existence check before uploading.
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
        collect(root, "", files, task.id)
        if (files.isEmpty()) {
            bus.update(task.id) {
                it.copy(phase = TaskProgress.Phase.DONE, total = 0, current = 0, message = "目录为空")
            }
            return
        }

        // Load the manifest of files already uploaded for this task.
        val manifest = app.syncStore.getSyncedFiles(task.id).toMutableMap()

        bus.update(task.id) {
            it.copy(
                phase = TaskProgress.Phase.UPLOADING,
                total = files.size,
                current = 0,
                succeeded = 0,
                failed = 0,
                skipped = 0,
                message = "开始上传",
            )
        }

        val history = mutableListOf<SyncHistoryEntry>()
        var succeeded = 0
        var failed = 0
        var skipped = 0
        files.forEachIndexed { idx, entry ->
            val cur = bus.get(task.id)
            if (cur?.phase == TaskProgress.Phase.CANCELLED) return@forEachIndexed
            bus.update(task.id) {
                it.copy(
                    current = idx + 1,
                    currentFile = entry.relativePath,
                    fileProgress = 0f,
                    message = "检查 ${entry.relativePath}",
                )
            }

            // 1) Local manifest hit: size + lastModified both match → skip
            //    without any network request.
            val local = manifest[entry.relativePath]
            if (local != null &&
                local.size == entry.size &&
                (entry.lastModified == 0L || local.lastModified == entry.lastModified)
            ) {
                skipped++
                history.add(
                    SyncHistoryEntry(
                        id = UUID.randomUUID().toString(),
                        taskId = task.id,
                        relativePath = entry.relativePath,
                        status = "skipped",
                        size = entry.size,
                        message = "本地已同步，未修改",
                    )
                )
                bus.update(task.id) { it.copy(skipped = skipped, fileProgress = 1f) }
            }

            val targetDir = PathUtil.join(task.targetPath, entry.relativeParent)
            // Ensure target directory exists (best-effort).
            if (entry.relativeParent.isNotEmpty()) {
                runCatching { app.fileRepo.mkdir(targetDir) }
            }

            // 2) Server fallback: if another device already uploaded a file
            //    with the same name and size, skip the upload.
            val targetPath = PathUtil.join(targetDir, entry.relativePath.substringAfterLast('/'))
            val existing = runCatching { app.fileRepo.getFileInfoOrNull(targetPath) }.getOrNull()
            if (existing != null && !existing.isDir && existing.size == entry.size) {
                skipped++
                manifest[entry.relativePath] = SyncedFileEntry(
                    relativePath = entry.relativePath,
                    size = entry.size,
                    lastModified = entry.lastModified,
                    syncedAt = System.currentTimeMillis(),
                )
                history.add(
                    SyncHistoryEntry(
                        id = UUID.randomUUID().toString(),
                        taskId = task.id,
                        relativePath = entry.relativePath,
                        status = "skipped",
                        size = entry.size,
                        message = "服务器已存在相同文件",
                    )
                )
                bus.update(task.id) { it.copy(skipped = skipped, fileProgress = 1f) }
            }

            // 3) Upload needed. Stream directly from the SAF InputStream via
            //    OkHttp, no temp-file copy.
            bus.update(task.id) {
                it.copy(message = "上传 ${entry.relativePath}")
            }
            val fileName = entry.relativePath.substringAfterLast('/')
            val result = runCatching {
                app.fileRepo.uploadFile(
                    path = targetDir,
                    contentResolver = app.contentResolver,
                    uri = entry.uri,
                    fileName = fileName,
                    size = entry.size,
                    mimeType = entry.mimeType,
                    onProgress = { p ->
                        bus.update(task.id) { it.copy(fileProgress = p) }
                    },
                )
            }
            if (result.isSuccess) {
                succeeded++
                manifest[entry.relativePath] = SyncedFileEntry(
                    relativePath = entry.relativePath,
                    size = entry.size,
                    lastModified = entry.lastModified,
                    syncedAt = System.currentTimeMillis(),
                )
                history.add(
                    SyncHistoryEntry(
                        id = UUID.randomUUID().toString(),
                        taskId = task.id,
                        relativePath = entry.relativePath,
                        status = "success",
                        size = entry.size,
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
                        size = entry.size,
                    )
                )
            }
            bus.update(task.id) { it.copy(succeeded = succeeded, failed = failed) }
        }

        // Persist the updated manifest so the next run can skip locally.
        app.syncStore.saveSyncedFiles(task.id, manifest.values.toList())
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
                message = "完成：成功 $succeeded，跳过 $skipped，失败 $failed",
            )
        }
    }

    private fun collect(
        node: DocumentFile,
        parent: String,
        out: MutableList<DocEntry>,
        taskId: String,
    ) {
        node.listFiles().forEach { child ->
            val name = child.name ?: return@forEach
            if (child.isDirectory) {
                collect(child, if (parent.isEmpty()) name else "$parent/$name", out, taskId)
            } else if (child.isFile) {
                out.add(
                    DocEntry(
                        relativeParent = parent,
                        relativePath = if (parent.isEmpty()) name else "$parent/$name",
                        uri = child.uri,
                        size = child.length(),
                        lastModified = child.lastModified(),
                        mimeType = child.type ?: "application/octet-stream",
                    )
                )
                app.syncProgress.update(taskId) {
                    it.copy(message = "扫描文件… 已发现 ${out.size} 个")
                }
            }
        }
    }

    private data class DocEntry(
        val relativeParent: String,
        val relativePath: String,
        val uri: Uri,
        val size: Long,
        val lastModified: Long,
        val mimeType: String,
    )
}
