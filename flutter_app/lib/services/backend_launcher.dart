import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:http/http.dart' as http;

/// BackendLauncher manages the Go HTTP server lifecycle.
///
/// On Android: Go server runs in the same process (via gomobile)
/// On Windows: Go server runs as a separate process
class BackendLauncher {
  BackendLauncher._();

  static const String _baseUrl = 'http://127.0.0.1:18888';
  static const MethodChannel _channel =
      MethodChannel('com.videoviewer/goserver');

  static Process? _windowsProcess;
  static bool _isInitialized = false;

  /// Returns the base URL for API calls.
  static String get baseUrl => _baseUrl;

  /// Returns whether the backend has been initialized.
  static bool get isInitialized => _isInitialized;

  /// Launches the Go backend server.
  ///
  /// This must be called before making any API requests.
  /// On Android, the server is started via platform channel (gomobile).
  /// On Windows, the server is started as a separate process.
  /// On Web, the backend must be running separately - this is a no-op.
  static Future<void> launch({
    String addr = '127.0.0.1:18888',
    String apiBase = '',
    String token = '',
    String cookie = '',
    String downloadDir = '',
  }) async {
    if (_isInitialized) {
      debugPrint('BackendLauncher: already initialized');
      return;
    }

    // Web platform: backend must be running separately
    if (kIsWeb) {
      debugPrint('BackendLauncher: web platform detected, skipping backend launch');
      debugPrint('BackendLauncher: ensure Go server is running at $_baseUrl');
      _isInitialized = true;
      return;
    }

    if (Platform.isAndroid) {
      await _launchAndroid(
        addr: addr,
        apiBase: apiBase,
        token: token,
        cookie: cookie,
        downloadDir: downloadDir,
      );
    } else if (Platform.isWindows) {
      await _launchWindows(
        addr: addr,
        apiBase: apiBase,
        token: token,
        cookie: cookie,
        downloadDir: downloadDir,
      );
    } else {
      throw UnsupportedError(
          'Platform not supported: ${Platform.operatingSystem}');
    }

    // Wait for server to be ready
    await _waitForReady();
    _isInitialized = true;
    debugPrint('BackendLauncher: initialized successfully');
  }

  /// Stops the Go backend server.
  static Future<void> stop() async {
    if (!_isInitialized) {
      return;
    }

    // Web platform: no backend to stop
    if (kIsWeb) {
      _isInitialized = false;
      debugPrint('BackendLauncher: stopped (web)');
      return;
    }

    if (Platform.isAndroid) {
      await _stopAndroid();
    } else if (Platform.isWindows) {
      await _stopWindows();
    }

    _isInitialized = false;
    debugPrint('BackendLauncher: stopped');
  }

  // Android: Start via platform channel (gomobile)
  static Future<void> _launchAndroid({
    required String addr,
    required String apiBase,
    required String token,
    required String cookie,
    required String downloadDir,
  }) async {
    try {
      final result = await _channel.invokeMethod<String>('startServer', {
        'addr': addr,
        'apiBase': apiBase,
        'token': token,
        'cookie': cookie,
        'downloadDir': downloadDir,
      });

      if (result != null && result.isNotEmpty) {
        throw Exception('Failed to start Android server: $result');
      }
      debugPrint('BackendLauncher: Android server started via gomobile');
    } on PlatformException catch (e) {
      throw Exception('Platform error: ${e.message}');
    }
  }

  // Android: Stop via platform channel
  static Future<void> _stopAndroid() async {
    try {
      await _channel.invokeMethod('stopServer');
      debugPrint('BackendLauncher: Android server stopped');
    } on PlatformException catch (e) {
      debugPrint(
          'BackendLauncher: Error stopping Android server: ${e.message}');
    }
  }

  // Windows: Start as separate process
  static Future<void> _launchWindows({
    required String addr,
    required String apiBase,
    required String token,
    required String cookie,
    required String downloadDir,
  }) async {
    // Find the server executable
    final exePath = await _findWindowsExecutable();
    if (exePath == null) {
      throw Exception('Could not find javdbserver.exe');
    }

    final args = <String>['-addr', addr];
    if (apiBase.isNotEmpty) {
      args.addAll(['-api-base', apiBase]);
    }
    if (token.isNotEmpty) {
      args.addAll(['-token', token]);
    }
    if (cookie.isNotEmpty) {
      args.addAll(['-cookie', cookie]);
    }
    if (downloadDir.isNotEmpty) {
      args.addAll(['-dl-dir', downloadDir]);
    }

    _windowsProcess = await Process.start(
      exePath,
      args,
      mode: ProcessStartMode.detached,
    );

    debugPrint(
        'BackendLauncher: Windows server started (PID: ${_windowsProcess!.pid})');
  }

  // Windows: Stop the process
  static Future<void> _stopWindows() async {
    if (_windowsProcess != null) {
      _windowsProcess!.kill();
      await _windowsProcess!.exitCode.timeout(
        const Duration(seconds: 5),
        onTimeout: () => -1,
      );
      _windowsProcess = null;
      debugPrint('BackendLauncher: Windows server stopped');
    }
  }

  // Find javdbserver.exe relative to the Flutter executable
  static Future<String?> _findWindowsExecutable() async {
    // Try relative to the executable directory
    final exeDir = File(Platform.resolvedExecutable).parent.path;
    final candidates = [
      '$exeDir\\javdbserver.exe',
      '$exeDir\\..\\javdbserver.exe',
      '$exeDir\\data\\javdbserver.exe',
    ];

    for (final path in candidates) {
      if (await File(path).exists()) {
        return path;
      }
    }

    // Try current directory
    if (await File('javdbserver.exe').exists()) {
      return 'javdbserver.exe';
    }

    return null;
  }

  // Wait for the server to become ready
  static Future<void> _waitForReady({int timeoutMs = 10000}) async {
    final stopwatch = Stopwatch()..start();
    final client = http.Client();

    while (stopwatch.elapsedMilliseconds < timeoutMs) {
      try {
        final response = await client.get(
          Uri.parse('$_baseUrl/health'),
          headers: {'Accept': 'application/json'},
        ).timeout(const Duration(milliseconds: 1000));

        if (response.statusCode == 200) {
          final data = jsonDecode(response.body) as Map<String, dynamic>;
          if (data['status'] == 'ok') {
            debugPrint(
              'BackendLauncher: server ready in ${stopwatch.elapsedMilliseconds}ms',
            );
            return;
          }
        }
      } catch (_) {
        // Server not ready yet, continue waiting
      }

      await Future.delayed(const Duration(milliseconds: 200));
    }

    throw TimeoutException(
      'Server startup timeout after ${timeoutMs}ms',
      Duration(milliseconds: timeoutMs),
    );
  }
}
