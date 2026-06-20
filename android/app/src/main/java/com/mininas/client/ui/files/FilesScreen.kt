package com.mininas.client.ui.files

import android.widget.Toast
import androidx.activity.compose.BackHandler
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
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.GridView
import androidx.compose.material.icons.filled.List
import androidx.compose.material.icons.filled.SdStorage
import androidx.compose.material.icons.filled.Upload
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
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
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.mininas.client.MiniNasApp
import com.mininas.client.data.api.FileInfo
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FilesScreen(
    onOpenPreview: (FileInfo) -> Unit,
    onOpenStorage: () -> Unit,
    viewModel: FilesViewModel = viewModel(factory = filesViewModelFactory()),
) {
    val state by viewModel.state.collectAsState()
    val ctx = LocalContext.current
    val app = MiniNasApp.get()
    val scope = rememberCoroutineScope()
    val snackbar = remember { SnackbarHostState() }
    var showNewFolder by remember { mutableStateOf(false) }
    var renameTarget by remember { mutableStateOf<FileInfo?>(null) }
    var deleteTarget by remember { mutableStateOf<FileInfo?>(null) }
    var uploading by remember { mutableStateOf(false) }
    var uploadProgress by remember { mutableStateOf(0f) }
    var uploadName by remember { mutableStateOf("") }

    val pickFile = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.OpenDocument(),
    ) { uri ->
        if (uri != null) {
            uploading = true
            uploadProgress = 0f
            scope.launch {
                runCatching {
                    withContext(Dispatchers.IO) {
                        val name = uri.lastPathSegment?.substringAfterLast('/') ?: "file"
                        uploadName = name
                        val tmp = File.createTempFile("upload_", "_${name}", ctx.cacheDir)
                        ctx.contentResolver.openInputStream(uri)?.use { input ->
                            tmp.outputStream().use { input.copyTo(it) }
                        }
                        app.fileRepo.uploadFile(
                            path = state.currentPath,
                            file = tmp,
                            mimeType = ctx.contentResolver.getType(uri),
                            onProgress = { p -> uploadProgress = p },
                        )
                        tmp.delete()
                    }
                }.onSuccess {
                    uploading = false
                    snackbar.showSnackbar("上传成功")
                    viewModel.refresh()
                }.onFailure { e ->
                    uploading = false
                    snackbar.showSnackbar("上传失败: ${e.message}")
                }
            }
        }
    }

    fun handleMenu(f: FileInfo, a: FileMenuAction) = when (a) {
        FileMenuAction.RENAME -> { renameTarget = f }
        FileMenuAction.DELETE -> { deleteTarget = f }
        FileMenuAction.DOWNLOAD -> downloadToExternal(ctx, f)
        FileMenuAction.MOVE -> {
            Toast.makeText(ctx, "移动功能开发中", Toast.LENGTH_SHORT).show()
        }
    }

    BackHandler(enabled = state.selection.isNotEmpty() || state.searchResults != null) {
        when {
            state.selection.isNotEmpty() -> viewModel.clearSelection()
            state.searchResults != null -> viewModel.clearSearch()
        }
    }

    LaunchedEffect(state.toast) {
        state.toast?.let {
            snackbar.showSnackbar(it)
            viewModel.consumeToast()
        }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text(
                            if (state.selection.isNotEmpty()) "已选 ${state.selection.size}"
                            else "mini-nas",
                            style = MaterialTheme.typography.titleMedium,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        if (state.selection.isEmpty() && state.currentPath.isNotEmpty()) {
                            Text(
                                state.currentPath,
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                    }
                },
                navigationIcon = {
                    if (state.selection.isNotEmpty()) {
                        IconButton(onClick = { viewModel.clearSelection() }) {
                            Icon(Icons.Filled.Close, contentDescription = "取消选择")
                        }
                    } else if (state.currentPath.isNotEmpty()) {
                        IconButton(onClick = { viewModel.goUp() }) {
                            Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回上级")
                        }
                    }
                },
                actions = {
                    if (state.selection.isNotEmpty()) {
                        IconButton(onClick = { viewModel.deleteSelected() }) {
                            Icon(Icons.Filled.Delete, contentDescription = "删除")
                        }
                        IconButton(onClick = { viewModel.selectAll() }) {
                            Icon(Icons.Filled.Check, contentDescription = "全选")
                        }
                    } else {
                        IconButton(onClick = onOpenStorage) {
                            Icon(Icons.Filled.SdStorage, contentDescription = "存储映射")
                        }
                    }
                },
            )
        },
        floatingActionButton = {
            if (state.selection.isEmpty() && !uploading) {
                FloatingActionButton(onClick = { pickFile.launch(arrayOf("*/*")) }, containerColor = MaterialTheme.colorScheme.primary) {
                    Icon(Icons.Filled.Upload, contentDescription = "上传")
                }
            }
        },
    ) { padding ->
        Column(modifier = Modifier.fillMaxSize().padding(padding)) {
            if (uploading) {
                Column(modifier = Modifier.fillMaxWidth().padding(12.dp)) {
                    Text("正在上传 $uploadName… ${(uploadProgress * 100).toInt()}%", style = MaterialTheme.typography.bodySmall)
                    Spacer(Modifier.size(4.dp))
                    LinearProgressIndicator(progress = { uploadProgress }, modifier = Modifier.fillMaxWidth())
                }
            }
            Row(
                modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                OutlinedTextField(
                    value = state.searchQuery,
                    onValueChange = viewModel::search,
                    modifier = Modifier.weight(1f),
                    placeholder = { Text("搜索当前目录") },
                    singleLine = true,
                    shape = RoundedCornerShape(24.dp),
                )
                Spacer(Modifier.size(8.dp))
                IconButton(onClick = {
                    viewModel.setViewMode(if (state.viewMode == ViewMode.LIST) ViewMode.GRID else ViewMode.LIST)
                }) {
                    Icon(
                        if (state.viewMode == ViewMode.LIST) Icons.Filled.GridView else Icons.Filled.List,
                        contentDescription = "切换视图",
                    )
                }
                IconButton(onClick = { showNewFolder = true }) {
                    Icon(Icons.Filled.Add, contentDescription = "新建文件夹")
                }
            }

            SortBar(
                sortBy = state.sortBy,
                sortDesc = state.sortDesc,
                total = state.total,
                onSort = viewModel::toggleSort,
            )

            HorizontalDivider()

            Box(modifier = Modifier.fillMaxSize()) {
                when {
                    state.isLoading -> {
                        CircularProgressIndicator(modifier = Modifier.size(36.dp).align(Alignment.Center))
                    }
                    state.error != null -> {
                        Column(
                            modifier = Modifier.fillMaxSize().padding(24.dp),
                            horizontalAlignment = Alignment.CenterHorizontally,
                            verticalArrangement = Arrangement.Center,
                        ) {
                            Text(state.error!!, color = MaterialTheme.colorScheme.error)
                            Spacer(Modifier.height(8.dp))
                            TextButton(onClick = { viewModel.refresh() }) { Text("重试") }
                        }
                    }
                    state.searchResults != null -> {
                        val res = state.searchResults!!
                        if (res.isEmpty()) {
                            EmptyState("未找到匹配文件")
                        } else {
                            FileList(
                                items = res,
                                selection = state.selection,
                                onNavigate = { viewModel.navigate(it.path) },
                                onPreview = onOpenPreview,
                                onMenu = { f, a -> handleMenu(f, a) },
                                onToggleSelect = viewModel::toggleSelection,
                            )
                        }
                    }
                    state.items.isEmpty() -> EmptyState("此目录为空")
                    state.viewMode == ViewMode.LIST -> FileList(
                        items = state.items,
                        selection = state.selection,
                        onNavigate = { viewModel.navigate(it.path) },
                        onPreview = onOpenPreview,
                        onMenu = { f, a -> handleMenu(f, a) },
                        onToggleSelect = viewModel::toggleSelection,
                    )
                    else -> FileGrid(
                        items = state.items,
                        selection = state.selection,
                        onNavigate = { viewModel.navigate(it.path) },
                        onPreview = onOpenPreview,
                        onMenu = { f, a -> handleMenu(f, a) },
                        onToggleSelect = viewModel::toggleSelection,
                    )
                }
            }
        }
    }

    if (showNewFolder) {
        var name by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { showNewFolder = false },
            title = { Text("新建文件夹") },
            text = {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("文件夹名称") },
                    singleLine = true,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Ascii),
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    if (name.isNotBlank()) { viewModel.mkdir(name.trim()); showNewFolder = false }
                }) { Text("创建") }
            },
            dismissButton = { TextButton(onClick = { showNewFolder = false }) { Text("取消") } },
        )
    }

    renameTarget?.let { target ->
        var name by remember(target.path) { mutableStateOf(target.name) }
        AlertDialog(
            onDismissRequest = { renameTarget = null },
            title = { Text("重命名") },
            text = {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("新名称") },
                    singleLine = true,
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    if (name.isNotBlank()) { viewModel.rename(target.path, name.trim()); renameTarget = null }
                }) { Text("确定") }
            },
            dismissButton = { TextButton(onClick = { renameTarget = null }) { Text("取消") } },
        )
    }

    deleteTarget?.let { target ->
        AlertDialog(
            onDismissRequest = { deleteTarget = null },
            title = { Text("删除") },
            text = { Text("确定删除 “${target.name}” 吗？") },
            confirmButton = {
                TextButton(onClick = { viewModel.delete(target.path); deleteTarget = null }) { Text("删除") }
            },
            dismissButton = { TextButton(onClick = { deleteTarget = null }) { Text("取消") } },
        )
    }
}

@Composable
private fun EmptyState(msg: String) {
    Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Text(msg, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

