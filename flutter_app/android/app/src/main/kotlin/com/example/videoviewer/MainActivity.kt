package com.example.videoviewer

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
    }
    
    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        
        // Start Go server immediately
        startGoServer(DEFAULT_ADDR)
        
        // Setup method channel for Flutter communication
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, CHANNEL).setMethodCallHandler { call, result ->
            when (call.method) {
                "startServer" -> {
                    val addr = call.argument<String>("addr") ?: DEFAULT_ADDR
                    val apiBase = call.argument<String>("apiBase") ?: ""
                    val token = call.argument<String>("token") ?: ""
                    val cookie = call.argument<String>("cookie") ?: ""
                    val downloadDir = call.argument<String>("downloadDir") ?: ""
                    
                    val error = startGoServer(addr, apiBase, token, cookie, downloadDir)
                    if (error.isNullOrEmpty()) {
                        result.success(null)
                    } else {
                        result.error("START_FAILED", error, null)
                    }
                }
                "stopServer" -> {
                    val error = stopGoServer()
                    if (error.isNullOrEmpty()) {
                        result.success(null)
                    } else {
                        result.error("STOP_FAILED", error, null)
                    }
                }
                "isServerRunning" -> {
                    result.success(isServerRunning())
                }
                "getServerAddr" -> {
                    result.success(getServerAddr())
                }
                else -> {
                    result.notImplemented()
                }
            }
        }
        
        Log.i(TAG, "Flutter engine configured, Go server started")
    }
    
    override fun onDestroy() {
        super.onDestroy()
        // Stop Go server when activity is destroyed
        stopGoServer()
        Log.i(TAG, "Activity destroyed, Go server stopped")
    }
    
    /**
     * Starts the Go HTTP server.
     * This calls into the gomobile-generated Go library.
     * 
     * @return null on success, error message on failure
     */
    private fun startGoServer(
        addr: String = DEFAULT_ADDR,
        apiBase: String = "",
        token: String = "",
        cookie: String = "",
        downloadDir: String = ""
    ): String? {
        return try {
            // Call gomobile-generated Go function
            // The actual import will be: import goserver.Goserver
            // For now, we'll use reflection to avoid compile errors before gomobile bind
            val goserverClass = Class.forName("goserver.Goserver")
            val startMethod = goserverClass.getMethod(
                "startServer",
                String::class.java,
                String::class.java,
                String::class.java,
                String::class.java,
                String::class.java
            )
            val result = startMethod.invoke(null, addr, apiBase, token, cookie, downloadDir) as? String
            if (result.isNullOrEmpty()) {
                Log.i(TAG, "Go server started on $addr")
                null
            } else {
                Log.e(TAG, "Failed to start Go server: $result")
                result
            }
        } catch (e: ClassNotFoundException) {
            // gomobile library not loaded yet - this is expected during development
            Log.w(TAG, "Go server library not found. Run 'gomobile bind' first.")
            Log.w(TAG, "For development, start the server manually: javdbserver -addr $addr")
            null // Don't fail - allow development without gomobile
        } catch (e: Exception) {
            Log.e(TAG, "Error starting Go server", e)
            e.message
        }
    }
    
    /**
     * Stops the Go HTTP server.
     * 
     * @return null on success, error message on failure
     */
    private fun stopGoServer(): String? {
        return try {
            val goserverClass = Class.forName("goserver.Goserver")
            val stopMethod = goserverClass.getMethod("stopServer")
            val result = stopMethod.invoke(null) as? String
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
    
    /**
     * Checks if the Go server is running.
     */
    private fun isServerRunning(): Boolean {
        return try {
            val goserverClass = Class.forName("goserver.Goserver")
            val method = goserverClass.getMethod("isServerRunning")
            method.invoke(null) as? Boolean ?: false
        } catch (e: Exception) {
            false
        }
    }
    
    /**
     * Gets the server address.
     */
    private fun getServerAddr(): String {
        return try {
            val goserverClass = Class.forName("goserver.Goserver")
            val method = goserverClass.getMethod("getServerAddr")
            method.invoke(null) as? String ?: ""
        } catch (e: Exception) {
            ""
        }
    }
}
