package com.example.videoviewer

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.provider.Settings
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
        private const val STORAGE_PERMISSION_REQ = 2
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
                "getStorageOptions" -> {
                    result.success(storageOptions())
                }
                "requestStoragePermission" -> {
                    requestStoragePermission()
                    result.success(null)
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

    /**
     * 下载存储位置：Android 11+ 用「所有文件访问」（MANAGE_EXTERNAL_STORAGE）
     * 直接写公共目录；Android 10 及以下用 WRITE_EXTERNAL_STORAGE 运行时权限。
     * 未授权时返回 granted=false，前端提示用户去系统设置开启。
     */
    private fun storagePermissionGranted(): Boolean =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            Environment.isExternalStorageManager()
        } else {
            checkSelfPermission(Manifest.permission.WRITE_EXTERNAL_STORAGE) ==
                PackageManager.PERMISSION_GRANTED
        }

    /**
     * 返回可选下载位置：内部存储 / SD 卡（如有）/ 应用私有目录（兜底，无需权限）。
     */
    private fun storageOptions(): Map<String, Any> {
        val options = mutableListOf<Map<String, Any>>()
        val externalRoot = Environment.getExternalStorageDirectory().absolutePath
        options.add(
            mapOf(
                "label" to "内部存储",
                "path" to "$externalRoot/Movies/VideoViewer",
                "kind" to "internal",
                "available" to true,
            ),
        )
        sdVolumeRoot()?.let { root ->
            options.add(
                mapOf(
                    "label" to "SD 卡",
                    "path" to "$root/VideoViewer",
                    "kind" to "external",
                    "available" to true,
                ),
            )
        }
        options.add(
            mapOf(
                "label" to "应用私有目录（无需权限）",
                "path" to (getExternalFilesDir(null)?.absolutePath ?: filesDir.absolutePath),
                "kind" to "private",
                "available" to true,
            ),
        )
        return mapOf(
            "granted" to storagePermissionGranted(),
            "options" to options,
        )
    }

    /**
     * 可移动存储（SD 卡）卷根路径：从外部私有目录回溯到 /storage/<卷>。
     * 目录形如 /storage/XXXX-XXXX/Android/data/<pkg>/files。
     */
    private fun sdVolumeRoot(): String? {
        val dirs = getExternalFilesDirs(null)
        for (i in 1 until dirs.size) {
            val path = dirs[i]?.absolutePath ?: continue
            val idx = path.indexOf("/Android/")
            if (idx > 0) return path.substring(0, idx)
        }
        return null
    }

    /**
     * 请求存储权限：Android 11+ 跳系统「所有文件访问」设置页（系统不提供弹窗
     * 直授权），部分 ROM 无 per-app 入口时退到全局列表页；10 及以下为运行时弹窗。
     */
    private fun requestStoragePermission() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            try {
                startActivity(
                    Intent(
                        Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION,
                        Uri.parse("package:$packageName"),
                    ),
                )
            } catch (e: Exception) {
                Log.w(TAG, "No per-app all-files page, fallback to global list", e)
                try {
                    startActivity(Intent(Settings.ACTION_MANAGE_ALL_FILES_ACCESS_PERMISSION))
                } catch (e2: Exception) {
                    Log.e(TAG, "No all-files access settings page", e2)
                }
            }
        } else {
            requestPermissions(
                arrayOf(Manifest.permission.WRITE_EXTERNAL_STORAGE),
                STORAGE_PERMISSION_REQ,
            )
        }
    }
}
