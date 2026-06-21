package com.mininas.client.sync

import androidx.work.Constraints
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import com.mininas.client.MiniNasApp
import kotlinx.coroutines.flow.first
import java.util.concurrent.TimeUnit

/**
 * Manages the periodic [AutoSyncWorker] schedule based on the user's
 * auto-sync and Wi-Fi-only settings.
 *
 * Call [schedule] whenever either setting changes. It reads the current
 * values from [SettingsStore] and either enqueues the periodic work
 * (with the appropriate network constraint) or cancels it.
 *
 * WorkManager persists the schedule across reboots, so no boot receiver
 * is needed.
 */
object SyncScheduler {

    private const val WORK_NAME = "auto_sync"
    private const val INTERVAL_MIN = 15L // WorkManager minimum periodic interval

    /**
     * Enqueues or cancels the auto-sync periodic work according to the
     * current settings. Safe to call repeatedly; uses
     * [ExistingPeriodicWorkPolicy.UPDATE] so the constraints are
     * refreshed without duplicating the work.
     */
    suspend fun schedule(app: MiniNasApp) {
        val autoSync = app.settings.autoSync.first()
        val wifiOnly = app.settings.wifiOnly.first()
        val workManager = WorkManager.getInstance(app)

        if (!autoSync) {
            workManager.cancelUniqueWork(WORK_NAME)
            return
        }

        // UNMETERED effectively means Wi-Fi / Ethernet in practice.
        val networkType = if (wifiOnly) NetworkType.UNMETERED else NetworkType.CONNECTED

        val constraints = Constraints.Builder()
            .setRequiredNetworkType(networkType)
            .build()

        val request = PeriodicWorkRequestBuilder<AutoSyncWorker>(INTERVAL_MIN, TimeUnit.MINUTES)
            .setConstraints(constraints)
            .build()

        workManager.enqueueUniquePeriodicWork(
            WORK_NAME,
            ExistingPeriodicWorkPolicy.UPDATE,
            request,
        )
    }
}
