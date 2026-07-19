package com.mininas.client.data.api

import retrofit2.converter.kotlinx.serialization.asConverterFactory
import kotlinx.serialization.json.Json
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Response
import okhttp3.logging.HttpLoggingInterceptor
import retrofit2.Retrofit
import java.util.concurrent.TimeUnit

/**
 * Owns the singleton [ApiService] instance and rebuilds it whenever the user
 * changes the server URL. The OkHttp client (and its [AuthInterceptor]) is
 * preserved across URL changes so that the auth header keeps being applied.
 */
class ApiProvider(
    private val tokenProvider: () -> String?,
    private val onAuthError: (() -> Unit)? = null,
) {
    private val json: Json = Json {
        ignoreUnknownKeys = true
        coerceInputValues = true
        explicitNulls = false
    }

    private val authInterceptor = AuthInterceptor(tokenProvider)
    private var fallbackInterceptor: FallbackInterceptor? = null

    private val okHttp: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(60, TimeUnit.SECONDS)
        .writeTimeout(120, TimeUnit.SECONDS)
        .addInterceptor(authInterceptor)
        .addInterceptor(authResponseInterceptor())
        .addInterceptor(HttpLoggingInterceptor().apply {
            level = HttpLoggingInterceptor.Level.BASIC
        })
        .build()

    fun setFallbackInterceptor(urlSelector: ServerUrlSelector) {
        fallbackInterceptor = FallbackInterceptor(urlSelector, this)
    }

    @Volatile
    var current: ApiService = build("http://localhost:8080/")
        private set

    /**
     * The normalized base URL currently in use, or empty if never set. Used to
     * skip no-op rebuilds in [updateBaseUrl].
     */
    @Volatile
    private var currentNormalizedUrl: String = ""

    /**
     * Rebuilds the Retrofit service against a new base URL. Safe to call from
     * the main thread; existing in-flight requests keep using the old client.
     *
     * No-op when [rawUrl] normalizes to the same value already in use — this
     * is important because [com.mininas.client.data.api.ServerUrlSelector]
     * emits its effective URL as a flow that may re-emit the same value.
     */
    fun updateBaseUrl(rawUrl: String) {
        val normalized = normalize(rawUrl)
        if (normalized.isBlank()) return
        if (normalized == currentNormalizedUrl) return
        current = build(normalized)
        currentNormalizedUrl = normalized
    }

    private fun authResponseInterceptor(): Interceptor = Interceptor { chain ->
        val response = chain.proceed(chain.request())
        if (response.code == 401) {
            onAuthError?.invoke()
        }
        response
    }

    private fun build(baseUrl: String): ApiService {
        val url = if (baseUrl.endsWith("/")) baseUrl else "$baseUrl/"
        val clientBuilder = okHttp.newBuilder()
        fallbackInterceptor?.let { clientBuilder.addInterceptor(it) }
        return Retrofit.Builder()
            .baseUrl(url)
            .client(clientBuilder.build())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(ApiService::class.java)
    }

    companion object {
        /**
         * Normalizes user-entered server URLs:
         * - defaults to `http://` when no scheme is present
         * - strips trailing slash
         * - strips any path component (we only want the origin)
         */
        fun normalize(input: String): String {
            var s = input.trim()
            if (s.isBlank()) return ""
            if (!s.contains("://")) s = "http://$s"
            // Keep scheme + host + port only; drop any path the user pasted.
            val schemeEnd = s.indexOf("://") + 3
            val pathStart = s.indexOf('/', schemeEnd)
            if (pathStart > 0) s = s.substring(0, pathStart)
            return s.trimEnd('/')
        }
    }
}
