package com.example.videoviewer

import android.Manifest
import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.provider.DocumentsContract
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
        private const val PICK_DIR_REQ = 0x5644 // 文件管理器选择自定义下载目录
    }

    // SAF 目录选择器是异步的：结果返回前挂起 Flutter 回调。
    private var pendingPickDirResult: MethodChannel.Result? = null

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
                "pickDownloadDir" -> {
                    pickDownloadDir(result)
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
     * 返回可选下载位置：内部存储（默认）/ 应用私有目录（兜底，无需权限）。
     * 自定义位置不在此枚举：由用户经系统文件管理器选择（见 pickDownloadDir）。
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

    /** SAF 目录选择结果回调（pickDownloadDir 挂起等待此处返回）。 */
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode != PICK_DIR_REQ) return
        val result = pendingPickDirResult ?: return
        pendingPickDirResult = null
        val treeUri = data?.data
        if (resultCode != Activity.RESULT_OK || treeUri == null) {
            result.success(mapOf("status" to "canceled"))
            return
        }
        val path = resolveTreePath(treeUri)
        if (path == null) {
            result.success(mapOf("status" to "unsupported"))
            return
        }
        result.success(mapOf("status" to "ok", "path" to path))
    }

    /**
     * 唤起系统文件管理器（SAF）选择自定义下载目录。
     *
     * SAF 只负责「选目录」，下载写盘仍是路径直写文件系统，因此需要「所有文件
     * 访问」权限；未授权时返回 need_permission，由前端先引导授权再重试。
     */
    private fun pickDownloadDir(result: MethodChannel.Result) {
        if (!storagePermissionGranted()) {
            result.success(mapOf("status" to "need_permission"))
            return
        }
        if (pendingPickDirResult != null) {
            // 已有选择器在途（重复触发）：忽略本次
            result.success(mapOf("status" to "canceled"))
            return
        }
        try {
            val intent = Intent(Intent.ACTION_OPEN_DOCUMENT_TREE)
                // 部分 ROM 需显式开启「显示高级设备」才可见部分目录
                .putExtra("android.content.extra.SHOW_ADVANCED", true)
            pendingPickDirResult = result
            startActivityForResult(intent, PICK_DIR_REQ)
        } catch (e: ActivityNotFoundException) {
            Log.e(TAG, "No document tree picker available", e)
            result.success(mapOf("status" to "unsupported"))
        }
    }

    /**
     * 把 SAF 目录 tree URI 映射为本地文件系统路径。
     *
     * 仅支持系统外部存储 provider：documentId 为 "primary:xxx"（内置存储）或
     * "XXXX-XXXX:xxx"（SD 卡等可移动卷），映射为 /storage/emulated/0/xxx 与
     * /storage/<卷>/xxx。云盘等其它 provider 与根目录（无子路径）无法用于
     * 路径直写，返回 null。
     */
    private fun resolveTreePath(treeUri: Uri): String? {
        if (treeUri.authority != "com.android.externalstorage.documents") {
            Log.w(TAG, "Unsupported tree provider: ${treeUri.authority}")
            return null
        }
        val docId = try {
            DocumentsContract.getTreeDocumentId(treeUri)
        } catch (e: Exception) {
            Log.w(TAG, "Bad tree uri: $treeUri", e)
            return null
        }
        val parts = docId.split(":", limit = 2)
        if (parts.size != 2) return null
        val relative = parts[1].trim('/')
        if (relative.isEmpty()) return null // 根目录：下载产物需落在子目录
        val root = if (parts[0].equals("primary", ignoreCase = true)) {
            Environment.getExternalStorageDirectory().absolutePath
        } else {
            "/storage/${parts[0]}"
        }
        return "$root/$relative"
    }
}
