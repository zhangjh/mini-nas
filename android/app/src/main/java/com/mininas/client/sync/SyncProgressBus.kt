package com.mininas.client.sync

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

/**
 * Per-task live progress, broadcast from [SyncEngine] to UI via StateFlow.
 * Keyed by task id.
 */
data class TaskProgress(
    val taskId: String,
    val phase: Phase = Phase.IDLE,
    val current: Int = 0,
    val total: Int = 0,
    val currentFile: String = "",
    val fileProgress: Float = 0f,
    val succeeded: Int = 0,
    val failed: Int = 0,
    val skipped: Int = 0,
    val message: String = "",
) {
    enum class Phase { IDLE, SCANNING, UPLOADING, DONE, FAILED, CANCELLED }
}

class SyncProgressBus {
    private val _tasks: MutableStateFlow<Map<String, TaskProgress>> = MutableStateFlow(emptyMap())
    val tasks: StateFlow<Map<String, TaskProgress>> = _tasks.asStateFlow()

    fun update(taskId: String, block: (TaskProgress) -> TaskProgress) {
        _tasks.update { map ->
            val cur = map[taskId] ?: TaskProgress(taskId)
            (map + (taskId to block(cur)))
        }
    }

    fun get(taskId: String): TaskProgress? = _tasks.value[taskId]
    fun clear(taskId: String) {
        _tasks.update { it - taskId }
    }
}
