package com.mininas.client.ui.files

import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import com.mininas.client.MiniNasApp

fun filesViewModelFactory(): ViewModelProvider.Factory =
    object : ViewModelProvider.Factory {
        @Suppress("UNCHECKED_CAST")
        override fun <T : ViewModel> create(modelClass: Class<T>): T {
            return FilesViewModel(MiniNasApp.get()) as T
        }
    }
