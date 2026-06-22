package com.mininas.client.data.api

import okhttp3.MultipartBody
import okhttp3.ResponseBody
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.Multipart
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Part
import retrofit2.http.Query

/**
 * Retrofit interface mirroring the server's `/api` routes defined in
 * `server/internal/api/router.go`.
 *
 * Endpoints that require JWT are protected server-side via middleware; the
 * `Authorization` header is added by [AuthInterceptor].
 */
interface ApiService {

    // ---- health & auth (public) ----

    @GET("/api/health")
    suspend fun health(): ApiResponse<Map<String, String>>

    @GET("/api/auth/status")
    suspend fun authStatus(): ApiResponse<AuthStatus>

    @POST("/api/auth/login")
    suspend fun login(@Body body: LoginRequest): ApiResponse<LoginResponse>

    @POST("/api/auth/register")
    suspend fun register(@Body body: LoginRequest): ApiResponse<LoginResponse>

    // ---- files ----

    @GET("/api/files")
    suspend fun listFiles(
        @Query("path") path: String? = null,
        @Query("page") page: Int? = null,
        @Query("page_size") pageSize: Int? = null,
        @Query("sort_by") sortBy: String? = null,
        @Query("sort_desc") sortDesc: Boolean? = null,
    ): ApiResponse<FileList>

    @GET("/api/files/tree")
    suspend fun listFileTree(@Query("path") path: String): ApiResponse<List<FileInfo>>

    @GET("/api/files/info")
    suspend fun getFileInfo(@Query("path") path: String): ApiResponse<FileInfo>

    @GET("/api/files/search")
    suspend fun searchFiles(
        @Query("q") q: String,
        @Query("path") path: String? = null,
    ): ApiResponse<List<FileInfo>>

    @Multipart
    @POST("/api/files/upload")
    suspend fun uploadFile(
        @Query("path") path: String,
        @Part part: MultipartBody.Part,
    ): ApiResponse<UploadResult>

    @GET("/api/files/download")
    suspend fun downloadFile(@Query("path") path: String): ResponseBody

    @GET("/api/files/preview")
    suspend fun previewFile(@Query("path") path: String): ResponseBody

    @GET("/api/thumb")
    suspend fun thumbnail(@Query("path") path: String): ResponseBody

    // ---- file operations ----

    @POST("/api/mkdir")
    suspend fun mkdir(@Body body: MkdirRequest): ApiResponse<Unit>

    @DELETE("/api/file")
    suspend fun deleteFile(@Body body: DeleteRequest): ApiResponse<Unit>

    @PUT("/api/file/rename")
    suspend fun renameFile(@Body body: RenameRequest): ApiResponse<Unit>

    @POST("/api/file/move")
    suspend fun moveFiles(@Body body: MoveCopyRequest): ApiResponse<Unit>

    @POST("/api/file/copy")
    suspend fun copyFiles(@Body body: MoveCopyRequest): ApiResponse<Unit>

    // ---- system dirs (for mount picker) ----

    @GET("/api/system/dirs")
    suspend fun listSystemDirs(@Query("path") path: String? = null): ApiResponse<List<SystemDir>>

    // ---- mounts ----

    @GET("/api/mounts")
    suspend fun listMounts(): ApiResponse<List<Mount>>

    @POST("/api/mounts")
    suspend fun createMount(@Body body: CreateMountRequest): ApiResponse<Mount>

    @PUT("/api/mounts")
    suspend fun updateMount(
        @Query("name") name: String,
        @Body body: UpdateMountRequest,
    ): ApiResponse<Mount>

    @DELETE("/api/mounts")
    suspend fun deleteMount(@Query("name") name: String): ApiResponse<Unit>
}
