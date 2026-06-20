package com.mininas.client.ui.files

import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.Checkbox
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.mininas.client.data.api.FileInfo
import com.mininas.client.ui.components.FileThumbnail
import com.mininas.client.util.FormatUtil

@Composable
fun FileList(
    items: List<FileInfo>,
    selection: Set<String>,
    onNavigate: (FileInfo) -> Unit,
    onPreview: (FileInfo) -> Unit,
    onMenu: (FileInfo, FileMenuAction) -> Unit,
    onToggleSelect: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    LazyColumn(modifier = modifier.fillMaxSize()) {
        items(items, key = { it.path }) { file ->
            FileRow(
                file = file,
                selected = file.path in selection,
                inSelectionMode = selection.isNotEmpty(),
                onClick = {
                    if (selection.isNotEmpty()) onToggleSelect(file.path)
                    else if (file.isDir) onNavigate(file)
                    else onPreview(file)
                },
                onLongClick = { onToggleSelect(file.path) },
                onMenu = { action -> onMenu(file, action) },
            )
        }
    }
}

@Composable
fun FileGrid(
    items: List<FileInfo>,
    selection: Set<String>,
    onNavigate: (FileInfo) -> Unit,
    onPreview: (FileInfo) -> Unit,
    onMenu: (FileInfo, FileMenuAction) -> Unit,
    onToggleSelect: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    LazyVerticalGrid(
        columns = GridCells.Adaptive(minSize = 110.dp),
        modifier = modifier.fillMaxSize().padding(8.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        items(items, key = { it.path }) { file ->
            FileGridCell(
                file = file,
                selected = file.path in selection,
                inSelectionMode = selection.isNotEmpty(),
                onClick = {
                    if (selection.isNotEmpty()) onToggleSelect(file.path)
                    else if (file.isDir) onNavigate(file)
                    else onPreview(file)
                },
                onLongClick = { onToggleSelect(file.path) },
                onMenu = { action -> onMenu(file, action) },
            )
        }
    }
}

enum class FileMenuAction { RENAME, DELETE, DOWNLOAD, MOVE }

@Composable
private fun FileRow(
    file: FileInfo,
    selected: Boolean,
    inSelectionMode: Boolean,
    onClick: () -> Unit,
    onLongClick: () -> Unit,
    onMenu: (FileMenuAction) -> Unit,
) {
    var menuOpen by remember { mutableStateOf(false) }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .background(if (selected) MaterialTheme.colorScheme.primary.copy(alpha = 0.1f) else androidx.compose.ui.graphics.Color.Transparent)
            .combinedClickable(onClick = onClick, onLongClick = onLongClick)
            .padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (inSelectionMode) {
            Checkbox(checked = selected, onCheckedChange = { onLongClick() })
            Spacer(Modifier.width(8.dp))
        }
        FileThumbnail(file = file, size = 44)
        Spacer(Modifier.width(12.dp))
        Column(modifier = Modifier.weight(1f)) {
            Text(
                file.name,
                style = MaterialTheme.typography.bodyLarge,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                if (file.isDir) "目录" else FormatUtil.size(file.size) + " · " + FormatUtil.time(file.modTime),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Box {
            IconButton(onClick = { menuOpen = true }) {
                Icon(Icons.Filled.MoreVert, contentDescription = "更多")
            }
            DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                if (!file.isDir) {
                    DropdownMenuItem(text = { Text("下载") }, onClick = { menuOpen = false; onMenu(FileMenuAction.DOWNLOAD) })
                }
                DropdownMenuItem(text = { Text("重命名") }, onClick = { menuOpen = false; onMenu(FileMenuAction.RENAME) })
                DropdownMenuItem(text = { Text("移动") }, onClick = { menuOpen = false; onMenu(FileMenuAction.MOVE) })
                DropdownMenuItem(text = { Text("删除") }, onClick = { menuOpen = false; onMenu(FileMenuAction.DELETE) })
            }
        }
    }
}

@Composable
private fun FileGridCell(
    file: FileInfo,
    selected: Boolean,
    inSelectionMode: Boolean,
    onClick: () -> Unit,
    onLongClick: () -> Unit,
    onMenu: (FileMenuAction) -> Unit,
) {
    var menuOpen by remember { mutableStateOf(false) }
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(12.dp))
            .background(
                if (selected) MaterialTheme.colorScheme.primary.copy(alpha = 0.12f)
                else MaterialTheme.colorScheme.surface
            )
            .combinedClickable(onClick = onClick, onLongClick = onLongClick)
            .padding(8.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Box(contentAlignment = Alignment.TopEnd) {
            FileThumbnail(file = file, size = 80)
            if (inSelectionMode) {
                Checkbox(checked = selected, onCheckedChange = { onLongClick() }, modifier = Modifier.size(24.dp))
            }
        }
        Spacer(Modifier.height(6.dp))
        Text(
            file.name,
            style = MaterialTheme.typography.bodySmall,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        Text(
            if (file.isDir) "目录" else FormatUtil.size(file.size),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
        )
    }
}
