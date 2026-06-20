package com.mininas.client.data.repo

import com.mininas.client.data.api.ApiProvider
import com.mininas.client.data.api.ApiResponse
import com.mininas.client.data.api.AuthStatus
import com.mininas.client.data.api.LoginRequest
import com.mininas.client.data.api.LoginResponse
import com.mininas.client.data.prefs.SettingsStore

/**
 * Wraps auth-related API calls and persists the resulting token/username.
 * Throws [ApiException] when the server returns `ok=false` so callers can
 * surface the error message directly.
 */
class AuthRepository(
    private val apiProvider: ApiProvider,
    private val settings: SettingsStore,
) {

    suspend fun status(): AuthStatus {
        return apiProvider.current.authStatus().unwrap()
    }

    suspend fun login(username: String, password: String): LoginResponse {
        val resp = apiProvider.current.login(LoginRequest(username, password))
        val data = resp.unwrap()
        settings.setToken(data.token)
        settings.setUsername(data.username)
        return data
    }

    suspend fun register(username: String, password: String): LoginResponse {
        val resp = apiProvider.current.register(LoginRequest(username, password))
        val data = resp.unwrap()
        settings.setToken(data.token)
        settings.setUsername(data.username)
        return data
    }

    suspend fun logout() {
        settings.clearAuth()
    }
}

/**
 * Thrown when the server returns a non-OK ApiResponse. The message is the
 * server-provided error string (or a generic fallback).
 */
class ApiException(message: String) : RuntimeException(message)

/**
 * Unwraps an [ApiResponse], throwing [ApiException] if `ok=false` or `data`
 * is null. Convenience extension to keep call sites concise.
 */
fun <T> ApiResponse<T>.unwrap(): T {
    if (!ok) throw ApiException(error ?: "请求失败")
    return data ?: throw ApiException("响应数据为空")
}
