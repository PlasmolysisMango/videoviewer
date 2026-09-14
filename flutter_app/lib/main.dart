import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import 'api/client.dart';
import 'providers/auth_provider.dart';
import 'screens/login_screen.dart';
import 'screens/home_screen.dart';
import 'services/backend_launcher.dart';
import 'services/logger.dart';

String? _serverStartError;

void main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // Launch the Go backend server
  try {
    AppLogger.info('Starting backend server...');
    await BackendLauncher.launch();
    AppLogger.info('Backend server started successfully');
  } catch (e) {
    AppLogger.error('Failed to start backend server', e);
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
    return ChangeNotifierProvider(
      create: (context) => AuthProvider(client)..loadSavedCredentials(),
      child: MaterialApp(
        title: 'JavDB',
        debugShowCheckedModeBanner: false,
        theme: ThemeData(
          colorScheme: ColorScheme.fromSeed(seedColor: Colors.blue),
          useMaterial3: true,
        ),
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
    );
  }
}
