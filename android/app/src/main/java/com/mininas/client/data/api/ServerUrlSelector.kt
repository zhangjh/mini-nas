package com.mininas.client.data.api

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import com.mininas.client.data.prefs.SettingsStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.net.InetSocketAddress
import java.net.Socket

/**
 * Picks the effective base URL to talk to the NAS from the two configured
 * addresses in [SettingsStore]:
 *
 * - **Public URL** ([SettingsStore.serverUrl]): always reachable, used as the
 *   fallback. Required.
 * - **LAN URL** ([SettingsStore.lanUrl]): optional. When the device is on the
 *   same network as the NAS, the LAN route is faster and doesn't consume the
 *   user's mobile/cellular quota on the public side.
 *
 * Detection is done by a short TCP connect probe to the LAN host:port. If the
 * connect succeeds within [LAN_PROBE_TIMEOUT_MS], the device is considered to
 * be on the internal network and the LAN URL is preferred. The result is
 * cached for [PROBE_TTL_MS] and additionally invalidated whenever the system
 * reports a network change (Wi-Fi ↔ cellular, reconnection, etc.).
 *
 * The selected URL is exposed both synchronously (cached, for [UrlBuilder] /
 * [AuthInterceptor] which run on OkHttp dispatcher threads) and as a
 * [StateFlow] (for [ApiProvider] to rebuild Retrofit against).
 *
 * When no LAN URL is configured, the selector is a transparent pass-through
 * of the public URL — no probing is performed.
 */
class ServerUrlSelector(
    private val context: Context,
    private val settings: SettingsStore,
    private val apiProvider: ApiProvider,
) {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val probeMutex = Mutex()

    /** Cached effective URL; the single source of truth consumed by [ApiProvider]. */
    private val _effectiveUrl = MutableStateFlow("")
    val effectiveUrl: StateFlow<String> = _effectiveUrl.asStateFlow()

    @Volatile private var lastProbeAt: Long = 0L
    @Volatile private var lanReachable: Boolean = false
    @Volatile private var lastProbedLanUrl: String = ""

    init {
        // Prime the cache from the in-memory settings cache so that the very
        // first OkHttp request (before the async refresh completes) uses the
        // public URL rather than the ApiProvider's localhost default.
        // ALWAYS start with the public URL as the safe fallback — LAN is only
        // used after a successful probe.
        val publicUrl = settings.serverUrlSync()
        if (publicUrl.isNotBlank()) {
            applyEffectiveToUrl(publicUrl)
        }
    }

    /**
     * Returns the cached effective URL without performing any IO. Safe to call
     * from any thread, including the OkHttp dispatcher.
     *
     * The returned value may be stale (up to [PROBE_TTL_MS] old); callers that
     * need a fresh decision should use [refresh] (suspend) instead.
     */
    fun currentSync(): String = _effectiveUrl.value

    fun lanUrlSync(): String = settings.lanUrlSync()

    fun publicUrlSync(): String = settings.serverUrlSync()

    fun markLanUnreachable() {
        lanReachable = false
        lastProbeAt = 0L
    }

    /**
     * Forces a re-probe of the LAN URL and returns the resulting effective
     * URL. Safe to call concurrently — parallel callers share a single probe
     * via [probeMutex].
     *
     * This is the entry point used by login/settings screens after the user
     * edits the addresses, and by the sync worker before a run. When the
     * effective URL changes, [apiProvider] is rebuilt synchronously inside
     * this call so the very next Retrofit request uses the new URL.
     *
     * Important: during probing, [apiProvider] is temporarily switched to the
     * public URL as a safety fallback. This ensures that requests issued
     * immediately after this call (e.g., login) don't fail against an
     * unreachable LAN address while the probe is still running.
     *
     * LAN probing is performed regardless of network type (Wi-Fi or cellular).
     * The only reliable way to determine if we're on the internal network is
     * to actually try connecting to the configured LAN address.
     */
    suspend fun refresh(): String = probeMutex.withLock {
        val publicUrl = settings.serverUrlSync()
        val lanUrl = settings.lanUrlSync()

        // Immediately switch to the public URL before probing. This is
        // critical for login/settings flows where the user may be on a
        // public network but has previously configured a LAN address. Without
        // this, the probe would take 1.5 seconds to timeout, and the login
        // request would fail against the unreachable LAN address.
        if (publicUrl.isNotBlank()) {
            applyEffectiveToUrl(publicUrl)
        }

        val reachable = if (lanUrl.isBlank()) {
            // No LAN URL configured — skip probing entirely.
            false
        } else {
            // Actually try connecting to the LAN address. If it succeeds, we're
            // on the internal network and should use the faster LAN route.
            probeLanInternal(lanUrl)
        }

        lanReachable = reachable
        lastProbedLanUrl = lanUrl
        lastProbeAt = System.currentTimeMillis()

        // Now apply the final decision: if LAN probe succeeded, switch back
        // to LAN; otherwise stay on public (already applied above).
        applyEffective(publicUrl, lanUrl, reachable)
    }

    /**
     * Returns the effective URL, re-probing only if the cache is stale or the
     * configured LAN URL has changed since the last probe. Cheaper than
     * [refresh] for hot paths.
     */
    suspend fun current(): String {
        val lanUrl = settings.lanUrlSync()
        val stale = System.currentTimeMillis() - lastProbeAt > PROBE_TTL_MS
        val lanChanged = lanUrl != lastProbedLanUrl
        if (stale || lanChanged) return refresh()
        return _effectiveUrl.value
    }

    /**
     * Marks the cache stale and kicks off an async re-probe. Intended for
     * network-change callbacks (which arrive on a binder thread and can't
     * block).
     *
     * The cached effective URL is immediately reset to the public URL so that
     * requests issued during the re-probe window (a few hundred ms) don't
     * fail against a LAN address that may no longer be reachable after the
     * network change. If the re-probe then succeeds, [refresh] flips it back
     * to the LAN URL.
     */
    fun invalidate() {
        lastProbeAt = 0L
        // Immediate safe fallback to the public URL while we re-probe.
        val publicUrl = settings.serverUrlSync()
        if (publicUrl.isNotBlank()) {
            applyEffectiveToUrl(publicUrl)
        }
        // Coalesce: cancel any pending async refresh and start a new one.
        asyncRefreshJob?.cancel()
        asyncRefreshJob = scope.launch { refresh() }
    }

    @Volatile private var asyncRefreshJob: Job? = null

    /**
     * Recomputes the cached effective URL synchronously from the in-memory
     * settings caches, WITHOUT probing. The LAN URL is assumed unreachable
     * until a probe completes. This keeps the cache consistent immediately
     * after settings edits, so [currentSync] never returns a stale-by-config
     * value while a probe is in flight.
     */
    private fun recomputeSync() {
        val publicUrl = settings.serverUrlSync()
        val lanUrl = settings.lanUrlSync()
        // If LAN URL changed, drop the stale probe result so the next
        // refresh() actually probes the new host.
        if (lanUrl != lastProbedLanUrl) {
            lastProbedLanUrl = lanUrl
            lanReachable = false
            lastProbeAt = 0L
        }
        applyEffective(publicUrl, lanUrl, lanReachable)
    }

    /**
     * Picks the effective URL and pushes it to [_effectiveUrl] and
     * [apiProvider] if it changed. Returns the effective URL.
     */
    private fun applyEffective(publicUrl: String, lanUrl: String, lanReachable: Boolean): String {
        val effective = pickEffective(publicUrl, lanUrl, lanReachable)
        applyEffectiveToUrl(effective)
        return effective
    }

    private fun applyEffectiveToUrl(effective: String) {
        if (effective != _effectiveUrl.value) {
            _effectiveUrl.value = effective
        }
        // Always sync ApiProvider — it's a no-op when the URL is unchanged
        // (see ApiProvider.updateBaseUrl), and this covers the first apply
        // where _effectiveUrl was "" but ApiProvider still has its default.
        if (effective.isNotBlank()) {
            apiProvider.updateBaseUrl(effective)
        }
    }

    private fun pickEffective(publicUrl: String, lanUrl: String, lanReachable: Boolean): String {
        // Prefer LAN when configured AND the last probe succeeded.
        if (lanUrl.isNotBlank() && lanReachable) return ApiProvider.normalize(lanUrl)
        // Fall back to public URL.
        if (publicUrl.isNotBlank()) return ApiProvider.normalize(publicUrl)
        // Last resort: if only LAN is configured, use it (will likely fail,
        // but it's the user's only configured address).
        if (lanUrl.isNotBlank()) return ApiProvider.normalize(lanUrl)
        return ""
    }

    /**
     * Performs the TCP connect probe. Runs on [Dispatchers.IO]. Returns true
     * if the LAN host accepted a TCP connection within [LAN_PROBE_TIMEOUT_MS].
     *
     * A successful TCP handshake is sufficient evidence that the NAS is
     * reachable on the LAN: the host:port is the user's own NAS address, so
     * a false positive (some other device at the same IP:port) is extremely
     * unlikely and self-corrects on the next request.
     */
    private suspend fun probeLanInternal(lanUrl: String): Boolean = withContext(Dispatchers.IO) {
        val (host, port) = parseHostPort(lanUrl) ?: return@withContext false
        runCatching {
            Socket().use { socket ->
                socket.connect(InetSocketAddress(host, port), LAN_PROBE_TIMEOUT_MS)
                socket.isConnected
            }
        }.getOrDefault(false).also { tcpReachable ->
            if (!tcpReachable) return@withContext false
        }

        runCatching {
            withContext(Dispatchers.IO) {
                val normalizedLanUrl = ApiProvider.normalize(lanUrl)
                val healthUrl = if (normalizedLanUrl.endsWith("/")) "${normalizedLanUrl}api/health" else "${normalizedLanUrl}/api/health"
                
                val okHttpClient = okhttp3.OkHttpClient.Builder()
                    .connectTimeout(LAN_PROBE_TIMEOUT_MS.toLong(), java.util.concurrent.TimeUnit.MILLISECONDS)
                    .readTimeout(LAN_PROBE_TIMEOUT_MS.toLong(), java.util.concurrent.TimeUnit.MILLISECONDS)
                    .writeTimeout(LAN_PROBE_TIMEOUT_MS.toLong(), java.util.concurrent.TimeUnit.MILLISECONDS)
                    .build()
                
                val request = okhttp3.Request.Builder()
                    .url(healthUrl)
                    .get()
                    .build()
                
                okHttpClient.newCall(request).execute().use { response ->
                    response.isSuccessful
                }
            }
        }.getOrDefault(false)
    }

    /**
     * Extracts (host, port) from a URL string, applying sensible defaults:
     - scheme defaults to http://
     - port defaults to 80 for http, 443 for https
     */
    private fun parseHostPort(raw: String): Pair<String, Int>? {
        val s = raw.trim()
        if (s.isBlank()) return null
        val withScheme = if (s.contains("://")) s else "http://$s"
        // Use java.net.URL which exposes getDefaultPort() for scheme-based
        // defaults (URI does not).
        val url = runCatching { java.net.URL(withScheme) }.getOrNull() ?: return null
        val host = url.host
        if (host.isBlank()) return null
        val port = url.port.takeIf { it > 0 }
            ?: url.defaultPort.takeIf { it > 0 }
            ?: if (url.protocol.equals("https", true)) 443 else 80
        return host to port
    }

    /**
     * Registers a [ConnectivityManager.NetworkCallback] that calls
     * [invalidate] whenever the default network changes (Wi-Fi ↔ cellular,
     * reconnect, etc.). Must be called once from [com.mininas.client.MiniNasApp].
     */
    fun registerNetworkCallback() {
        val cm = context.getSystemService(Context.CONNECTIVITY_SERVICE) as? ConnectivityManager
            ?: return
        val callback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) { invalidate() }
            override fun onLost(network: Network) { invalidate() }
            override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) {
                // Transport changed (e.g. Wi-Fi → cellular) without onLost:
                // invalidate so we re-probe against the new transport.
                invalidate()
            }
        }
        runCatching { cm.registerDefaultNetworkCallback(callback) }
    }

    companion object {
        /** TCP connect timeout for the LAN probe. Short, since LAN is either
         *  reachable in <100ms or not at all. */
        private const val LAN_PROBE_TIMEOUT_MS = 1500

        /** How long a successful probe result is trusted before re-probing. */
        private const val PROBE_TTL_MS = 30_000L
    }
}
