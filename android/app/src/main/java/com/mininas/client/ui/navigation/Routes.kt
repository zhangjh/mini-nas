package com.mininas.client.ui.navigation

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CloudSync
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Settings
import androidx.compose.ui.graphics.vector.ImageVector

enum class TopRoute(val label: String, val icon: ImageVector) {
    Files("文件", Icons.Filled.Folder),
    Sync("同步", Icons.Filled.CloudSync),
    Settings("设置", Icons.Filled.Settings),
}

object Routes {
    const val LOGIN = "login"
    const val FILES = "files"
    const val SYNC = "sync"
    const val SETTINGS = "settings"
    const val PREVIEW = "preview/{path}"
    const val ADD_SYNC_TASK = "add_sync_task"
    const val SYNC_HISTORY = "sync_history"
    const val STORAGE = "storage"

    fun preview(path: String) = "preview/${encode(path)}"
    fun encode(s: String): String =
        android.net.Uri.encode(s)
}
