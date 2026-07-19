package com.mininas.client

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.os.Build
import com.mininas.client.data.api.ApiProvider
import com.mininas.client.data.api.ServerUrlSelector
import com.mininas.client.data.api.UrlBuilder
import com.mininas.client.data.prefs.SettingsStore
import com.mininas.client.data.prefs.SyncStore
import com.mininas.client.data.repo.AuthRepository
import com.mininas.client.data.repo.FileRepository
import com.mininas.client.sync.SyncEngine
import com.mininas.client.sync.SyncProgressBus
import com.mininas.client.sync.SyncScheduler
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.launch

class MiniNasApp : Application() {

    lateinit var settings: SettingsStore
        private set
    lateinit var syncStore: SyncStore
        private set
    lateinit var apiProvider: ApiProvider
        private set
    lateinit var urlSelector: ServerUrlSelector
        private set
    lateinit var urlBuilder: UrlBuilder
        private set
    lateinit var authRepo: AuthRepository
        private set
    lateinit var fileRepo: FileRepository
        private set
    lateinit var syncEngine: SyncEngine
        private set
    val syncProgress = SyncProgressBus()

    private val _sessionExpired = MutableSharedFlow<Unit>(extraBufferCapacity = 1)
    val sessionExpired: SharedFlow<Unit> = _sessionExpired

    private val appScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    override fun onCreate() {
        super.onCreate()
        instance = this
        settings = SettingsStore(this)
        syncStore = SyncStore(this)
        apiProvider = ApiProvider(
            tokenProvider = { settings.tokenSync() },
            onAuthError = {
                appScope.launch { settings.clearAuth() }
                _sessionExpired.tryEmit(Unit)
            },
        )
        urlSelector = ServerUrlSelector(this, settings, apiProvider)
        apiProvider.setFallbackInterceptor(urlSelector)
        urlBuilder = UrlBuilder(
            baseUrlProvider = { urlSelector.currentSync() },
            tokenProvider = { settings.tokenSync() },
        )
        authRepo = AuthRepository(apiProvider, settings)
        fileRepo = FileRepository(apiProvider)
        syncEngine = SyncEngine(this)

        // On startup, force a fresh LAN probe so we begin with the right
        // route (the selector's init defaults to the public URL). The
        // selector pushes the result to apiProvider itself.
        appScope.launch { urlSelector.refresh() }

        // Re-evaluate the auto-sync schedule whenever either toggle changes.
        // combine() emits once both flows have produced an initial value and
        // re-emits on every subsequent change, so the schedule stays in sync
        // with the user's settings.
        appScope.launch {
            settings.autoSync.combine(settings.wifiOnly) { _, _ -> Unit }.collect {
                SyncScheduler.schedule(this@MiniNasApp)
            }
        }

        // Listen for network changes (Wi-Fi ↔ cellular, reconnect) so the
        // selector re-probes the LAN and switches routes promptly.
        urlSelector.registerNetworkCallback()

        createNotificationChannels()
    }

    private fun createNotificationChannels() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val mgr = getSystemService(NotificationManager::class.java)
            val ch = NotificationChannel(
                SYNC_CHANNEL_ID,
                getString(R.string.notification_channel_sync),
                NotificationManager.IMPORTANCE_LOW,
            ).apply {
                description = getString(R.string.notification_channel_sync_desc)
            }
            mgr.createNotificationChannel(ch)
        }
    }

    companion object {
        const val SYNC_CHANNEL_ID = "mini_nas_sync"

        @Volatile private var instance: MiniNasApp? = null
        fun get(): MiniNasApp = instance!!
    }
}
