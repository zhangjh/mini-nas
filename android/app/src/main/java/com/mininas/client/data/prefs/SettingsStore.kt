package com.mininas.client.data.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.runBlocking

private val Context.settingsDataStore: DataStore<Preferences> by preferencesDataStore(name = "mini_nas_settings")

/**
 * Persists user settings (server URL, JWT, username, sync options) in DataStore.
 *
 * The "server" can be configured with two addresses:
 * - [serverUrl]: the always-reachable public address (公网域名/IP).
 * - [lanUrl]: an optional LAN address used when the device is on the same
 *   network as the NAS, to speed up sync and avoid consuming mobile data
 *   plan bandwidth through the public route. Selection is handled by
 *   [com.mininas.client.data.api.ServerUrlSelector].
 *
 * To avoid blocking the OkHttp dispatcher thread on every network call, the
 * token and server URL are also cached in memory after the first read, so
 * [tokenSync] / [serverUrlSync] are safe to call from any thread.
 */
class SettingsStore(private val context: Context) {

    private val keyServerUrl = stringPreferencesKey("server_url")
    private val keyLanUrl = stringPreferencesKey("lan_url")
    private val keyToken = stringPreferencesKey("token")
    private val keyUsername = stringPreferencesKey("username")
    private val keyWifiOnly = booleanPreferencesKey("wifi_only")
    private val keyAutoSync = booleanPreferencesKey("auto_sync")

    // ---- in-memory caches (written from DataStore reads/writes) ----
    @Volatile private var tokenCache: String? = null
    @Volatile private var urlCache: String = ""
    @Volatile private var lanUrlCache: String = ""

    val serverUrl: Flow<String> = context.settingsDataStore.data.map { it[keyServerUrl] ?: "" }
    val lanUrl: Flow<String> = context.settingsDataStore.data.map { it[keyLanUrl] ?: "" }
    val token: Flow<String?> = context.settingsDataStore.data.map { it[keyToken] }
    val username: Flow<String> = context.settingsDataStore.data.map { it[keyUsername] ?: "" }
    val wifiOnly: Flow<Boolean> = context.settingsDataStore.data.map { it[keyWifiOnly] ?: true }
    val autoSync: Flow<Boolean> = context.settingsDataStore.data.map { it[keyAutoSync] ?: false }

    init {
        // Prime the in-memory caches synchronously on first construction.
        // This runs on the main thread during Application.onCreate, which is
        // acceptable since DataStore's first read is fast.
        runCatching {
            val prefs = runBlocking { context.settingsDataStore.data.first() }
            tokenCache = prefs[keyToken]
            urlCache = prefs[keyServerUrl] ?: ""
            lanUrlCache = prefs[keyLanUrl] ?: ""
        }
    }

    // ---- synchronous accessors for interceptors / URL builders ----

    fun tokenSync(): String? = tokenCache
    fun serverUrlSync(): String = urlCache
    fun lanUrlSync(): String = lanUrlCache

    // ---- suspend accessors ----

    suspend fun getServerUrl(): String = serverUrl.first()
    suspend fun getLanUrl(): String = lanUrl.first()
    suspend fun getToken(): String? = token.first()
    suspend fun getUsername(): String = username.first()

    // ---- mutators ----

    suspend fun setServerUrl(url: String) {
        val normalized = normalizeUrl(url)
        context.settingsDataStore.edit { it[keyServerUrl] = normalized }
        urlCache = normalized
    }

    suspend fun setLanUrl(url: String) {
        val normalized = normalizeUrl(url)
        context.settingsDataStore.edit { it[keyLanUrl] = normalized }
        lanUrlCache = normalized
    }

    private fun normalizeUrl(input: String): String {
        val s = input.trim()
        if (s.isBlank()) return ""
        if (s.contains("://")) return s
        // IPv4 地址使用 http，其他（域名）使用 https
        val isIpv4 = Regex("^\\d{1,3}\\.\\d{1,3}\\.\\d{1,3}\\.\\d{1,3}(:\\d+)?$").matches(s)
        return if (isIpv4) "http://$s" else "https://$s"
    }

    suspend fun setToken(token: String?) {
        context.settingsDataStore.edit {
            if (token.isNullOrBlank()) it.remove(keyToken) else it[keyToken] = token
        }
        tokenCache = token
    }

    suspend fun setUsername(name: String) {
        context.settingsDataStore.edit { it[keyUsername] = name }
    }

    suspend fun setWifiOnly(v: Boolean) {
        context.settingsDataStore.edit { it[keyWifiOnly] = v }
    }

    suspend fun setAutoSync(v: Boolean) {
        context.settingsDataStore.edit { it[keyAutoSync] = v }
    }

    /**
     * Clears token + username (used on logout). Server URL is preserved so the
     * user doesn't have to re-enter it on next login.
     */
    suspend fun clearAuth() {
        context.settingsDataStore.edit {
            it.remove(keyToken)
            it.remove(keyUsername)
        }
        tokenCache = null
    }
}
