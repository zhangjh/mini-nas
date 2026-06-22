package com.mininas.client.sync

import android.content.ContentUris
import android.content.ContentResolver
import android.net.Uri
import android.provider.MediaStore
import androidx.documentfile.provider.DocumentFile
import com.mininas.client.MiniNasApp
import com.mininas.client.data.prefs.SyncHistoryEntry
import com.mininas.client.data.prefs.SyncTaskDef
import com.mininas.client.data.prefs.SyncedFileEntry
import com.mininas.client.data.repo.toErrorMessage
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
 * Syncs files from the device to the configured server path, mirroring the
 * directory structure. Reports progress via [progressBus] and persists
 * per-file results to [SyncStore].
 *
 * Two collection strategies, chosen per task by [SyncTaskDef.mediaRoot]:
 *
 * - **MediaStore** (media folders like DCIM/Pictures): queries the system
 *   MediaStore with `date_modified > lastSyncAt`, sorted descending. This
 *   is an indexed database lookup — no directory traversal needed, so
 *   only new/modified files are returned in O(log n).
 *
 * - **SAF** (arbitrary folders): walks the DocumentFile tree. Each
 *   directory's files are sorted by lastModified descending and
 *   collection stops at the watermark, so old files are skipped early.
 *
 * Both paths share the same watermark ([SyncStore.getLastSyncAt]) and
 * upload logic. The watermark is only advanced on a clean run (no
 * failures), so failed files are retried next time.
 *
 * Upload path is intentionally minimal — no per-file `mkdir`. The watermark
 * + local manifest already guarantee we only ship files that are new or
 * modified since the last successful sync, so every upload is useful work.
 * The server's `WriteFile` creates intermediate directories itself (MkdirAll).
 *
 * On the first run (watermark == 0, manifest empty), a single bulk
 * `GET /api/files/tree` request fetches all files already on the server.
 * Files with matching size are skipped and recorded in the manifest, so
 * subsequent runs skip them locally. This avoids redundant re-uploads when
 * the task is recreated or the app is upgraded, without per-file probing.
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

    /**
     * Starts [task] (or joins the in-flight run if already active) and
     * suspends until it completes. Intended for background callers such as
     * [AutoSyncWorker] that need to await the result.
     */
    suspend fun runOnce(task: SyncTaskDef) {
        start(task)
        // start() returns the existing runId if already active; join the
        // current job. If it already finished and removed itself, join is
        // a no-op.
        running[task.id]?.join()
    }

    fun cancel(taskId: String) {
        running[taskId]?.cancel()
        running.remove(taskId)
        app.syncProgress.update(taskId) { it.copy(phase = TaskProgress.Phase.CANCELLED) }
    }

    private suspend fun runSync(task: SyncTaskDef, runId: String) {
        val bus = app.syncProgress
        val lastSyncAt = app.syncStore.getLastSyncAt(task.syncKey)

        bus.update(task.id) {
            it.copy(
                phase = TaskProgress.Phase.SCANNING,
                message = if (lastSyncAt > 0) "增量扫描文件…" else "扫描文件…",
            )
        }

        val files = mutableListOf<DocEntry>()
        val maxLastModified = if (task.mediaRoot != null) {
            collectFromMediaStore(task.mediaRoot, files, task.id, lastSyncAt)
        } else {
            val root = DocumentFile.fromTreeUri(app, Uri.parse(task.sourceUri))
            if (root == null || !root.exists()) {
                bus.update(task.id) { it.copy(phase = TaskProgress.Phase.FAILED, message = "无法访问源目录") }
                return
            }
            collect(root, "", files, task.id, lastSyncAt)
        }
        if (files.isEmpty()) {
            // No new/modified files. Advance the watermark so the next run
            // can skip the same old files, but only if we actually saw any
            // files (maxLastModified > 0) and didn't already have one.
            if (lastSyncAt == 0L && maxLastModified > 0) {
                app.syncStore.saveLastSyncAt(task.syncKey, maxLastModified)
            }
            bus.update(task.id) {
                it.copy(
                    phase = TaskProgress.Phase.DONE,
                    total = 0,
                    current = 0,
                    message = if (lastSyncAt > 0) "无新文件需要同步" else "目录为空",
                )
            }
            return
        }

        // Load the manifest after scanning. It's only needed for the
        // upload-skip fallback (e.g. files re-collected after a failed run
        // whose watermark wasn't advanced), not for collect() itself.
        val manifest = app.syncStore.getSyncedFiles(task.syncKey).toMutableMap()

        // On the first run (no watermark), the local manifest is empty.
        // Fetch the server-side file tree so we can skip files that already
        // exist on the server with matching relative path + size — this
        // avoids redundant re-uploads when the task is recreated or the app
        // is upgraded. One bulk request instead of N per-file probes.
        // The map key is the path relative to task.targetPath (includes
        // filename + directory structure), so matching is path-exact + size.
        val remoteFiles: Map<String, Long> = if (lastSyncAt == 0L) {
            bus.update(task.id) { it.copy(message = "检查远端已同步文件…") }
            try {
                val targetPrefix = task.targetPath.trimEnd('/') + "/"
                app.fileRepo.listFileTree(task.targetPath)
                    .filter { it.path.startsWith(targetPrefix) }
                    .associate { it.path.removePrefix(targetPrefix) to it.size }
            } catch (e: Exception) {
                emptyMap()
            }
        } else {
            emptyMap()
        }

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
                return@forEachIndexed
            }

            // 1.5) Server-side dedup: if the file already exists on the
            //      server at the same relative path with matching size, skip
            //      the upload and record it in the manifest so it's skipped
            //      locally next time. The path match is exact (map key lookup
            //      on entry.relativePath, which includes filename + dirs);
            //      size is a secondary guard against partial/corrupt uploads.
            //      This is critical when the local manifest is empty (first
            //      run, recreated task, app upgrade) but files are already on
            //      the server from a previous sync.
            val remoteSize = remoteFiles[entry.relativePath]
            if (remoteSize != null && remoteSize == entry.size) {
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
                        message = "远端已存在",
                    )
                )
                bus.update(task.id) { it.copy(skipped = skipped, fileProgress = 1f) }
                return@forEachIndexed
            }

            // 2) Upload needed. Stream directly from the SAF/MediaStore
            //    InputStream via OkHttp, no temp-file copy. The server's
            //    WriteFile creates intermediate directories automatically
            //    (MkdirAll), so no client-side mkdir is needed.
            val targetDir = PathUtil.join(task.targetPath, entry.relativeParent)
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
                        message = result.exceptionOrNull()?.toErrorMessage() ?: "上传失败",
                        size = entry.size,
                    )
                )
            }
            bus.update(task.id) { it.copy(succeeded = succeeded, failed = failed) }
        }

        // Persist the updated manifest so the next run can skip locally.
        app.syncStore.saveSyncedFiles(task.syncKey, manifest.values.toList())
        app.syncStore.appendHistory(history)

        // Advance the incremental-sync watermark. Only do this when the run
        // had no failures, so that failed files (whose lastModified is <=
        // maxLastModified) are retried on the next run instead of being
        // skipped by the timestamp filter during collect().
        if (failed == 0 && maxLastModified > 0) {
            app.syncStore.saveLastSyncAt(task.syncKey, maxLastModified)
        }

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

    /**
     * Queries MediaStore for new/modified media files under [mediaRoot].
     * Returns the maximum date_modified (in ms) seen across ALL matched
     * files, so the caller can advance the watermark.
     *
     * Incremental filter: when [lastSyncAt] > 0, only files with
     * `date_modified > lastSyncAt/1000` are returned (MediaStore stores
     * seconds, the watermark is in ms). When [lastSyncAt] is 0 (first
     * run), all files under [mediaRoot] are returned.
     *
     * This is a single indexed database query per collection (Images,
     * Video) — no directory traversal, so it's dramatically faster than
     * walking the SAF tree for large photo libraries.
     */
    private fun collectFromMediaStore(
        mediaRoot: String,
        out: MutableList<DocEntry>,
        taskId: String,
        lastSyncAt: Long,
    ): Long {
        val resolver: ContentResolver = app.contentResolver
        var maxLastModified = 0L

        // MediaStore stores date_modified in seconds; our watermark is in ms.
        val lastSyncSeconds = lastSyncAt / 1000

        val projection = arrayOf(
            MediaStore.MediaColumns._ID,
            MediaStore.MediaColumns.DISPLAY_NAME,
            MediaStore.MediaColumns.SIZE,
            MediaStore.MediaColumns.DATE_MODIFIED,
            MediaStore.MediaColumns.MIME_TYPE,
            MediaStore.MediaColumns.RELATIVE_PATH,
        )

        // Scope to mediaRoot and its subfolders. RELATIVE_PATH looks like
        // "DCIM/Camera/" (trailing slash, no leading slash).
        val selection = buildString {
            if (lastSyncAt > 0) append("${MediaStore.MediaColumns.DATE_MODIFIED} > ?")
            else append("${MediaStore.MediaColumns.DATE_MODIFIED} >= 0")
            append(" AND ${MediaStore.MediaColumns.RELATIVE_PATH} LIKE ?")
        }
        val selectionArgs = if (lastSyncAt > 0) {
            arrayOf(lastSyncSeconds.toString(), "$mediaRoot/%")
        } else {
            arrayOf("$mediaRoot/%")
        }
        val sortOrder = "${MediaStore.MediaColumns.DATE_MODIFIED} DESC"

        // Query Images and Video collections. Audio is omitted since the
        // primary use case is photo/video backup; add it here if needed.
        val collections = listOf(
            MediaStore.Images.Media.EXTERNAL_CONTENT_URI,
            MediaStore.Video.Media.EXTERNAL_CONTENT_URI,
        )

        for (collection in collections) {
            resolver.query(collection, projection, selection, selectionArgs, sortOrder)?.use { cursor ->
                val idCol = cursor.getColumnIndexOrThrow(MediaStore.MediaColumns._ID)
                val nameCol = cursor.getColumnIndexOrThrow(MediaStore.MediaColumns.DISPLAY_NAME)
                val sizeCol = cursor.getColumnIndexOrThrow(MediaStore.MediaColumns.SIZE)
                val dateCol = cursor.getColumnIndexOrThrow(MediaStore.MediaColumns.DATE_MODIFIED)
                val mimeCol = cursor.getColumnIndexOrThrow(MediaStore.MediaColumns.MIME_TYPE)
                val relPathCol = cursor.getColumnIndexOrThrow(MediaStore.MediaColumns.RELATIVE_PATH)

                while (cursor.moveToNext()) {
                    val id = cursor.getLong(idCol)
                    val name = cursor.getString(nameCol) ?: continue
                    val size = cursor.getLong(sizeCol)
                    val dateModifiedSecs = cursor.getLong(dateCol)
                    val dateModifiedMs = dateModifiedSecs * 1000L
                    val mimeType = cursor.getString(mimeCol) ?: "application/octet-stream"
                    val relPath = cursor.getString(relPathCol) ?: ""

                    if (dateModifiedMs > maxLastModified) maxLastModified = dateModifiedMs

                    val contentUri = ContentUris.withAppendedId(collection, id)

                    // Build path relative to mediaRoot so the server-side
                    // structure mirrors the picked folder.
                    // relPath = "DCIM/Camera/Sub/" → strip "DCIM/" prefix
                    val relativeParent = relPath
                        .removePrefix("$mediaRoot/")
                        .trimEnd('/')

                    out.add(
                        DocEntry(
                            relativeParent = relativeParent,
                            relativePath = if (relativeParent.isEmpty()) name else "$relativeParent/$name",
                            uri = contentUri,
                            size = size,
                            lastModified = dateModifiedMs,
                            mimeType = mimeType,
                        ),
                    )
                    app.syncProgress.update(taskId) {
                        it.copy(message = "扫描文件… 已发现 ${out.size} 个")
                    }
                }
            }
        }
        return maxLastModified
    }

    /**
     * Recursively walks [node] and appends files that need processing to
     * [out]. Returns the maximum lastModified seen across ALL files
     * (including skipped ones), so the caller can advance the watermark.
     *
     * Incremental filter: within each directory, files are sorted by
     * lastModified descending. We collect files while their lastModified
     * is strictly greater than [lastSyncAt], and stop as soon as we
     * encounter one at or below the watermark — because the list is
     * sorted, every remaining file is equally old. Subdirectories are
     * always recursed into (a subdir may contain new files even when
     * the parent directory's own files are all old).
     *
     * When [lastSyncAt] is 0 (first run), no filtering is applied and
     * every file is collected.
     */
    private fun collect(
        node: DocumentFile,
        parent: String,
        out: MutableList<DocEntry>,
        taskId: String,
        lastSyncAt: Long,
    ): Long {
        var maxLastModified = 0L
        val files = mutableListOf<DocumentFile>()
        node.listFiles().forEach { child ->
            val name = child.name ?: return@forEach
            if (child.isDirectory) {
                maxLastModified = maxOf(
                    maxLastModified,
                    collect(child, if (parent.isEmpty()) name else "$parent/$name", out, taskId, lastSyncAt),
                )
            } else if (child.isFile) {
                files.add(child)
            }
        }

        // Sort newest-first so we can stop at the watermark.
        files.sortByDescending { it.lastModified() }

        for (file in files) {
            val lm = file.lastModified()
            if (lm > maxLastModified) maxLastModified = lm

            // Once we hit a file at or below the watermark, all remaining
            // files (sorted descending) are equally old — stop.
            if (lastSyncAt > 0 && lm <= lastSyncAt) break

            val name = file.name ?: continue
            out.add(
                DocEntry(
                    relativeParent = parent,
                    relativePath = if (parent.isEmpty()) name else "$parent/$name",
                    uri = file.uri,
                    size = file.length(),
                    lastModified = lm,
                    mimeType = file.type ?: "application/octet-stream",
                ),
            )
            app.syncProgress.update(taskId) {
                it.copy(message = "扫描文件… 已发现 ${out.size} 个")
            }
        }
        return maxLastModified
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
