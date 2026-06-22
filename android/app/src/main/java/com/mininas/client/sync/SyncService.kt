package com.mininas.client.sync

import android.app.Notification
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build
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
 * only owns the lifecycle notification.
 */
class SyncService : LifecycleService() {

    override fun onCreate() {
        super.onCreate()
        startForeground(NOTIF_ID, buildNotification("准备同步…"))
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
            .setContentTitle("mini-nas 同步")
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
