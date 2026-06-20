package com.mininas.client.data.repo

import com.mininas.client.data.api.ApiProvider
import com.mininas.client.data.api.CreateMountRequest
import com.mininas.client.data.api.DeleteRequest
import com.mininas.client.data.api.FileInfo
import com.mininas.client.data.api.FileList
import com.mininas.client.data.api.MkdirRequest
import com.mininas.client.data.api.Mount
import com.mininas.client.data.api.MoveCopyRequest
import com.mininas.client.data.api.RenameRequest
import com.mininas.client.data.api.UpdateMountRequest
import okhttp3.MediaType
import okhttp3.MultipartBody
import okhttp3.RequestBody
import okio.Buffer
import okio.BufferedSink
import okio.ForwardingSource
import okio.buffer
import java.io.File

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

    suspend fun search(q: String, path: String? = null): List<FileInfo> =
        api.searchFiles(q, path).unwrap()

    suspend fun uploadFile(
        path: String,
        file: File,
        mimeType: String? = null,
        onProgress: ((Float) -> Unit)? = null,
    ) {
        val mediaType = mimeType?.let { MediaType.parse(it) }
        val rawBody = RequestBody.create(mediaType, file)
        val body = if (onProgress != null) ProgressRequestBody(rawBody, onProgress) else rawBody
        val part = MultipartBody.Part.createFormData("file", file.name, body)
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
 * reads the source in chunks; we count bytes read and invoke the callback.
 */
private class ProgressRequestBody(
    private val delegate: RequestBody,
    private val onProgress: (Float) -> Unit,
) : RequestBody() {

    override fun contentType() = delegate.contentType()
    override fun contentLength(): Long = delegate.contentLength()

    override fun writeTo(sink: BufferedSink) {
        val total = contentLength()
        val countingSource = object : ForwardingSource(delegate.source()) {
            var sent = 0L
            override fun read(sink: Buffer, byteCount: Long): Long {
                val read = super.read(sink, byteCount)
                if (read > 0) {
                    sent += read
                    val p = if (total > 0) sent.toFloat() / total else 0f
                    onProgress(p.coerceIn(0f, 1f))
                }
                return read
            }
        }
        sink.writeAll(countingSource.buffer())
    }
}
