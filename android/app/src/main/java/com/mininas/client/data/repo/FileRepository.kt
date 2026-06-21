package com.mininas.client.data.repo

import android.content.ContentResolver
import android.net.Uri
import com.mininas.client.data.api.ApiProvider
import com.mininas.client.data.api.CreateMountRequest
import com.mininas.client.data.api.DeleteRequest
import com.mininas.client.data.api.FileInfo
import com.mininas.client.data.api.FileList
import com.mininas.client.data.api.MkdirRequest
import com.mininas.client.data.api.Mount
import com.mininas.client.data.api.MoveCopyRequest
import com.mininas.client.data.api.RenameRequest
import com.mininas.client.data.api.SystemDir
import com.mininas.client.data.api.UpdateMountRequest
import okhttp3.MediaType
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.MultipartBody
import okhttp3.RequestBody
import okio.Buffer
import okio.BufferedSink
import okio.ForwardingSink
import okio.buffer
import java.io.File
import java.io.IOException

/**
 * High-level wrapper around [com.mininas.client.data.api.ApiService] for
 * file/mount operations. Upload progress is reported via [ProgressRequestBody].
 */
class FileRepository(
    private val apiProvider: ApiProvider,
) {

    private val api get() = apiProvider.current

    suspend fun listFiles(
        path: String? = null,
        page: Int? = null,
        pageSize: Int? = null,
        sortBy: String? = null,
        sortDesc: Boolean? = null,
    ): FileList = api.listFiles(path, page, pageSize, sortBy, sortDesc).unwrap()

    suspend fun getFileInfo(path: String): FileInfo = api.getFileInfo(path).unwrap()

    /**
     * Returns the [FileInfo] for [path], or null if the file does not exist
     * on the server. Network/other errors still throw.
     */
    suspend fun getFileInfoOrNull(path: String): FileInfo? {
        return try {
            api.getFileInfo(path).unwrap()
        } catch (e: retrofit2.HttpException) {
            if (e.code() == 404) null else throw e
        }
    }

    suspend fun search(q: String, path: String? = null): List<FileInfo> =
        api.searchFiles(q, path).unwrap()

    suspend fun uploadFile(
        path: String,
        file: File,
        mimeType: String? = null,
        onProgress: ((Float) -> Unit)? = null,
    ) {
        val mediaType = mimeType?.toMediaTypeOrNull()
        val rawBody = RequestBody.create(mediaType, file)
        val body = if (onProgress != null) ProgressRequestBody(rawBody, onProgress) else rawBody
        val part = MultipartBody.Part.createFormData("file", file.name, body)
        api.uploadFile(path, part).unwrap()
    }

    /**
     * 直接从 [uri] 流式上传，避免把文件先拷贝到临时文件造成磁盘/内存占用暴涨。
     * [size] 来自 SAF 的 DocumentFile.length()，用于 Content-Length 与进度计算。
     */
    suspend fun uploadFile(
        path: String,
        contentResolver: ContentResolver,
        uri: Uri,
        fileName: String,
        size: Long,
        mimeType: String? = null,
        onProgress: ((Float) -> Unit)? = null,
    ) {
        val mediaType = mimeType?.toMediaTypeOrNull()
        val rawBody = InputStreamRequestBody(contentResolver, uri, size, mediaType)
        val body = if (onProgress != null) ProgressRequestBody(rawBody, onProgress) else rawBody
        val part = MultipartBody.Part.createFormData("file", fileName, body)
        api.uploadFile(path, part).unwrap()
    }

    suspend fun mkdir(path: String) {
        api.mkdir(MkdirRequest(path)).unwrap()
    }

    suspend fun delete(path: String) {
        api.deleteFile(DeleteRequest(path)).unwrap()
    }

    suspend fun rename(path: String, newName: String) {
        api.renameFile(RenameRequest(path, newName)).unwrap()
    }

    suspend fun move(paths: List<String>, dest: String) {
        api.moveFiles(MoveCopyRequest(paths, dest)).unwrap()
    }

    suspend fun copy(paths: List<String>, dest: String) {
        api.copyFiles(MoveCopyRequest(paths, dest)).unwrap()
    }

    suspend fun listMounts(): List<Mount> = api.listMounts().unwrap()

    /**
     * 列出服务器宿主机文件系统的目录（用于挂载点路径选择器）。
     * 调用 GET /api/system/dirs，与 Web 端 DirTree 行为一致。
     */
    suspend fun listSystemDirs(path: String? = null): List<SystemDir> =
        api.listSystemDirs(path).unwrap()

    suspend fun createMount(name: String, path: String): Mount =
        api.createMount(CreateMountRequest(name, path)).unwrap()

    suspend fun updateMount(name: String, path: String): Mount =
        api.updateMount(name, UpdateMountRequest(path)).unwrap()

    suspend fun deleteMount(name: String) {
        api.deleteMount(name).unwrap()
    }
}

/**
 * RequestBody wrapper that reports upload progress as a 0..1 float. OkHttp
 * writes the content to the sink; we wrap the sink to count bytes.
 */
private class ProgressRequestBody(
    private val delegate: RequestBody,
    private val onProgress: (Float) -> Unit,
) : RequestBody() {

    override fun contentType() = delegate.contentType()
    override fun contentLength(): Long = delegate.contentLength()

    override fun writeTo(sink: BufferedSink) {
        val total = contentLength()
        val countingSink = object : ForwardingSink(sink) {
            private var bytesWritten = 0L

            override fun write(source: Buffer, byteCount: Long) {
                super.write(source, byteCount)
                bytesWritten += byteCount
                val p = if (total > 0) bytesWritten.toFloat() / total else 0f
                onProgress(p.coerceIn(0f, 1f))
            }
        }.buffer()

        delegate.writeTo(countingSink)
        countingSink.flush()
    }
}

/**
 * 直接从 ContentResolver 打开的 [uri] 流式读取内容，避免把整个文件
 * 先拷贝到临时文件。OkHttp 在重试时会再次调用 [writeTo]，因此每次
 * 调用都重新打开 InputStream。
 */
private class InputStreamRequestBody(
    private val contentResolver: ContentResolver,
    private val uri: Uri,
    private val size: Long,
    private val mediaType: MediaType?,
) : RequestBody() {

    override fun contentType(): MediaType? = mediaType

    override fun contentLength(): Long = size

    override fun writeTo(sink: BufferedSink) {
        val input = contentResolver.openInputStream(uri)
            ?: throw IOException("无法打开输入流: $uri")
        input.use { stream ->
            val buffer = ByteArray(64 * 1024)
            while (true) {
                val read = stream.read(buffer)
                if (read == -1) break
                sink.write(buffer, 0, read)
            }
        }
    }
}
