package com.example.videoviewer

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.util.Log
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel

class MainActivity : FlutterActivity() {
    private val CHANNEL = "com.videoviewer/goserver"

    companion object {
        private const val TAG = "VideoViewer"
        private const val DEFAULT_ADDR = "127.0.0.1:18888"
        private const val NOTI_PERMISSION_REQ = 1
    }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)

        requestNotificationPermissionIfNeeded()

        // Start the Go backend via the foreground service (lifecycle is owned by
        // the service so backgrounding the activity no longer kills the backend)
        startServerService(DEFAULT_ADDR)

        // Setup method channel for Flutter communication
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, CHANNEL).setMethodCallHandler { call, result ->
            when (call.method) {
                "startServer" -> {
                    val addr = call.argument<String>("addr") ?: DEFAULT_ADDR
                    val apiBase = call.argument<String>("apiBase") ?: ""
                    val token = call.argument<String>("token") ?: ""
                    val cookie = call.argument<String>("cookie") ?: ""
                    val downloadDir = call.argument<String>("downloadDir") ?: ""

                    val error = startServerService(addr, apiBase, token, cookie, downloadDir)
                    if (error.isNullOrEmpty()) {
                        result.success(null)
                    } else {
                        result.error("START_FAILED", error, null)
                    }
                }
                "stopServer" -> {
                    val error = stopServerService()
                    if (error.isNullOrEmpty()) {
                        result.success(null)
                    } else {
                        result.error("STOP_FAILED", error, null)
                    }
                }
                "isServerRunning" -> {
                    result.success(GoServerBridge.isRunning())
                }
                "getServerAddr" -> {
                    result.success(GoServerBridge.addr())
                }
                else -> {
                    result.notImplemented()
                }
            }
        }

        Log.i(TAG, "Flutter engine configured, Go server service started")
    }

    override fun onDestroy() {
        super.onDestroy()
        // Do NOT stop the Go server here. Activity destruction does not mean the
        // user is leaving (system may destroy the activity in background while
        // keeping the process, e.g. "don't keep activities" or vendor ROM
        // reclamation). The backend lifecycle is owned by GoServerService.
        Log.i(TAG, "Activity destroyed (Go server keeps running in the foreground service)")
    }

    /**
     * Starts the foreground service that hosts the embedded Go HTTP server.
     *
     * @return null on success, error message on failure
     */
    private fun startServerService(
        addr: String = DEFAULT_ADDR,
        apiBase: String = "",
        token: String = "",
        cookie: String = "",
        downloadDir: String = "",
    ): String? {
        return try {
            val intent = Intent(this, GoServerService::class.java)
                .setAction(GoServerService.ACTION_START)
                .putExtra(GoServerService.EXTRA_ADDR, addr)
                .putExtra(GoServerService.EXTRA_API_BASE, apiBase)
                .putExtra(GoServerService.EXTRA_TOKEN, token)
                .putExtra(GoServerService.EXTRA_COOKIE, cookie)
                .putExtra(GoServerService.EXTRA_DOWNLOAD_DIR, downloadDir)
            // startForegroundService requires the service to call startForeground
            // promptly, which GoServerService does in onStartCommand.
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                startForegroundService(intent)
            } else {
                startService(intent)
            }
            null
        } catch (e: Exception) {
            Log.e(TAG, "Error starting Go server service", e)
            e.message
        }
    }

    /**
     * Stops the foreground service (and the Go server hosted inside).
     *
     * @return null on success, error message on failure
     */
    private fun stopServerService(): String? {
        return try {
            val intent = Intent(this, GoServerService::class.java)
                .setAction(GoServerService.ACTION_STOP)
            startService(intent)
            null
        } catch (e: Exception) {
            Log.e(TAG, "Error stopping Go server service", e)
            e.message
        }
    }

    /**
     * Android 13+ requires a runtime permission to show the foreground service
     * notification. Without it the service still runs but the notification is
     * hidden, so a denied permission only degrades visibility.
     */
    private fun requestNotificationPermissionIfNeeded() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) !=
            PackageManager.PERMISSION_GRANTED
        ) {
            requestPermissions(
                arrayOf(Manifest.permission.POST_NOTIFICATIONS),
                NOTI_PERMISSION_REQ,
            )
        }
    }
}
