package com.mininas.client.ui.preview

import android.content.Context
import android.content.Intent
import androidx.core.content.FileProvider
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.mininas.client.MiniNasApp
import com.mininas.client.data.api.FileInfo
import com.mininas.client.util.FileTypes
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File

data class PreviewUiState(
    val isLoading: Boolean = false,
    val bytes: ByteArray? = null,
    val error: String? = null,
)

class PreviewViewModel(
    private val app: MiniNasApp,
) : ViewModel() {

    private val _state = MutableStateFlow(PreviewUiState())
    val state: StateFlow<PreviewUiState> = _state.asStateFlow()

    fun load(file: FileInfo) {
        // Images and videos are streamed directly via URL, no need to fetch bytes.
        if (FileTypes.isImage(file.name) || FileTypes.isVideo(file.name) || FileTypes.isAudio(file.name)) return
        viewModelScope.launch {
            _state.value = PreviewUiState(isLoading = true)
            runCatching {
                withContext(Dispatchers.IO) {
                    val body = app.apiProvider.current.previewFile(file.path)
                    body.bytes()
                }
            }.onSuccess { bytes ->
                _state.value = PreviewUiState(isLoading = false, bytes = bytes)
            }.onFailure { e ->
                _state.value = PreviewUiState(isLoading = false, error = e.message ?: "加载失败")
            }
        }
    }

    fun share(ctx: Context) {
        val bytes = _state.value.bytes ?: return
        viewModelScope.launch {
            val file = File(ctx.cacheDir, "preview_share").apply { writeBytes(bytes) }
            val uri = FileProvider.getUriForFile(ctx, "${ctx.packageName}.fileprovider", file)
            val intent = Intent(Intent.ACTION_SEND).apply {
                type = "application/octet-stream"
                putExtra(Intent.EXTRA_STREAM, uri)
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            }
            runCatching { ctx.startActivity(Intent.createChooser(intent, "分享")) }
        }
    }
}
