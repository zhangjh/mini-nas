package com.mininas.client

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.os.Build
import com.mininas.client.data.api.ApiProvider
import com.mininas.client.data.api.UrlBuilder
import com.mininas.client.data.prefs.SettingsStore
import com.mininas.client.data.prefs.SyncStore
import com.mininas.client.data.repo.AuthRepository
import com.mininas.client.data.repo.FileRepository
import com.mininas.client.sync.SyncProgressBus
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch

class MiniNasApp : Application() {

    lateinit var settings: SettingsStore
        private set
    lateinit var syncStore: SyncStore
        private set
    lateinit var apiProvider: ApiProvider
        private set
    lateinit var urlBuilder: UrlBuilder
        private set
    lateinit var authRepo: AuthRepository
        private set
    lateinit var fileRepo: FileRepository
        private set
    val syncProgress = SyncProgressBus()

    private val appScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    override fun onCreate() {
        super.onCreate()
        instance = this
        settings = SettingsStore(this)
        syncStore = SyncStore(this)
        apiProvider = ApiProvider { settings.tokenSync() }
        urlBuilder = UrlBuilder(
            baseUrlProvider = { settings.serverUrlSync() },
            tokenProvider = { settings.tokenSync() },
        )
        authRepo = AuthRepository(apiProvider, settings)
        fileRepo = FileRepository(apiProvider)

        appScope.launch {
            val url = settings.getServerUrl()
            if (url.isNotBlank()) apiProvider.updateBaseUrl(url)
        }

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
