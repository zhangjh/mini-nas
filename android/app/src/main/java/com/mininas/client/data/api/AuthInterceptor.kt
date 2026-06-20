package com.mininas.client.data.api

import okhttp3.Interceptor
import okhttp3.Response

/**
 * Adds `Authorization: Bearer <token>` to every outgoing request when a token
 * is available from the [tokenProvider]. The provider is a synchronous lambda
 * backed by an in-memory cache in [com.mininas.client.data.prefs.SettingsStore]
 * to avoid blocking the OkHttp dispatcher thread on DataStore IO.
 */
class AuthInterceptor(
    private val tokenProvider: () -> String?,
) : Interceptor {

    override fun intercept(chain: Interceptor.Chain): Response {
        val token = tokenProvider()
        val req = if (token.isNullOrBlank()) {
            chain.request()
        } else {
            chain.request().newBuilder()
                .addHeader("Authorization", "Bearer $token")
                .build()
        }
        return chain.proceed(req)
    }
}
