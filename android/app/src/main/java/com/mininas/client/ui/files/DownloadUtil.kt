package com.mininas.client.ui.files

import android.content.Context
import android.widget.Toast
import com.mininas.client.MiniNasApp
import com.mininas.client.data.api.FileInfo

/**
 * Enqueues a download via system DownloadManager. Used by both the file list
 * menu and the preview screen.
 */
fun downloadToExternal(ctx: Context, file: FileInfo) {
    val app = MiniNasApp.get()
    val url = app.urlBuilder.downloadUrl(file.path)
    val request = android.app.DownloadManager.Request(android.net.Uri.parse(url))
        .setTitle(file.name)
        .setNotificationVisibility(android.app.DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
        .setDestinationInExternalFilesDir(ctx, null, file.name)
    val mgr = ctx.getSystemService(Context.DOWNLOAD_SERVICE) as android.app.DownloadManager
    mgr.enqueue(request)
    Toast.makeText(ctx, "开始下载 ${file.name}", Toast.LENGTH_SHORT).show()
}
