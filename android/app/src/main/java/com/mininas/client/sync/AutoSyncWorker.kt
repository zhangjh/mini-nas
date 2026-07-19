package com.mininas.client.sync

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import com.mininas.client.MiniNasApp
import kotlinx.coroutines.flow.first

/**
 * Periodic worker that runs every configured sync task through
 * [SyncEngine]. Scheduled by [SyncScheduler] when the user enables
 * auto-sync in settings. The network constraint (Wi-Fi only vs. any)
 * is enforced by WorkManager at execution time, so the worker itself
 * does not re-check connectivity.
 *
 * The engine's incremental-sync manifest ensures unchanged files are
 * skipped without any network request, so running all tasks every
 * cycle is cheap.
 */
class AutoSyncWorker(
    appContext: Context,
    params: WorkerParameters,
) : CoroutineWorker(appContext, params) {

    override suspend fun doWork(): Result {
        val app = applicationContext as MiniNasApp

        // Safety check: the user may have disabled auto-sync after the
        // work was enqueued. WorkManager.cancelUniqueWork should prevent
        // this, but guard anyway.
        if (!app.settings.autoSync.first()) return Result.success()

        // Skip entirely if not authenticated — nothing to upload.
        if (app.settings.getToken().isNullOrBlank()) return Result.success()

        val tasks = app.syncStore.tasks.first()
        if (tasks.isEmpty()) return Result.success()

        // Skip if a sync is already in progress (e.g. user-triggered),
        // to avoid overlapping runs on the same task ids.
        if (app.syncEngine.isRunning.value) return Result.success()

        // Re-probe the LAN route before running. The worker fires every 15
        // minutes, so the selector's cache is almost certainly stale; this
        // ensures we pick the fastest reachable address for this run. The
        // ApiProvider is rebuilt by the flow collector in MiniNasApp when
        // the effective URL changes.
        runCatching { app.urlSelector.refresh() }

        // Keep the process alive with a foreground notification while
        // uploading. SyncService stops itself once no task is active.
        SyncService.start(applicationContext)

        for (task in tasks) {
            // runOnce joins an in-flight run if one exists, so repeated
            // cycles are safe.
            app.syncEngine.runOnce(task)
        }
        return Result.success()
    }
}
