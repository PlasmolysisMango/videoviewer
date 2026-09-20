import 'dart:async';
import 'dart:io' show Platform;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import 'api/client.dart';
import 'providers/auth_provider.dart';
import 'providers/subscription_provider.dart';
import 'providers/theme_provider.dart';
import 'screens/login_screen.dart';
import 'screens/home_screen.dart';
import 'services/backend_launcher.dart';
import 'services/logger.dart';

String? _serverStartError;

/// CJK 字体回退链：Windows 用微软雅黑，macOS/Windows/Linux 各取常见中文字体，
/// 逐个尝试直到命中，不存在的项被忽略。
const _cjkFontFallback = [
  'Microsoft YaHei',
  'PingFang SC',
  'Noto Sans CJK SC',
  'Source Han Sans SC',
  'SimHei',
];

void main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // Global error handlers - catch all uncaught errors
  FlutterError.onError = (FlutterErrorDetails details) {
    AppLogger.error('FlutterError', details.exception, details.stack);
  };

  PlatformDispatcher.instance.onError = (error, stack) {
    AppLogger.error('PlatformDispatcher error', error, stack);
    return true; // prevent crash
  };

  // Zone error handler for async errors
  runZonedGuarded(() async {
    await _runApp();
  }, (error, stack) {
    AppLogger.error('Zone error', error, stack);
  });

  // 主应用退出时同步停止后端 server（Windows 下为独立进程）。
  WidgetsBinding.instance.addObserver(_BackendShutdownObserver());
}

/// 监听应用生命周期：detached（窗口关闭/进程退出）时停止后端。
/// 服务端另有 -parent 看门狗兜底：主进程无论怎样退出，server 都会自杀。
/// Android 例外：detached 可能由系统在后台回收 Activity 触发而进程仍存活，
/// 内嵌后端归前台服务（GoServerService）管，这里主动停会误杀后端。
class _BackendShutdownObserver extends WidgetsBindingObserver {
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.detached) {
      if (!kIsWeb && !Platform.isAndroid) {
        BackendLauncher.stop();
      }
    }
  }
}

Future<void> _runApp() async {
  // Launch the Go backend server
  try {
    AppLogger.info('Starting backend server...');
    await BackendLauncher.launch();
    AppLogger.info('Backend server started successfully');
  } catch (e, stack) {
    AppLogger.error('Failed to start backend server', e, stack);
    _serverStartError = e.toString();
  }

  // Create API client using the backend URL
  final client = JavDBClient(BackendLauncher.baseUrl);

  runApp(MyApp(client: client));
}

class MyApp extends StatelessWidget {
  final JavDBClient client;

  const MyApp({super.key, required this.client});

  @override
  Widget build(BuildContext context) {
    return MultiProvider(
      providers: [
        ChangeNotifierProvider(create: (_) => ThemeProvider()..load()),
        ChangeNotifierProvider(
          create: (context) => AuthProvider(client)..loadSavedCredentials(),
        ),
        ChangeNotifierProvider(
          create: (_) => SubscriptionProvider(client)..load(),
        ),
      ],
      child: Consumer<ThemeProvider>(
        builder: (context, themeProvider, _) => MaterialApp(
          title: 'JavDB',
          debugShowCheckedModeBanner: false,
          // 统一浅色主题（白色背景）；深色模式可由用户手动开启或跟随系统。
          // fontFamilyFallback：Windows 默认 Segoe UI 无中文字形，回退到宋体
          // 等衬线字体显得不自然，显式指定微软雅黑优先。
          theme: ThemeData(
            colorScheme: ColorScheme.fromSeed(seedColor: Colors.blue),
            useMaterial3: true,
            fontFamilyFallback: _cjkFontFallback,
          ),
          darkTheme: ThemeData(
            colorScheme: ColorScheme.fromSeed(
                seedColor: Colors.blue, brightness: Brightness.dark),
            useMaterial3: true,
            fontFamilyFallback: _cjkFontFallback,
          ),
          themeMode: themeProvider.mode,
          initialRoute: '/home',
          routes: {
            '/login': (context) => const LoginScreen(),
            '/home': (context) => const HomeScreen(),
          },
          builder: (context, child) {
            // Show server start error dialog if needed
            if (_serverStartError != null) {
              WidgetsBinding.instance.addPostFrameCallback((_) {
                if (context.mounted) {
                  showDialog(
                    context: context,
                    barrierDismissible: false,
                    builder: (dialogContext) => AlertDialog(
                      title: const Text('后端服务启动失败'),
                      content: SelectableText(
                        _serverStartError!,
                        style: const TextStyle(fontSize: 12),
                      ),
                      actions: [
                        TextButton(
                          onPressed: () => Navigator.of(dialogContext).pop(),
                          child: const Text('继续 (部分功能不可用)'),
                        ),
                      ],
                    ),
                  );
                }
              });
            }
            return child ?? const SizedBox.shrink();
          },
        ),
      ),
    );
  }
}
