package com.mininas.client.data.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.longPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json

private val Context.syncDataStore: DataStore<Preferences> by preferencesDataStore(name = "mini_nas_sync")

@Serializable
data class SyncTaskDef(
    val id: String,
    val name: String,
    val sourceUri: String,
    val sourceDisplayName: String,
    val targetPath: String,
    /**
     * When non-null, the task syncs via MediaStore instead of walking the
     * SAF tree. Contains the MediaStore relative-path root (e.g. "DCIM",
     * "Pictures/Screenshots") used to scope the query. Only set when the
     * picked SAF folder maps to a known media directory on API 29+.
     */
    val mediaRoot: String? = null,
)

@Serializable
data class SyncHistoryEntry(
    val id: String,
    val taskId: String,
    val relativePath: String,
    val status: String, // "success" | "failed" | "skipped"
    val size: Long = 0,
    val message: String = "",
    val timestamp: Long = System.currentTimeMillis(),
)

/**
 * Record of a file that has been successfully uploaded for a task. Used to
 * skip unchanged files on subsequent (incremental) sync runs without
 * querying the server.
 */
@Serializable
data class SyncedFileEntry(
    val relativePath: String,
    val size: Long,
    val lastModified: Long,
    val syncedAt: Long,
)

/**
 * Persists sync task definitions and per-file upload history as JSON in
 * DataStore. History is append-only and capped at [MAX_HISTORY] entries to
 * keep storage bounded.
 */
class SyncStore(private val context: Context) {

    private val json = Json { ignoreUnknownKeys = true }
    private val keyTasks = stringPreferencesKey("tasks_json")
    private val keyHistory = stringPreferencesKey("history_json")
    private val keySyncedPrefix = "synced_files_"
    private val keyLastSyncAtPrefix = "last_sync_at_"

    val tasks: Flow<List<SyncTaskDef>> = context.syncDataStore.data.map { prefs ->
        prefs[keyTasks]?.let { runCatching { json.decodeFromString(ListSerializer(SyncTaskDef.serializer()), it) }.getOrNull() } ?: emptyList()
    }

    val history: Flow<List<SyncHistoryEntry>> = context.syncDataStore.data.map { prefs ->
        prefs[keyHistory]?.let { runCatching { json.decodeFromString(ListSerializer(SyncHistoryEntry.serializer()), it) }.getOrNull() } ?: emptyList()
    }

    suspend fun addTask(task: SyncTaskDef) {
        context.syncDataStore.edit { prefs ->
            val list = prefs[keyTasks]?.let {
                runCatching { json.decodeFromString(ListSerializer(SyncTaskDef.serializer()), it) }.getOrNull()
            } ?: emptyList()
            prefs[keyTasks] = json.encodeToString(ListSerializer(SyncTaskDef.serializer()), list + task)
        }
    }

    suspend fun removeTask(taskId: String) {
        context.syncDataStore.edit { prefs ->
            val list = prefs[keyTasks]?.let {
                runCatching { json.decodeFromString(ListSerializer(SyncTaskDef.serializer()), it) }.getOrNull()
            } ?: emptyList()
            prefs[keyTasks] = json.encodeToString(ListSerializer(SyncTaskDef.serializer()), list.filterNot { it.id == taskId })
            // Also drop the synced-files manifest and last-sync timestamp for this task.
            prefs.remove(stringPreferencesKey(keySyncedPrefix + taskId))
            prefs.remove(longPreferencesKey(keyLastSyncAtPrefix + taskId))
        }
    }

    suspend fun appendHistory(entries: List<SyncHistoryEntry>) {
        if (entries.isEmpty()) return
        context.syncDataStore.edit { prefs ->
            val existing = prefs[keyHistory]?.let {
                runCatching { json.decodeFromString(ListSerializer(SyncHistoryEntry.serializer()), it) }.getOrNull()
            } ?: emptyList()
            val merged = (existing + entries).takeLast(MAX_HISTORY)
            prefs[keyHistory] = json.encodeToString(ListSerializer(SyncHistoryEntry.serializer()), merged)
        }
    }

    suspend fun clearHistory() {
        context.syncDataStore.edit { it.remove(keyHistory) }
    }

    /**
     * Loads the synced-files manifest for [taskId] as a map keyed by
     * relative path. Returns an empty map if none stored yet.
     */
    suspend fun getSyncedFiles(taskId: String): Map<String, SyncedFileEntry> {
        val prefs = context.syncDataStore.data.first()
        val raw = prefs[stringPreferencesKey(keySyncedPrefix + taskId)]
        return raw?.let {
            runCatching {
                json.decodeFromString(ListSerializer(SyncedFileEntry.serializer()), it)
                    .associateBy { it.relativePath }
            }.getOrNull() ?: emptyMap()
        } ?: emptyMap()
    }

    /**
     * Replaces the synced-files manifest for [taskId] with [entries].
     */
    suspend fun saveSyncedFiles(taskId: String, entries: List<SyncedFileEntry>) {
        context.syncDataStore.edit { prefs ->
            prefs[stringPreferencesKey(keySyncedPrefix + taskId)] =
                json.encodeToString(ListSerializer(SyncedFileEntry.serializer()), entries)
        }
    }

    /**
     * Returns the max file lastModified timestamp seen during the last
     * successful sync for [taskId], or 0 if none recorded yet. Used by
     * [SyncEngine] to skip unchanged files during the directory walk
     * itself, avoiding a full re-scan.
     */
    suspend fun getLastSyncAt(taskId: String): Long {
        val prefs = context.syncDataStore.data.first()
        return prefs[longPreferencesKey(keyLastSyncAtPrefix + taskId)] ?: 0L
    }

    /**
     * Persists [timestamp] as the new last-sync watermark for [taskId].
     * Only call this after a sync run with no failures, so that failed
     * files (whose lastModified is <= [timestamp]) are retried on the
     * next run instead of being skipped.
     */
    suspend fun saveLastSyncAt(taskId: String, timestamp: Long) {
        context.syncDataStore.edit { prefs ->
            prefs[longPreferencesKey(keyLastSyncAtPrefix + taskId)] = timestamp
        }
    }

    companion object {
        private const val MAX_HISTORY = 500
    }
}
