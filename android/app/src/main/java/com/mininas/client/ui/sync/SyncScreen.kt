package com.mininas.client.ui.sync

import android.content.Intent
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Stop
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.mininas.client.data.prefs.SyncHistoryEntry
import com.mininas.client.data.prefs.SyncTaskDef
import com.mininas.client.sync.TaskProgress
import com.mininas.client.ui.picker.DirPickerScreen
import com.mininas.client.util.FormatUtil

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SyncScreen(
    viewModel: SyncViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsState()
    val progress by viewModel.progress.collectAsState()
    val snackbar = remember { SnackbarHostState() }
    var showAdd by remember { mutableStateOf(false) }
    var pickedServerDir by remember { mutableStateOf("") }
    var showPicker by remember { mutableStateOf(false) }

    LaunchedEffect(state.toast) {
        state.toast?.let {
            snackbar.showSnackbar(it)
            viewModel.consumeToast()
        }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = { TopAppBar(title = { Text("同步任务") }) },
        floatingActionButton = {
            FloatingActionButton(onClick = { showAdd = true }, containerColor = MaterialTheme.colorScheme.primary) {
                Icon(Icons.Filled.Add, contentDescription = "添加同步任务")
            }
        },
    ) { padding ->
        if (state.tasks.isEmpty()) {
            Box(modifier = Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                Text("暂无同步任务，点击右下角添加", color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        } else {
            LazyColumn(
                modifier = Modifier.fillMaxSize().padding(padding),
                verticalArrangement = Arrangement.spacedBy(8.dp),
                contentPadding = androidx.compose.foundation.layout.PaddingValues(12.dp),
            ) {
                items(state.tasks, key = { it.id }) { task ->
                    val p = progress[task.id]
                    SyncTaskCard(
                        task = task,
                        progress = p,
                        onCancel = { viewModel.cancelTask(task.id) },
                        onStart = { viewModel.startTask(task) },
                        onRemove = { viewModel.removeTask(task.id) },
                    )
                }
                item {
                    Spacer(Modifier.height(12.dp))
                    Text("同步历史", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(horizontal = 4.dp))
                    Spacer(Modifier.height(4.dp))
                    if (state.history.isEmpty()) {
                        Text("暂无历史", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(8.dp))
                    } else {
                        Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                            TextButton(onClick = { viewModel.clearHistory() }) { Text("清空历史") }
                        }
                        state.history.take(50).forEach { h ->
                            HistoryRow(h)
                            HorizontalDivider()
                        }
                    }
                }
            }
        }
    }

    if (showAdd && !showPicker) {
        AddSyncDialog(
            initialTarget = pickedServerDir,
            onDismiss = { showAdd = false; pickedServerDir = "" },
            onConfirm = { name, srcUri, srcName, target ->
                viewModel.addTask(name, srcUri, srcName, target)
                showAdd = false
                pickedServerDir = ""
            },
            onPickServerDir = { showPicker = true },
        )
    }

    if (showPicker) {
        DirPickerScreen(
            initialPath = pickedServerDir,
            virtual = true,
            onConfirm = { picked ->
                pickedServerDir = picked
                showPicker = false
            },
            onBack = { showPicker = false },
        )
    }
}

@Composable
private fun SyncTaskCard(
    task: SyncTaskDef,
    progress: TaskProgress?,
    onCancel: () -> Unit,
    onStart: () -> Unit,
    onRemove: () -> Unit,
) {
    Card(
        modifier = Modifier.fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
    ) {
        Column(modifier = Modifier.padding(14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(task.name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Text(
                        "手机: ${task.sourceDisplayName}",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1, overflow = TextOverflow.Ellipsis,
                    )
                    Text(
                        "服务器: ${task.targetPath}",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1, overflow = TextOverflow.Ellipsis,
                    )
                }
                val running = progress?.phase == TaskProgress.Phase.UPLOADING ||
                    progress?.phase == TaskProgress.Phase.SCANNING
                IconButton(onClick = if (running) onCancel else onStart) {
                    Icon(
                        if (running) Icons.Filled.Stop else Icons.Filled.PlayArrow,
                        contentDescription = if (running) "停止" else "开始",
                    )
                }
                IconButton(onClick = onRemove) {
                    Icon(Icons.Filled.Delete, contentDescription = "删除任务")
                }
            }

            progress?.let { p ->
                Spacer(Modifier.height(8.dp))
                if (p.total > 0) {
                    LinearProgressIndicator(
                        progress = { p.current.toFloat() / p.total },
                        modifier = Modifier.fillMaxWidth(),
                    )
                } else if (p.phase == TaskProgress.Phase.SCANNING) {
                    LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
                }
                Spacer(Modifier.height(4.dp))
                Text(
                    when (p.phase) {
                        TaskProgress.Phase.IDLE -> "未开始"
                        TaskProgress.Phase.SCANNING -> p.message.ifBlank { "扫描中…" }
                        TaskProgress.Phase.UPLOADING ->
                            "上传 ${p.current}/${p.total} · ${p.currentFile} (${(p.fileProgress * 100).toInt()}%)" +
                                if (p.skipped > 0) " · 已跳过 ${p.skipped}" else ""
                        TaskProgress.Phase.DONE -> "完成：成功 ${p.succeeded}，跳过 ${p.skipped}，失败 ${p.failed}"
                        TaskProgress.Phase.FAILED -> "失败：${p.message}"
                        TaskProgress.Phase.CANCELLED -> "已取消"
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 2, overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

@Composable
private fun HistoryRow(h: SyncHistoryEntry) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp, horizontal = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(h.relativePath, style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
            val statusText = buildString {
                append(h.status)
                append(" · ")
                append(FormatUtil.size(h.size))
                if (h.message.isNotEmpty()) {
                    append(" · ")
                    append(h.message)
                }
            }
            Text(
                statusText,
                style = MaterialTheme.typography.labelSmall,
                color = when (h.status) {
                    "success" -> MaterialTheme.colorScheme.primary
                    "skipped" -> MaterialTheme.colorScheme.tertiary
                    else -> MaterialTheme.colorScheme.error
                },
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Text(
            FormatUtil.time(h.timestamp),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun AddSyncDialog(
    initialTarget: String,
    onDismiss: () -> Unit,
    onConfirm: (name: String, srcUri: String, srcName: String, target: String) -> Unit,
    onPickServerDir: () -> Unit,
) {
    var name by remember { mutableStateOf("") }
    var srcUri by remember { mutableStateOf("") }
    var srcName by remember { mutableStateOf("") }
    var target by remember { mutableStateOf(initialTarget) }
    val ctx = LocalContext.current

    // Reflect externally-picked server dir (picker overlays this dialog).
    androidx.compose.runtime.LaunchedEffect(initialTarget) {
        if (initialTarget.isNotEmpty()) target = initialTarget
    }

    val pickDir = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.OpenDocumentTree(),
    ) { uri ->
        if (uri != null) {
            runCatching {
                ctx.contentResolver.takePersistableUriPermission(
                    uri, Intent.FLAG_GRANT_READ_URI_PERMISSION,
                )
            }
            srcUri = uri.toString()
            srcName = runCatching {
                androidx.documentfile.provider.DocumentFile.fromTreeUri(ctx, uri)?.name
                    ?: uri.lastPathSegment.orEmpty()
            }.getOrDefault(uri.toString())
            if (name.isBlank()) name = srcName
        }
    }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("新建同步任务") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("任务名称") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                Row(verticalAlignment = Alignment.CenterVertically) {
                    OutlinedTextField(
                        value = srcName,
                        onValueChange = {},
                        readOnly = true,
                        label = { Text("手机目录") },
                        placeholder = { Text("点击右侧选择") },
                        modifier = Modifier.weight(1f),
                        singleLine = true,
                    )
                    IconButton(onClick = { pickDir.launch(null) }) {
                        Icon(Icons.Filled.Add, contentDescription = "选择目录")
                    }
                }
                Row(verticalAlignment = Alignment.CenterVertically) {
                    OutlinedTextField(
                        value = target,
                        onValueChange = {},
                        readOnly = true,
                        label = { Text("服务器目录") },
                        placeholder = { Text("点击右侧选择") },
                        modifier = Modifier.weight(1f),
                        singleLine = true,
                    )
                    IconButton(onClick = onPickServerDir) {
                        Icon(Icons.Filled.Add, contentDescription = "选择目录")
                    }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = {
                if (srcUri.isNotBlank() && target.isNotBlank()) {
                    onConfirm(name, srcUri, srcName, target)
                }
            }) { Text("创建并开始") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
}
