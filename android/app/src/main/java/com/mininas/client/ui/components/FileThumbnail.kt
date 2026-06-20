package com.mininas.client.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.AudioFile
import androidx.compose.material.icons.filled.Description
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Image
import androidx.compose.material.icons.filled.InsertDriveFile
import androidx.compose.material.icons.filled.PictureAsPdf
import androidx.compose.material.icons.filled.VideoFile
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import coil.compose.AsyncImage
import com.mininas.client.MiniNasApp
import com.mininas.client.data.api.FileInfo
import com.mininas.client.util.FileTypes

@Composable
fun FileThumbnail(
    file: FileInfo,
    modifier: Modifier = Modifier,
    size: Int = 48,
) {
    val app = MiniNasApp.get()
    val cat = FileTypes.category(file)
    when (cat) {
        FileTypes.Category.DIR -> IconBox(Icons.Filled.Folder, Color(0xFFE8B741), modifier, size)
        FileTypes.Category.IMAGE -> {
            val url = app.urlBuilder.thumbUrl(file.path)
            AsyncImage(
                model = url,
                contentDescription = file.name,
                modifier = modifier.size(size.dp).background(MaterialTheme.colorScheme.surfaceVariant, RoundedCornerShape(8.dp)),
            )
        }
        FileTypes.Category.VIDEO -> IconBox(Icons.Filled.VideoFile, Color(0xFF7B61FF), modifier, size)
        FileTypes.Category.AUDIO -> IconBox(Icons.Filled.AudioFile, Color(0xFF1FA8E0), modifier, size)
        FileTypes.Category.PDF -> IconBox(Icons.Filled.PictureAsPdf, Color(0xFFE5484D), modifier, size)
        FileTypes.Category.TEXT -> IconBox(Icons.Filled.Description, Color(0xFF30A46C), modifier, size)
        FileTypes.Category.OTHER -> IconBox(Icons.Filled.InsertDriveFile, Color(0xFF6B7280), modifier, size)
    }
}

@Composable
private fun IconBox(
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    tint: Color,
    modifier: Modifier,
    size: Int,
) {
    Box(
        modifier = modifier
            .size(size.dp)
            .background(tint.copy(alpha = 0.12f), RoundedCornerShape(8.dp)),
        contentAlignment = Alignment.Center,
    ) {
        Icon(icon, contentDescription = null, tint = tint, modifier = Modifier.size((size * 0.6f).dp))
    }
}
