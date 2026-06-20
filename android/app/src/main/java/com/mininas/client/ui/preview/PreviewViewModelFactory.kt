package com.mininas.client.ui.preview

import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import com.mininas.client.MiniNasApp

fun previewViewModelFactory(): ViewModelProvider.Factory =
    object : ViewModelProvider.Factory {
        @Suppress("UNCHECKED_CAST")
        override fun <T : ViewModel> create(modelClass: Class<T>): T {
            return PreviewViewModel(MiniNasApp.get()) as T
        }
    }
