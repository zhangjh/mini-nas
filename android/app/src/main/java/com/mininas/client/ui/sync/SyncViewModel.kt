package com.mininas.client.ui.sync

import android.app.Application
import android.content.Intent
import android.net.Uri
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.mininas.client.MiniNasApp
import com.mininas.client.data.prefs.SyncHistoryEntry
import com.mininas.client.data.prefs.SyncTaskDef
import com.mininas.client.sync.SyncEngine
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
    private val engine = SyncEngine(mini)

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
        val task = SyncTaskDef(
            id = UUID.randomUUID().toString(),
            name = name.ifBlank { sourceDisplayName },
            sourceUri = sourceUri,
            sourceDisplayName = sourceDisplayName,
            targetPath = targetPath,
        )
        viewModelScope.launch {
            mini.syncStore.addTask(task)
            startTask(task)
        }
    }

    fun startTask(task: SyncTaskDef) {
        SyncService.start(getApplication())
        engine.start(task)
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
}
