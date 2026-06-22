package com.mininas.client.ui.sync

import android.Manifest
import android.app.Application
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import androidx.core.content.ContextCompat
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.mininas.client.MiniNasApp
import com.mininas.client.data.prefs.SyncHistoryEntry
import com.mininas.client.data.prefs.SyncTaskDef
import com.mininas.client.sync.SyncService
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import java.util.UUID

data class SyncUiState(
    val tasks: List<SyncTaskDef> = emptyList(),
    val history: List<SyncHistoryEntry> = emptyList(),
    val toast: String? = null,
)

class SyncViewModel(app: Application) : AndroidViewModel(app) {

    private val mini = app as MiniNasApp
    private val engine = mini.syncEngine

    private val _toast = MutableStateFlow<String?>(null)
    val state: StateFlow<SyncUiState> = combine(
        mini.syncStore.tasks,
        mini.syncStore.history,
        _toast,
    ) { tasks, history, toast ->
        SyncUiState(tasks = tasks, history = history, toast = toast)
    }.stateIn(viewModelScope, SharingStarted.Eagerly, SyncUiState())

    val progress get() = mini.syncProgress.tasks

    fun addTask(name: String, sourceUri: String, sourceDisplayName: String, targetPath: String) {
        val mediaRoot = detectMediaRoot(sourceUri)
        val task = SyncTaskDef(
            id = UUID.randomUUID().toString(),
            name = name.ifBlank { sourceDisplayName },
            sourceUri = sourceUri,
            sourceDisplayName = sourceDisplayName,
            targetPath = targetPath,
            mediaRoot = mediaRoot,
        )
        viewModelScope.launch {
            mini.syncStore.addTask(task)
            startTask(task)
        }
    }

    /**
     * Inspects a SAF tree URI and returns the MediaStore relative-path
     * root (e.g. "DCIM", "Pictures/Screenshots") if the picked folder
     * maps to a known media directory. Returns null for non-media
     * folders, on API < 29 (where RELATIVE_PATH is unavailable), or
     * when the runtime media-read permission hasn't been granted (in
     * which case the task falls back to SAF, which uses the SAF tree
     * permission instead).
     *
     * Example: content://com.android.externalstorage.documents/tree/primary%3ADCIM%2FCamera
     * → "DCIM/Camera"
     */
    private fun detectMediaRoot(safTreeUri: String): String? {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) return null
        if (!hasMediaReadPermission()) return null
        val uri = Uri.parse(safTreeUri)
        val treePath = uri.lastPathSegment ?: return null
        val decoded = Uri.decode(treePath) // "primary:DCIM/Camera"
        // Only handle primary external storage — SD card URIs have
        // different tree segments and MediaStore scoping is unreliable.
        if (!decoded.startsWith("primary:")) return null
        val relPath = decoded.removePrefix("primary:").removePrefix("/")
        if (relPath.isEmpty()) return null
        val firstSegment = relPath.substringBefore("/")
        return if (firstSegment in MEDIA_ROOTS) relPath else null
    }

    private fun hasMediaReadPermission(): Boolean {
        val ctx = getApplication<Application>()
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            ContextCompat.checkSelfPermission(ctx, Manifest.permission.READ_MEDIA_IMAGES) ==
                PackageManager.PERMISSION_GRANTED ||
            ContextCompat.checkSelfPermission(ctx, Manifest.permission.READ_MEDIA_VIDEO) ==
                PackageManager.PERMISSION_GRANTED
        } else {
            ContextCompat.checkSelfPermission(ctx, Manifest.permission.READ_EXTERNAL_STORAGE) ==
                PackageManager.PERMISSION_GRANTED
        }
    }

    fun startTask(task: SyncTaskDef) {
        // Start the engine first so progress is SCANNING before the service
        // begins collecting. This prevents the service from seeing an empty
        // task map and stopping itself prematurely.
        engine.start(task)
        SyncService.start(getApplication())
    }

    fun cancelTask(taskId: String) = engine.cancel(taskId)

    fun removeTask(taskId: String) {
        engine.cancel(taskId)
        viewModelScope.launch {
            mini.syncStore.removeTask(taskId)
            mini.syncProgress.clear(taskId)
        }
    }

    fun clearHistory() {
        viewModelScope.launch { mini.syncStore.clearHistory() }
    }

    fun consumeToast() { _toast.value = null }

    /**
     * Resolves a SAF tree URI to a human-readable path for display.
     */
    fun resolveDisplayName(uri: String): String {
        return runCatching {
            val doc = androidx.documentfile.provider.DocumentFile.fromTreeUri(getApplication(), Uri.parse(uri))
            doc?.name ?: uri
        }.getOrDefault(uri)
    }

    companion object {
        /** Top-level directories that MediaStore indexes. */
        private val MEDIA_ROOTS = setOf("DCIM", "Pictures", "Movies", "Music")
    }
}
