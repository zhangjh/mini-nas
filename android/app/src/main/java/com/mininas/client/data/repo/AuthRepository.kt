package com.mininas.client.data.repo

import com.mininas.client.data.api.ApiProvider
import com.mininas.client.data.api.ApiResponse
import com.mininas.client.data.api.AuthStatus
import com.mininas.client.data.api.LoginRequest
import com.mininas.client.data.api.LoginResponse
import com.mininas.client.data.prefs.SettingsStore
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import retrofit2.HttpException

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

/**
 * Extracts a human-readable error message from an arbitrary [Throwable].
 *
 * For [HttpException] (non-2xx HTTP responses), parses the response body
 * as an [ApiResponse] and returns the server-provided `error` field —
 * e.g. "mount not found" instead of a bare "HTTP 500 Internal Server Error".
 * Falls back to the HTTP status line if the body can't be parsed.
 *
 * For [ApiException] and other exceptions, returns [Throwable.getMessage].
 */
fun Throwable.toErrorMessage(): String = when (this) {
    is HttpException -> {
        val body = runCatching { response()?.errorBody()?.string() }.getOrNull()
        if (body != null) {
            runCatching {
                val obj = Json { ignoreUnknownKeys = true }.parseToJsonElement(body).jsonObject
                obj["error"]?.jsonPrimitive?.content ?: "HTTP ${code()}"
            }.getOrNull() ?: "HTTP ${code()}"
        } else {
            "HTTP ${code()}"
        }
    }
    else -> message ?: "未知错误"
}
