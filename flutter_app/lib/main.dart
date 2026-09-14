import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import 'api/client.dart';
import 'providers/auth_provider.dart';
import 'screens/login_screen.dart';
import 'screens/home_screen.dart';
import 'services/backend_launcher.dart';

void main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // Launch the Go backend server
  try {
    await BackendLauncher.launch();
    debugPrint('Backend server started successfully');
  } catch (e) {
    debugPrint('Failed to start backend server: $e');
    // Continue anyway - user will see connection errors
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
        initialRoute: '/login',
        routes: {
          '/login': (context) => const LoginScreen(),
          '/home': (context) => const HomeScreen(),
        },
      ),
    );
  }
}
