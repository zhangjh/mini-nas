package com.mininas.client.ui.storage

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.mininas.client.MiniNasApp
import com.mininas.client.data.api.Mount
import com.mininas.client.ui.picker.DirPickerScreen
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class StorageViewModel(
    private val app: MiniNasApp,
) : ViewModel() {

    private val _state = MutableStateFlow(StorageUiState())
    val state: StateFlow<StorageUiState> = _state.asStateFlow()

    init { load() }

    fun load() {
        viewModelScope.launch {
            _state.value = _state.value.copy(isLoading = true, error = null)
            runCatching { app.fileRepo.listMounts() }
                .onSuccess { _state.value = StorageUiState(mounts = it) }
                .onFailure { e -> _state.value = StorageUiState(error = e.message ?: "加载失败") }
        }
    }

    fun create(name: String, path: String) {
        viewModelScope.launch {
            runCatching { app.fileRepo.createMount(name, path) }
                .onSuccess { _state.value = _state.value.copy(toast = "已添加"); load() }
                .onFailure { e -> _state.value = _state.value.copy(toast = e.message ?: "添加失败") }
        }
    }

    fun update(name: String, path: String) {
        viewModelScope.launch {
            runCatching { app.fileRepo.updateMount(name, path) }
                .onSuccess { _state.value = _state.value.copy(toast = "已更新"); load() }
                .onFailure { e -> _state.value = _state.value.copy(toast = e.message ?: "更新失败") }
        }
    }

    fun delete(name: String) {
        viewModelScope.launch {
            runCatching { app.fileRepo.deleteMount(name) }
                .onSuccess { _state.value = _state.value.copy(toast = "已删除"); load() }
                .onFailure { e -> _state.value = _state.value.copy(toast = e.message ?: "删除失败") }
        }
    }

    fun consumeToast() { _state.value = _state.value.copy(toast = null) }
}

data class StorageUiState(
    val mounts: List<Mount> = emptyList(),
    val isLoading: Boolean = false,
    val error: String? = null,
    val toast: String? = null,
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun StorageScreen(
    onBack: () -> Unit,
    viewModel: StorageViewModel = androidx.lifecycle.viewmodel.compose.viewModel(
        factory = object : androidx.lifecycle.ViewModelProvider.Factory {
            @Suppress("UNCHECKED_CAST")
            override fun <T : ViewModel> create(modelClass: Class<T>): T = StorageViewModel(MiniNasApp.get()) as T
        },
    ),
) {
    val state by viewModel.state.collectAsState()
    val snackbar = remember { SnackbarHostState() }
    var showAdd by remember { mutableStateOf(false) }
    var editTarget by remember { mutableStateOf<Mount?>(null) }
    var deleteTarget by remember { mutableStateOf<Mount?>(null) }
    var pickedPath by remember { mutableStateOf("") }
    var showPicker by remember { mutableStateOf(false) }

    LaunchedEffect(state.toast) {
        state.toast?.let { snackbar.showSnackbar(it); viewModel.consumeToast() }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = {
            TopAppBar(
                title = { Text("存储映射") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
            )
        },
        floatingActionButton = {
            FloatingActionButton(onClick = { pickedPath = ""; showAdd = true }, containerColor = MaterialTheme.colorScheme.primary) {
                Icon(Icons.Filled.Add, contentDescription = "添加挂载点")
            }
        },
    ) { padding ->
        if (state.mounts.isEmpty() && !state.isLoading) {
            Column(
                modifier = Modifier.fillMaxSize().padding(padding),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.Center,
            ) {
                Text("暂无挂载点", color = MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(Modifier.height(8.dp))
                Button(onClick = { pickedPath = ""; showAdd = true }) { Text("添加挂载点") }
            }
        } else {
            LazyColumn(
                modifier = Modifier.fillMaxSize().padding(padding),
                contentPadding = androidx.compose.foundation.layout.PaddingValues(12.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                items(state.mounts, key = { it.name }) { m ->
                    MountCard(
                        mount = m,
                        onEdit = { pickedPath = m.path; editTarget = m },
                        onDelete = { deleteTarget = m },
                    )
                }
            }
        }
    }

    if (showAdd && !showPicker) {
        MountDialog(
            title = "添加挂载点",
            initialName = "",
            initialPath = pickedPath,
            nameEditable = true,
            onDismiss = { showAdd = false; pickedPath = "" },
            onConfirm = { n, p -> viewModel.create(n, p); showAdd = false; pickedPath = "" },
            onPickPath = { showPicker = true },
        )
    }
    editTarget?.let { m ->
        if (!showPicker) {
            MountDialog(
                title = "编辑挂载点",
                initialName = m.name,
                initialPath = pickedPath.ifBlank { m.path },
                nameEditable = false,
                onDismiss = { editTarget = null; pickedPath = "" },
                onConfirm = { _, p -> viewModel.update(m.name, p); editTarget = null; pickedPath = "" },
                onPickPath = { showPicker = true },
            )
        }
    }
    deleteTarget?.let { m ->
        AlertDialog(
            onDismissRequest = { deleteTarget = null },
            title = { Text("删除挂载点") },
            text = { Text("确定删除 “${m.name}” 吗？") },
            confirmButton = {
                TextButton(onClick = { viewModel.delete(m.name); deleteTarget = null }) { Text("删除") }
            },
            dismissButton = { TextButton(onClick = { deleteTarget = null }) { Text("取消") } },
        )
    }

    if (showPicker) {
        DirPickerScreen(
            initialPath = pickedPath,
            onConfirm = { picked ->
                pickedPath = picked
                showPicker = false
            },
            onBack = { showPicker = false },
        )
    }
}

@Composable
private fun MountCard(mount: Mount, onEdit: () -> Unit, onDelete: () -> Unit) {
    Card(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(12.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
    ) {
        Row(
            modifier = Modifier.padding(14.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(Icons.Filled.Folder, contentDescription = null, tint = MaterialTheme.colorScheme.primary)
            Spacer(Modifier.height(8.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text(mount.name, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(mount.path, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            IconButton(onClick = onEdit) { Icon(Icons.Filled.Edit, contentDescription = "编辑") }
            IconButton(onClick = onDelete) { Icon(Icons.Filled.Delete, contentDescription = "删除") }
        }
    }
}

@Composable
private fun MountDialog(
    title: String,
    initialName: String,
    initialPath: String,
    nameEditable: Boolean,
    onDismiss: () -> Unit,
    onConfirm: (name: String, path: String) -> Unit,
    onPickPath: () -> Unit,
) {
    var name by remember { mutableStateOf(initialName) }
    var path by remember { mutableStateOf(initialPath) }
    LaunchedEffect(initialPath) { if (initialPath.isNotEmpty()) path = initialPath }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("挂载名称") },
                    singleLine = true,
                    enabled = nameEditable,
                    modifier = Modifier.fillMaxWidth(),
                )
                Row(verticalAlignment = Alignment.CenterVertically) {
                    OutlinedTextField(
                        value = path,
                        onValueChange = {},
                        readOnly = true,
                        label = { Text("服务器路径") },
                        placeholder = { Text("点击右侧选择") },
                        modifier = Modifier.weight(1f),
                        singleLine = true,
                    )
                    IconButton(onClick = onPickPath) {
                        Icon(Icons.Filled.Add, contentDescription = "选择路径")
                    }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = {
                if (name.isNotBlank() && path.isNotBlank()) onConfirm(name.trim(), path.trim())
            }) { Text("保存") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
}
