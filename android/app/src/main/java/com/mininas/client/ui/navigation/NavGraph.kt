package com.mininas.client.ui.navigation

import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.mininas.client.data.api.FileInfo
import com.mininas.client.ui.files.FilesScreen
import com.mininas.client.ui.login.LoginScreen
import com.mininas.client.ui.preview.PreviewScreen
import com.mininas.client.ui.settings.SettingsScreen
import com.mininas.client.ui.storage.StorageScreen
import com.mininas.client.ui.sync.SyncScreen

@Composable
fun MiniNasNavGraph(
    navController: NavHostController = rememberNavController(),
    startDestination: String,
) {
    val backStack by navController.currentBackStackEntryAsState()
    val currentRoute = backStack?.destination?.route
    val showBottomBar = currentRoute in TopRoute.entries.map { it.name.lowercase() }

    Scaffold(
        bottomBar = {
            if (showBottomBar) {
                NavigationBar {
                    TopRoute.entries.forEach { route ->
                        val selected = currentRoute == route.name.lowercase()
                        NavigationBarItem(
                            selected = selected,
                            onClick = {
                                navController.navigate(route.name.lowercase()) {
                                    popUpTo(navController.graph.startDestinationId) { saveState = true }
                                    launchSingleTop = true
                                    restoreState = true
                                }
                            },
                            icon = { Icon(route.icon, contentDescription = route.label) },
                            label = { Text(route.label) },
                        )
                    }
                }
            }
        },
    ) { padding ->
        NavHost(
            navController = navController,
            startDestination = startDestination,
            modifier = Modifier.padding(padding),
        ) {
            composable(Routes.LOGIN) {
                LoginScreen(onLoggedIn = {
                    navController.navigate(Routes.FILES) {
                        popUpTo(Routes.LOGIN) { inclusive = true }
                    }
                })
            }
            composable(Routes.FILES) {
                FilesScreen(
                    onOpenPreview = { file ->
                        navController.navigate(Routes.preview(file.path))
                    },
                    onOpenStorage = { navController.navigate(Routes.STORAGE) },
                )
            }
            composable(Routes.SYNC) {
                SyncScreen()
            }
            composable(Routes.SETTINGS) {
                SettingsScreen(
                    onLogout = {
                        navController.navigate(Routes.LOGIN) {
                            popUpTo(0) { inclusive = true }
                        }
                    },
                    onOpenStorage = { navController.navigate(Routes.STORAGE) },
                )
            }
            composable(Routes.STORAGE) {
                StorageScreen(onBack = { navController.popBackStack() })
            }
            composable(
                route = Routes.PREVIEW,
                arguments = listOf(navArgument("path") { type = NavType.StringType }),
            ) { entry ->
                val path = entry.arguments?.getString("path").orEmpty()
                val decoded = android.net.Uri.decode(path)
                val file = FileInfo(name = decoded.substringAfterLast('/'), path = decoded)
                PreviewScreen(file = file, onBack = { navController.popBackStack() })
            }
        }
    }
}
