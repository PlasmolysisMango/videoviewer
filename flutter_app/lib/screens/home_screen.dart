import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../providers/auth_provider.dart';
import '../services/backend_launcher.dart';
import '../services/logger.dart';
import 'search_screen.dart';
import 'ranking_screen.dart';
import 'log_screen.dart';

class HomeScreen extends StatelessWidget {
  const HomeScreen({super.key});

  void _checkBackendAndNavigate(BuildContext context, Widget screen) {
    if (!BackendLauncher.isInitialized) {
      AppLogger.error('Backend not initialized, cannot navigate');
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('后端服务未启动，部分功能不可用'),
          backgroundColor: Colors.red,
        ),
      );
      return;
    }
    Navigator.push(context, MaterialPageRoute(builder: (context) => screen));
  }

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthProvider>();

    return Scaffold(
      appBar: AppBar(
        title: const Text('JavDB'),
        actions: [
          IconButton(
            icon: const Icon(Icons.bug_report),
            onPressed: () {
              Navigator.push(
                context,
                MaterialPageRoute(builder: (context) => const LogScreen()),
              );
            },
          ),
          if (auth.isLoggedIn)
            IconButton(
              icon: const Icon(Icons.logout),
              onPressed: () async {
                await auth.logout();
                if (context.mounted) {
                  ScaffoldMessenger.of(context).showSnackBar(
                    const SnackBar(content: Text('已退出登录')),
                  );
                }
              },
            )
          else
            TextButton.icon(
              onPressed: () {
                Navigator.of(context).pushNamed('/login');
              },
              icon: const Icon(Icons.login, size: 18),
              label: const Text('登录'),
            ),
        ],
      ),
      body: Center(
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            const Icon(
              Icons.movie_outlined,
              size: 100,
              color: Colors.blue,
            ),
            const SizedBox(height: 16),
            Text(
              auth.isLoggedIn ? '欢迎, ${auth.username ?? "用户"}' : '游客模式',
              style: const TextStyle(fontSize: 24),
            ),
            if (!auth.isLoggedIn)
              const Padding(
                padding: EdgeInsets.only(top: 8),
                child: Text(
                  '登录后可使用完整功能',
                  style: TextStyle(fontSize: 14, color: Colors.grey),
                ),
              ),
            const SizedBox(height: 48),
            ElevatedButton.icon(
              onPressed: () {
                _checkBackendAndNavigate(context, const SearchScreen());
              },
              icon: const Icon(Icons.search),
              label: const Text('搜索'),
              style: ElevatedButton.styleFrom(
                padding:
                    const EdgeInsets.symmetric(horizontal: 32, vertical: 16),
              ),
            ),
            const SizedBox(height: 16),
            ElevatedButton.icon(
              onPressed: () {
                _checkBackendAndNavigate(context, const RankingScreen());
              },
              icon: const Icon(Icons.trending_up),
              label: const Text('榜单'),
              style: ElevatedButton.styleFrom(
                padding:
                    const EdgeInsets.symmetric(horizontal: 32, vertical: 16),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
