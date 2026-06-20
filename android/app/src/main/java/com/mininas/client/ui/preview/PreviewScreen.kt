package com.mininas.client.ui.preview

import android.content.Intent
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.Share
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.media3.common.MediaItem
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.PlayerView
import coil.compose.AsyncImage
import com.mininas.client.MiniNasApp
import com.mininas.client.data.api.FileInfo
import com.mininas.client.ui.files.downloadToExternal
import com.mininas.client.util.FileTypes

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PreviewScreen(
    file: FileInfo,
    onBack: () -> Unit,
    viewModel: PreviewViewModel = viewModel(factory = previewViewModelFactory()),
) {
    val ctx = LocalContext.current
    val state by viewModel.state.collectAsState()
    LaunchedEffect(file.path) { viewModel.load(file) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Text(file.name, maxLines = 1, overflow = TextOverflow.Ellipsis)
                },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
                actions = {
                    IconButton(onClick = {
                        downloadToExternal(ctx, file)
                    }) { Icon(Icons.Filled.Download, contentDescription = "下载") }
                    IconButton(onClick = { viewModel.share(ctx) }) {
                        Icon(Icons.Filled.Share, contentDescription = "分享")
                    }
                },
            )
        },
    ) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .background(Color.Black),
            contentAlignment = Alignment.Center,
        ) {
            when {
                state.isLoading -> Text("加载中…", color = Color.White)
                state.error != null -> Text(state.error!!, color = Color.White)
                FileTypes.isImage(file.name) -> {
                    val app = MiniNasApp.get()
                    AsyncImage(
                        model = app.urlBuilder.previewUrl(file.path),
                        contentDescription = file.name,
                        modifier = Modifier.fillMaxSize(),
                    )
                }
                FileTypes.isVideo(file.name) || FileTypes.isAudio(file.name) -> {
                    VideoPlayer(file)
                }
                FileTypes.isPdf(file.name) || FileTypes.isText(file.name) -> {
                    val bytes = state.bytes
                    if (bytes != null) {
                        if (FileTypes.isText(file.name)) {
                            TextPreview(bytes)
                        } else {
                            PdfPreview(ctx, bytes, file.name)
                        }
                    } else {
                        Text("加载中…", color = Color.White)
                    }
                }
                else -> Text("暂不支持预览此文件", color = Color.White)
            }
        }
    }
}

@Composable
private fun VideoPlayer(file: FileInfo) {
    val ctx = LocalContext.current
    val app = MiniNasApp.get()
    val url = app.urlBuilder.previewUrl(file.path)
    val exo = remember {
        ExoPlayer.Builder(ctx).build().apply {
            setMediaItem(MediaItem.fromUri(url))
            prepare()
            playWhenReady = true
        }
    }
    AndroidView(
        factory = { context ->
            PlayerView(context).apply {
                player = exo
                useController = true
            }
        },
        modifier = Modifier.fillMaxSize(),
    )
    androidx.compose.runtime.DisposableEffect(Unit) {
        onDispose { exo.release() }
    }
}

@Composable
private fun TextPreview(bytes: ByteArray) {
    val text = remember(bytes) {
        runCatching { String(bytes, Charsets.UTF_8) }.getOrElse { String(bytes) }
    }
    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .background(Color.White)
            .padding(16.dp),
    ) {
        Text(text = text, color = Color.Black, style = MaterialTheme.typography.bodyMedium)
    }
}

@Composable
private fun PdfPreview(ctx: android.content.Context, bytes: ByteArray, name: String) {
    // Render via system PDF viewer by writing to cache and launching Intent.
    var launched by remember { mutableStateOf(false) }
    LaunchedEffect(bytes) {
        if (!launched) {
            launched = true
            val outFile = java.io.File(ctx.cacheDir, name)
            outFile.writeBytes(bytes)
            val uri = androidx.core.content.FileProvider.getUriForFile(
                ctx, "${ctx.packageName}.fileprovider", outFile,
            )
            val intent = Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(uri, "application/pdf")
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            }
            runCatching { ctx.startActivity(intent) }
        }
    }
    Text("正在使用系统 PDF 阅读器打开…", color = Color.White)
}
