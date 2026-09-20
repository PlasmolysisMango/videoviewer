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
    }

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

        // 系统杀死进程后自动重建服务；intent 为 null 时用默认参数恢复
        // （session.json 中持久化的登录态由 Go 侧 loadSession 自行恢复）
        return START_STICKY
    }

    override fun onDestroy() {
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

    private fun buildNotification(): Notification {
        val builder = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            Notification.Builder(this, CHANNEL_ID)
        } else {
            @Suppress("DEPRECATION")
            Notification.Builder(this)
        }
        return builder
            .setContentTitle("VideoViewer")
            .setContentText("数据服务运行中")
            .setSmallIcon(R.mipmap.ic_launcher)
            .setOngoing(true)
            .build()
    }
}
