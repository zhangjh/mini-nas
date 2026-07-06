package com.mininas.client.sync

import android.app.Notification
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.PowerManager
import androidx.core.app.NotificationCompat
import androidx.lifecycle.LifecycleService
import androidx.lifecycle.lifecycleScope
import com.mininas.client.MainActivity
import com.mininas.client.MiniNasApp
import com.mininas.client.R
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch

/**
 * Keeps the sync process alive by running as a foreground service while any
 * task is uploading. The actual work happens in [SyncEngine]; this service
 * only owns the lifecycle notification and a wake lock to keep the CPU
 * running while the screen is off.
 *
 * A foreground service prevents the system from killing the process, but it
 * does NOT automatically keep the CPU awake. When the screen turns off the
 * CPU enters sleep and all threads (including [Dispatchers.IO]) are paused.
 * A partial wake lock keeps the CPU running so uploads continue.
 */
class SyncService : LifecycleService() {

    private var wakeLock: PowerManager.WakeLock? = null

    override fun onCreate() {
        super.onCreate()
        startForeground(NOTIF_ID, buildNotification("准备同步…"))
        acquireWakeLock()
        val app = (application as MiniNasApp)
        lifecycleScope.launch {
            app.syncProgress.tasks.collectLatest { tasks ->
                val active = tasks.values.filter {
                    it.phase == TaskProgress.Phase.UPLOADING || it.phase == TaskProgress.Phase.SCANNING
                }
                if (active.isEmpty()) {
                    // Grace period: the sync engine may not have updated
                    // progress yet (e.g. service started slightly before
                    // engine.start()). Wait before stopping; collectLatest
                    // cancels this delay if a new value arrives (SCANNING /
                    // UPLOADING), keeping the service alive.
                    delay(STOP_GRACE_MS)
                    // Re-check after the delay — if still no active tasks,
                    // the sync has truly finished or not started.
                    val current = app.syncProgress.tasks.value
                    val stillActive = current.values.any {
                        it.phase == TaskProgress.Phase.UPLOADING ||
                            it.phase == TaskProgress.Phase.SCANNING
                    }
                    if (!stillActive) {
                        stopForegroundCompat()
                        stopSelf()
                    }
                    return@collectLatest
                }
                val t = active.first()
                val msg = "${t.current}/${t.total} · ${t.currentFile}"
                val mgr = getSystemService(NOTIFICATION_SERVICE) as android.app.NotificationManager
                mgr.notify(NOTIF_ID, buildNotification(msg))
            }
        }
    }

    override fun onDestroy() {
        releaseWakeLock()
        super.onDestroy()
    }

    /**
     * Acquires a partial wake lock to keep the CPU running while the screen
     * is off. Without this, the system dozes the CPU when the screen turns
     * off, pausing all sync coroutines even though the foreground service
     * keeps the process alive.
     *
     * The lock has a 1-hour safety timeout so it can never be held
     * indefinitely if the service crashes or forgets to release it.
     */
    private fun acquireWakeLock() {
        val pm = getSystemService(POWER_SERVICE) as PowerManager
        wakeLock = pm.newWakeLock(
            PowerManager.PARTIAL_WAKE_LOCK,
            "mini-nas:sync",
        ).also { it.acquire(WAKE_LOCK_TIMEOUT_MS) }
    }

    private fun releaseWakeLock() {
        wakeLock?.let { if (it.isHeld) it.release() }
        wakeLock = null
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        super.onStartCommand(intent, flags, startId)
        return START_STICKY
    }

    private fun buildNotification(text: String): Notification {
        val intent = Intent(this, MainActivity::class.java)
        val flags = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M)
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        else PendingIntent.FLAG_UPDATE_CURRENT
        val pi = PendingIntent.getActivity(this, 0, intent, flags)
        return NotificationCompat.Builder(this, MiniNasApp.SYNC_CHANNEL_ID)
            .setContentTitle("Mini-NAS 同步")
            .setContentText(text)
            .setSmallIcon(R.drawable.ic_launcher_foreground)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setContentIntent(pi)
            .setProgress(0, 0, true)
            .build()
    }

    @Suppress("DEPRECATION")
    private fun stopForegroundCompat() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            stopForeground(STOP_FOREGROUND_REMOVE)
        } else {
            stopForeground(true)
        }
    }

    companion object {
        const val NOTIF_ID = 1001
        private const val STOP_GRACE_MS = 2000L
        private const val WAKE_LOCK_TIMEOUT_MS = 60 * 60 * 1000L // 1 hour safety net
        fun start(ctx: Context) {
            val intent = Intent(ctx, SyncService::class.java)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                ctx.startForegroundService(intent)
            } else {
                ctx.startService(intent)
            }
        }
    }
}
