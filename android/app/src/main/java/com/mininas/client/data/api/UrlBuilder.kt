package com.mininas.client.data.api

import java.net.URLEncoder

/**
 * Builds full URLs for endpoints that are consumed directly by image/video
 * loaders (Coil, ExoPlayer) or by [android.app.DownloadManager], which can't
 * go through Retrofit. The JWT is appended as a `token` query parameter since
 * those clients can't easily set the `Authorization` header.
 *
 * The server's auth middleware accepts both `Authorization: Bearer <token>`
 * and `?token=<token>` (see `server/internal/api/auth.go`).
 */
class UrlBuilder(
    private val baseUrlProvider: () -> String,
    private val tokenProvider: () -> String?,
) {

    private fun base(): String {
        val b = baseUrlProvider().trimEnd('/')
        if (b.isEmpty()) return "http://localhost:8080"
        return if (b.contains("://")) b else "http://$b"
    }

    private fun withToken(url: String): String {
        val token = tokenProvider() ?: return url
        val sep = if (url.contains('?')) '&' else '?'
        return "$url${sep}token=${URLEncoder.encode(token, "UTF-8")}"
    }

    private fun encode(path: String): String =
        URLEncoder.encode(path, "UTF-8")

    fun previewUrl(path: String): String =
        withToken("${base()}/api/files/preview?path=${encode(path)}")

    fun thumbUrl(path: String): String =
        withToken("${base()}/api/thumb?path=${encode(path)}")

    fun downloadUrl(path: String): String =
        withToken("${base()}/api/files/download?path=${encode(path)}")
}
