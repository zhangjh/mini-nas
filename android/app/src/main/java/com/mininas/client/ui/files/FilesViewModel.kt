package com.mininas.client.ui.files

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.mininas.client.MiniNasApp
import com.mininas.client.data.api.FileInfo
import com.mininas.client.util.FileTypes
import com.mininas.client.util.FormatUtil
import com.mininas.client.util.PathUtil
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

enum class SortBy { NAME, SIZE, TIME }
enum class ViewMode { LIST, GRID }

data class FilesUiState(
    val currentPath: String = "",
    val items: List<FileInfo> = emptyList(),
    val total: Int = 0,
    val isLoading: Boolean = false,
    val error: String? = null,
    val sortBy: SortBy = SortBy.NAME,
    val sortDesc: Boolean = false,
    val viewMode: ViewMode = ViewMode.LIST,
    val selection: Set<String> = emptySet(),
    val searchQuery: String = "",
    val searchResults: List<FileInfo>? = null,
    val toast: String? = null,
)

class FilesViewModel(
    private val app: MiniNasApp,
) : ViewModel() {

    private val _state = MutableStateFlow(FilesUiState())
    val state: StateFlow<FilesUiState> = _state.asStateFlow()

    init { loadDir("") }

    fun loadDir(path: String) {
        viewModelScope.launch {
            _state.value = _state.value.copy(isLoading = true, error = null, currentPath = path, selection = emptySet(), searchQuery = "", searchResults = null)
            runCatching {
                app.fileRepo.listFiles(
                    path = path.ifEmpty { null },
                    sortBy = _state.value.sortBy.apiName(),
                    sortDesc = _state.value.sortDesc,
                )
            }.onSuccess { list ->
                _state.value = _state.value.copy(
                    items = list.items,
                    total = list.total,
                    isLoading = false,
                    currentPath = list.path.ifEmpty { path },
                )
            }.onFailure { e ->
                _state.value = _state.value.copy(isLoading = false, error = e.message ?: "加载失败")
            }
        }
    }

    fun refresh() = loadDir(_state.value.currentPath)

    fun navigate(path: String) = loadDir(path)

    fun goUp(): Boolean {
        val cur = _state.value.currentPath
        if (cur.isEmpty()) return false
        loadDir(PathUtil.parent(cur))
        return true
    }

    fun toggleSort(by: SortBy) {
        _state.update {
            val newDesc = if (it.sortBy == by) !it.sortDesc else false
            it.copy(sortBy = by, sortDesc = newDesc)
        }
        refresh()
    }

    fun setViewMode(mode: ViewMode) {
        _state.update { it.copy(viewMode = mode) }
    }

    fun toggleSelection(path: String) {
        _state.update {
            val s = it.selection.toMutableSet()
            if (!s.add(path)) s.remove(path)
            it.copy(selection = s)
        }
    }

    fun clearSelection() {
        _state.update { it.copy(selection = emptySet()) }
    }

    fun selectAll() {
        _state.update {
            val all = it.items.map { f -> f.path }.toSet()
            it.copy(selection = if (it.selection.containsAll(all)) emptySet() else all)
        }
    }

    fun search(q: String) {
        if (q.isBlank()) {
            _state.update { it.copy(searchQuery = "", searchResults = null) }
            return
        }
        _state.update { it.copy(searchQuery = q) }
        viewModelScope.launch {
            runCatching { app.fileRepo.search(q, _state.value.currentPath) }
                .onSuccess { res -> _state.update { it.copy(searchResults = res) } }
                .onFailure { _state.update { it.copy(searchResults = emptyList()) } }
        }
    }

    fun clearSearch() {
        _state.update { it.copy(searchQuery = "", searchResults = null) }
    }

    fun mkdir(name: String) {
        val path = PathUtil.join(_state.value.currentPath, name)
        viewModelScope.launch {
            runCatching { app.fileRepo.mkdir(path) }
                .onSuccess { _state.update { it.copy(toast = "已创建") }; refresh() }
                .onFailure { e -> _state.update { it.copy(toast = e.message ?: "创建失败") } }
        }
    }

    fun rename(path: String, newName: String) {
        viewModelScope.launch {
            runCatching { app.fileRepo.rename(path, newName) }
                .onSuccess { _state.update { it.copy(toast = "已重命名") }; refresh() }
                .onFailure { e -> _state.update { it.copy(toast = e.message ?: "重命名失败") } }
        }
    }

    fun delete(path: String) {
        viewModelScope.launch {
            runCatching { app.fileRepo.delete(path) }
                .onSuccess { _state.update { it.copy(toast = "已删除") }; refresh() }
                .onFailure { e -> _state.update { it.copy(toast = e.message ?: "删除失败") } }
        }
    }

    fun deleteSelected() {
        val paths = _state.value.selection.toList()
        if (paths.isEmpty()) return
        viewModelScope.launch {
            var failed = 0
            for (p in paths) {
                runCatching { app.fileRepo.delete(p) }.onFailure { failed++ }
            }
            _state.update { it.copy(selection = emptySet(), toast = if (failed == 0) "已删除" else "$failed 项失败") }
            refresh()
        }
    }

    fun moveSelected(dest: String) {
        val paths = _state.value.selection.toList()
        if (paths.isEmpty()) return
        viewModelScope.launch {
            runCatching { app.fileRepo.move(paths, dest) }
                .onSuccess { _state.update { it.copy(selection = emptySet(), toast = "已移动") }; refresh() }
                .onFailure { e -> _state.update { it.copy(toast = e.message ?: "移动失败") } }
        }
    }

    fun consumeToast() { _state.update { it.copy(toast = null) } }

    private fun SortBy.apiName() = when (this) {
        SortBy.NAME -> "name"
        SortBy.SIZE -> "size"
        SortBy.TIME -> "time"
    }
}
