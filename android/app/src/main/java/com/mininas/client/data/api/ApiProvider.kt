package com.mininas.client.data.api

import retrofit2.converter.kotlinx.serialization.asConverterFactory
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
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
) {
    private val json: Json = Json {
        ignoreUnknownKeys = true
        coerceInputValues = true
        explicitNulls = false
    }

    private val authInterceptor = AuthInterceptor(tokenProvider)

    private val okHttp: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(60, TimeUnit.SECONDS)
        .writeTimeout(120, TimeUnit.SECONDS)
        .addInterceptor(authInterceptor)
        .addInterceptor(HttpLoggingInterceptor().apply {
            level = HttpLoggingInterceptor.Level.BASIC
        })
        .build()

    @Volatile
    var current: ApiService = build("http://localhost:8080/")
        private set

    /**
     * Rebuilds the Retrofit service against a new base URL. Safe to call from
     * the main thread; existing in-flight requests keep using the old client.
     */
    fun updateBaseUrl(rawUrl: String) {
        val normalized = normalize(rawUrl)
        if (normalized.isBlank()) return
        current = build(normalized)
    }

    private fun build(baseUrl: String): ApiService {
        val url = if (baseUrl.endsWith("/")) baseUrl else "$baseUrl/"
        return Retrofit.Builder()
            .baseUrl(url)
            .client(okHttp)
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
