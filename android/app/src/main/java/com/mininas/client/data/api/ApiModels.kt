package com.mininas.client.data.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Unified API envelope returned by the mini-nas server.
 * JSON keys are snake_case to match the Go server's `json` tags.
 */
@Serializable
data class ApiResponse<T>(
    val ok: Boolean = false,
    val data: T? = null,
    val error: String? = null,
)

@Serializable
data class FileInfo(
    val name: String,
    val path: String,
    val size: Long = 0,
    @SerialName("is_dir") val isDir: Boolean = false,
    @SerialName("mod_time") val modTime: Long = 0,
    val mode: Int = 0,
)

@Serializable
data class FileList(
    val items: List<FileInfo> = emptyList(),
    val total: Int = 0,
    val page: Int = 0,
    @SerialName("page_size") val pageSize: Int = 0,
    val path: String = "",
)

@Serializable
data class LoginRequest(
    val username: String,
    val password: String,
)

@Serializable
data class LoginResponse(
    val token: String,
    @SerialName("expires_at") val expiresAt: Long = 0,
    val username: String = "",
)

@Serializable
data class AuthStatus(
    @SerialName("has_users") val hasUsers: Boolean = false,
)

@Serializable
data class Mount(
    val id: String = "",
    val name: String,
    val path: String,
    @SerialName("created_at") val createdAt: Long = 0,
)

@Serializable
data class MkdirRequest(val path: String)

@Serializable
data class DeleteRequest(val path: String)

@Serializable
data class RenameRequest(
    val path: String,
    @SerialName("new_name") val newName: String,
)

@Serializable
data class MoveCopyRequest(
    val paths: List<String>,
    val dest: String,
)

@Serializable
data class CreateMountRequest(
    val name: String,
    val path: String,
)

@Serializable
data class UpdateMountRequest(val path: String)

@Serializable
data class UploadResult(
    val size: Long = 0,
    val path: String = "",
    val name: String = "",
)

@Serializable
data class SystemDir(
    val name: String,
    val path: String,
)
