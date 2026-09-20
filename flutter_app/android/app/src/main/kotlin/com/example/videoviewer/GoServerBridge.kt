package com.example.videoviewer

import android.util.Log

/**
 * GoServerBridge 封装 gomobile 生成的 Go 库的反射调用，供 Activity 与前台服务共用。
 *
 * gomobile 库（libs 目录下的 AAR）提供 goserver.Goserver 入口；未打包 AAR 的开发环境
 * 里 ClassNotFoundException 被吞掉并返回可容忍的结果，保证 App 可以不带后端启动。
 * 注意：Kotlin 块注释支持嵌套，注释文本里不能出现形如 libs 斜杠星号的通配路径，
 * 否则会被当成新的注释开头导致 Unclosed comment。
 */
object GoServerBridge {
    private const val TAG = "VideoViewer"

    /** Starts the embedded Go HTTP server. Returns null on success, error message otherwise. */
    fun start(
        filesDir: String,
        addr: String = "127.0.0.1:18888",
        apiBase: String = "",
        token: String = "",
        cookie: String = "",
        downloadDir: String = "",
    ): String? {
        return try {
            val goserver = Class.forName("goserver.Goserver")
            // HOME 在 Android 上指向不可写的共享存储（scoped storage），
            // 持久化状态（session/订阅）改存应用私有目录
            goserver.getMethod("setDataDir", String::class.java)
                .invoke(null, filesDir)
            val result = goserver.getMethod(
                "startServer",
                String::class.java,
                String::class.java,
                String::class.java,
                String::class.java,
                String::class.java,
            ).invoke(null, addr, apiBase, token, cookie, downloadDir) as? String
            if (result.isNullOrEmpty()) {
                Log.i(TAG, "Go server started on $addr")
                null
            } else {
                Log.e(TAG, "Failed to start Go server: $result")
                result
            }
        } catch (e: ClassNotFoundException) {
            // gomobile library not loaded yet - expected during development
            Log.w(TAG, "Go server library not found. Run 'gomobile bind' first.")
            null
        } catch (e: Exception) {
            Log.e(TAG, "Error starting Go server", e)
            e.message
        }
    }

    /** Stops the embedded Go HTTP server. Returns null on success, error message otherwise. */
    fun stop(): String? {
        return try {
            val result = Class.forName("goserver.Goserver")
                .getMethod("stopServer").invoke(null) as? String
            if (result.isNullOrEmpty()) {
                Log.i(TAG, "Go server stopped")
                null
            } else {
                Log.e(TAG, "Failed to stop Go server: $result")
                result
            }
        } catch (e: ClassNotFoundException) {
            Log.w(TAG, "Go server library not found")
            null
        } catch (e: Exception) {
            Log.e(TAG, "Error stopping Go server", e)
            e.message
        }
    }

    fun isRunning(): Boolean {
        return try {
            Class.forName("goserver.Goserver")
                .getMethod("isServerRunning").invoke(null) as? Boolean ?: false
        } catch (e: Exception) {
            false
        }
    }

    fun addr(): String {
        return try {
            Class.forName("goserver.Goserver")
                .getMethod("getServerAddr").invoke(null) as? String ?: ""
        } catch (e: Exception) {
            ""
        }
    }
}
