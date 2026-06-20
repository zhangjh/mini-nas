package com.mininas.client.ui.login

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.mininas.client.MiniNasApp
import com.mininas.client.data.repo.AuthRepository
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

data class LoginUiState(
    val serverUrl: String = "",
    val username: String = "",
    val password: String = "",
    val isLoading: Boolean = false,
    val error: String? = null,
    val hasUsers: Boolean? = null,
    val success: Boolean = false,
)

class LoginViewModel(
    private val app: MiniNasApp,
) : ViewModel() {

    private val _state = MutableStateFlow(LoginUiState())
    val state: StateFlow<LoginUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            val url = app.settings.getServerUrl()
            _state.value = _state.value.copy(serverUrl = url)
            runCatching { app.authRepo.status() }
                .onSuccess { _state.value = _state.value.copy(hasUsers = it.hasUsers) }
                .onFailure { _state.value = _state.value.copy(hasUsers = null) }
        }
    }

    fun updateServerUrl(v: String) { _state.value = _state.value.copy(serverUrl = v) }
    fun updateUsername(v: String) { _state.value = _state.value.copy(username = v, error = null) }
    fun updatePassword(v: String) { _state.value = _state.value.copy(password = v, error = null) }

    fun login() = submit(false)
    fun register() = submit(true)

    private fun submit(register: Boolean) {
        val s = _state.value
        if (s.serverUrl.isBlank()) {
            _state.value = s.copy(error = "请填写服务器地址"); return
        }
        if (s.username.isBlank()) {
            _state.value = s.copy(error = "请填写用户名"); return
        }
        if (s.password.isBlank()) {
            _state.value = s.copy(error = "请填写密码"); return
        }
        viewModelScope.launch {
            _state.value = s.copy(isLoading = true, error = null)
            runCatching {
                app.settings.setServerUrl(s.serverUrl.trim())
                app.apiProvider.updateBaseUrl(s.serverUrl.trim())
                if (register) app.authRepo.register(s.username, s.password)
                else app.authRepo.login(s.username, s.password)
            }.onSuccess {
                _state.value = _state.value.copy(isLoading = false, success = true)
            }.onFailure { e ->
                _state.value = _state.value.copy(
                    isLoading = false,
                    error = e.message ?: "请求失败",
                )
            }
        }
    }
}
