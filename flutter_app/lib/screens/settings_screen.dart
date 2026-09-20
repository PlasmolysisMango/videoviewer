import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../api/client.dart';
import '../services/logger.dart';

/// 设置页（侧边栏入口）：播放行为、网页版 Cookie、关于。
class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  bool _autoWatched = false;

  static const _autoWatchedKey = 'auto_mark_watched';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    if (!mounted) return;
    setState(() {
      _autoWatched = prefs.getBool(_autoWatchedKey) ?? false;
    });
  }

  Future<void> _toggleAutoWatched(bool value) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_autoWatchedKey, value);
    if (!mounted) return;
    setState(() {
      _autoWatched = value;
    });
    AppLogger.info('Auto mark watched: $value');
  }

  Future<void> _importWebCookie() async {
    final controller = TextEditingController();
    final ok = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('导入网页版 Cookie'),
        content: TextField(
          controller: controller,
          maxLines: 3,
          decoration: const InputDecoration(
            hintText: '粘贴浏览器里的 JavDB 会话 Cookie（如 _bl_id=...; sid=...）',
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(dialogContext).pop(true),
            child: const Text('导入'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    final cookie = controller.text.trim();
    if (cookie.isEmpty) return;
    try {
      final client = context.read<JavDBClient>();
      await client.setWebCookie(cookie);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Cookie 已导入')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('导入失败：$e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('设置')),
      body: ListView(
        children: [
          const _SectionHeader('播放'),
          SwitchListTile(
            secondary: const Icon(Icons.done_all),
            title: const Text('播放结束后自动加入"看过"'),
            subtitle: const Text('与 JavDB 账号同步标记（需已登录）'),
            value: _autoWatched,
            onChanged: _toggleAutoWatched,
          ),
          const _SectionHeader('网页版'),
          ListTile(
            leading: const Icon(Icons.cookie_outlined),
            title: const Text('导入网页版 Cookie'),
            subtitle: const Text('解锁清单内容、题材筛选等网页版登录墙页面'),
            onTap: _importWebCookie,
          ),
          const _SectionHeader('关于'),
          const ListTile(
            leading: Icon(Icons.info_outline),
            title: Text('VideoViewer'),
            subtitle: Text('JavDB 客户端 · 数据与 JavDB 账号同步'),
          ),
        ],
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader(this.title);

  final String title;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
      child: Text(
        title,
        style: TextStyle(
          fontSize: 13,
          fontWeight: FontWeight.bold,
          color: Theme.of(context).colorScheme.primary,
        ),
      ),
    );
  }
}
