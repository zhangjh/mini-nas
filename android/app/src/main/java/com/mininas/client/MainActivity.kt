package com.mininas.client

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.core.content.ContextCompat
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import com.mininas.client.ui.navigation.MiniNasNavGraph
import com.mininas.client.ui.navigation.Routes
import com.mininas.client.ui.theme.MiniNasTheme
import kotlinx.coroutines.runBlocking

class MainActivity : ComponentActivity() {

    private val notifPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { /* Sync continues without notification permission. */ }

    private val mediaPermissionsLauncher = registerForActivityResult(
        ActivityResultContracts.RequestMultiplePermissions(),
    ) { /* MediaStore sync is used only for granted permissions; others fall back to SAF. */ }

    override fun onCreate(savedInstanceState: Bundle?) {
        installSplashScreen()
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        requestNotificationPermissionIfNeeded()
        requestMediaPermissionsIfNeeded()

        setContent {
            MiniNasTheme {
                val app = MiniNasApp.get()
                val token by app.settings.token.collectAsState(initial = runBlocking { app.settings.getToken() })
                val startDest = remember {
                    if (runBlocking { app.settings.getToken() }.isNullOrBlank()) Routes.LOGIN else Routes.FILES
                }
                MiniNasNavGraph(startDestination = startDest)
            }
        }
    }

    private fun requestNotificationPermissionIfNeeded() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            val granted = ContextCompat.checkSelfPermission(
                this, Manifest.permission.POST_NOTIFICATIONS,
            ) == PackageManager.PERMISSION_GRANTED
            if (!granted) notifPermissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    /**
     * Requests media-read permissions so that sync tasks targeting media
     * folders (DCIM, Pictures, …) can use the fast MediaStore query path
     * instead of walking the SAF tree. If denied, those tasks silently
     * fall back to SAF.
     */
    private fun requestMediaPermissionsIfNeeded() {
        val perms = when {
            Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU -> arrayOf(
                Manifest.permission.READ_MEDIA_IMAGES,
                Manifest.permission.READ_MEDIA_VIDEO,
            )
            Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q -> arrayOf(
                Manifest.permission.READ_EXTERNAL_STORAGE,
            )
            else -> return // API < 29: MediaStore path not used
        }
        val toRequest = perms.filter {
            ContextCompat.checkSelfPermission(this, it) != PackageManager.PERMISSION_GRANTED
        }
        if (toRequest.isNotEmpty()) {
            mediaPermissionsLauncher.launch(toRequest.toTypedArray())
        }
    }
}
