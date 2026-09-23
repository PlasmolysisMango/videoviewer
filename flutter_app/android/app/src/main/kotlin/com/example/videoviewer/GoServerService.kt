package com.example.videoviewer

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.IBinder
import android.util.Log
import org.json.JSONArray
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/**
 * 前台服务承载内嵌 Go 后端。
 *
 * 之前后端生命周期绑定在 Activity 上（onDestroy 里 stopServer），切后台后一旦
 * 系统销毁 Activity 而保留进程（"不保留活动"、厂商 ROM 回收、内存压力），后端
 * 就被误杀且无人拉起，表现为"切后台回来后访问被拒绝"。改为：
 *  - 服务持有前台通知 → 进程不会被 Cached Apps Freezer 冻结，被低内存查杀的
 *    概率大幅降低；
 *  - START_STICKY → 进程若仍被杀，系统会自动重建服务并走默认参数恢复后端
 *    （登录态从应用私有目录 session.json 恢复）；
 *  - 后端生命周期完全归服务（Service.onDestroy），Activity 的创建/销毁不再
 *    影响后端。
 */
class GoServerService : Service() {
    companion object {
        private const val TAG = "VideoViewer"
        private const val CHANNEL_ID = "backend"
        private const val NOTI_ID = 1
        const val ACTION_START = "com.videoviewer.action.START_SERVER"
        const val ACTION_STOP = "com.videoviewer.action.STOP_SERVER"
        const val EXTRA_ADDR = "addr"
        const val EXTRA_API_BASE = "apiBase"
        const val EXTRA_TOKEN = "token"
        const val EXTRA_COOKIE = "cookie"
        const val EXTRA_DOWNLOAD_DIR = "downloadDir"
        private const val DEFAULT_ADDR = "127.0.0.1:18888"
        private const val POLL_INTERVAL_MS = 2000L // 下载进度轮询间隔
    }

    // 下载进度轮询：有下载任务时把前台通知升级为进度条。
    private var pollThread: Thread? = null

    @Volatile
    private var polling = false

    // 通知去重：文本与进度均无变化时不重复 notify。
    private var lastNotiText: String? = "数据服务运行中"
    private var lastNotiProgress = -1

    override fun onCreate() {
        super.onCreate()
        createChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            GoServerBridge.stop()
            // stopForeground(int) is API 24+; fall back on older devices
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
                stopForeground(STOP_FOREGROUND_REMOVE)
            } else {
                @Suppress("DEPRECATION")
                stopForeground(true)
            }
            stopSelf()
            return START_NOT_STICKY
        }

        // 必须在启动后 5 秒内进入前台状态，先同步展示通知
        startForeground(NOTI_ID, buildNotification())

        val addr = intent?.getStringExtra(EXTRA_ADDR) ?: DEFAULT_ADDR
        val apiBase = intent?.getStringExtra(EXTRA_API_BASE) ?: ""
        val token = intent?.getStringExtra(EXTRA_TOKEN) ?: ""
        val cookie = intent?.getStringExtra(EXTRA_COOKIE) ?: ""
        val downloadDir = intent?.getStringExtra(EXTRA_DOWNLOAD_DIR) ?: ""

        // Go 库的启动包含文件系统初始化，放到子线程避免阻塞主线程
        Thread {
            val error = GoServerBridge.start(
                filesDir = filesDir.absolutePath,
                addr = addr,
                apiBase = apiBase,
                token = token,
                cookie = cookie,
                downloadDir = downloadDir,
            )
            Log.i(TAG, "GoServerService: server start requested on $addr (error=$error)")
        }.start()

        // 轮询下载队列：有任务时前台通知显示进度条与百分比
        startProgressPolling(addr)

        // 系统杀死进程后自动重建服务；intent 为 null 时用默认参数恢复
        // （session.json 中持久化的登录态由 Go 侧 loadSession 自行恢复）
        return START_STICKY
    }

    override fun onDestroy() {
        polling = false
        pollThread?.interrupt()
        pollThread = null
        GoServerBridge.stop()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun createChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "后台数据服务",
                NotificationManager.IMPORTANCE_LOW,
            ).apply {
                description = "保持本地数据服务在后台可用"
                setShowBadge(false)
            }
            (getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager)
                .createNotificationChannel(channel)
        }
    }

    /**
     * 轮询下载队列刷新前台通知（间隔 [POLL_INTERVAL_MS]）。
     *
     * 服务与 Go 后端同进程：直接 HTTP 轮询 127.0.0.1（下载 API 无鉴权）。
     * 有任务时展示进度条与百分比，无任务时恢复「数据服务运行中」。
     */
    private fun startProgressPolling(addr: String) {
        if (polling) return
        polling = true
        pollThread = Thread {
            while (polling) {
                try {
                    updateNotificationProgress(addr)
                } catch (e: Exception) {
                    Log.d(TAG, "Progress poll failed: ${e.message}")
                }
                try {
                    Thread.sleep(POLL_INTERVAL_MS)
                } catch (e: InterruptedException) {
                    break
                }
            }
        }.also {
            it.isDaemon = true
            it.start()
        }
    }

    /** 拉取下载任务并更新通知（文本与进度均无变化时跳过）。 */
    private fun updateNotificationProgress(addr: String) {
        val tasks = fetchDownloadTasks(addr) ?: return
        var running = 0
        var queued = 0
        var doneSum = 0
        var totalSum = 0
        var firstCode = ""
        for (i in 0 until tasks.length()) {
            val t = tasks.optJSONObject(i) ?: continue
            when (t.optString("status")) {
                "running" -> {
                    running++
                    if (firstCode.isEmpty()) firstCode = t.optString("code")
                    doneSum += t.optInt("done_segments")
                    totalSum += t.optInt("total_segments")
                }
                "queued" -> queued++
            }
        }
        var progress = -1
        val suffix = if (queued > 0) " · $queued 个排队" else ""
        val name = firstCode.ifEmpty { "任务" }
        val text = when {
            running > 0 && totalSum > 0 -> {
                progress = (doneSum * 100 / totalSum).coerceIn(0, 100)
                if (running == 1) {
                    "$name 下载中 $progress%$suffix"
                } else {
                    "$running 个任务下载中 $progress%$suffix"
                }
            }
            running > 0 -> if (running == 1) "$name 下载中" else "$running 个任务下载中"
            queued > 0 -> "$queued 个任务排队中"
            else -> "数据服务运行中"
        }
        if (text == lastNotiText && progress == lastNotiProgress) return
        lastNotiText = text
        lastNotiProgress = progress
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        nm.notify(NOTI_ID, buildNotification(text, progress))
    }

    /** GET /api/downloads 返回的 tasks 数组；请求或解析失败返回 null。 */
    private fun fetchDownloadTasks(addr: String): JSONArray? {
        val conn = URL("http://$addr/api/downloads").openConnection() as HttpURLConnection
        return try {
            conn.connectTimeout = 1000
            conn.readTimeout = 2000
            val body = conn.inputStream.bufferedReader().use { it.readText() }
            JSONObject(body).optJSONArray("tasks")
        } finally {
            conn.disconnect()
        }
    }

    /** 前台通知：默认「数据服务运行中」；progress >= 0 时展示进度条。 */
    private fun buildNotification(
        text: String = "数据服务运行中",
        progress: Int = -1,
    ): Notification {
        val builder = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            Notification.Builder(this, CHANNEL_ID)
        } else {
            @Suppress("DEPRECATION")
            Notification.Builder(this)
        }
        builder
            .setContentTitle("VideoViewer")
            .setContentText(text)
            .setSmallIcon(R.mipmap.ic_launcher)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
        if (progress >= 0) {
            builder.setProgress(100, progress, false)
        }
        return builder.build()
    }
}
