import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../api/client.dart';
import '../services/data_saver.dart';
import '../services/logger.dart';
import 'video_player_screen.dart' show PlayerDefaults;

/// 设置页（侧边栏入口）：播放行为、网页版 Cookie、关于。
class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  bool _autoWatched = false;
  int _maxHeight = 0; // 默认清晰度上限（px），0 = 不限
  bool _limitOnMobile = true; // 移动网络下限制加载（默认开）
  int _speedMbps = 2; // 下载速率上限（MB/s），0 = 不限制
  int _bufferSeconds = 20; // 预读上限（秒），0 = 不限制

  static const _autoWatchedKey = 'auto_mark_watched';
  static const _maxHeightKey = 'default_max_height';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    await DataSaver.load();
    final prefs = await SharedPreferences.getInstance();
    if (!mounted) return;
    setState(() {
      _autoWatched = prefs.getBool(_autoWatchedKey) ?? false;
      _maxHeight = prefs.getInt(_maxHeightKey) ?? 0;
      _limitOnMobile = DataSaver.limitOnMobile;
      _speedMbps = DataSaver.speedLimitMbps;
      _bufferSeconds = DataSaver.bufferSeconds;
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

  Future<void> _toggleLimitOnMobile(bool value) async {
    await DataSaver.setEnabled(value);
    if (!mounted) return;
    setState(() {
      _limitOnMobile = value;
    });
    AppLogger.info('Limit loading on mobile network: $value');
  }

  /// 默认清晰度选项：不限保持现有行为；设置了上限则播放器
  /// 在不超过上限的片源里取最高的（全部超限时取最低档）。
  static const _maxHeightOptions = <(int, String)>[
    (0, '不限（片源默认排序）'),
    (2160, '4K (2160p)'),
    (1080, '1080p'),
    (720, '720p'),
    (480, '480p'),
    (360, '360p'),
  ];

  Future<void> _pickMaxHeight() async {
    final picked = await _pickOption(_maxHeightOptions, _maxHeight);
    if (picked == null || picked == _maxHeight) return;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setInt(_maxHeightKey, picked);
    // 同步内存值：下次进播放器立即生效，无需重启 app。
    PlayerDefaults.maxHeight = picked;
    if (!mounted) return;
    setState(() => _maxHeight = picked);
    AppLogger.info('Default max height: $picked');
  }

  /// 移动网络档位选项：「不限制」分别表示仅限预读 / 保持播放器默认。
  static const _speedOptions = <(int, String)>[
    (1, '1 MB/s'),
    (2, '2 MB/s'),
    (3, '3 MB/s'),
    (5, '5 MB/s'),
    (0, '不限制'),
  ];
  static const _bufferOptions = <(int, String)>[
    (10, '10 秒'),
    (20, '20 秒'),
    (30, '30 秒'),
    (60, '60 秒'),
    (0, '不限制'),
  ];

  Future<void> _pickSpeedLimit() async {
    final picked = await _pickOption(_speedOptions, _speedMbps);
    if (picked == null || picked == _speedMbps) return;
    await DataSaver.setSpeedLimitMbps(picked);
    if (!mounted) return;
    setState(() => _speedMbps = picked);
    AppLogger.info('Mobile speed limit: $picked');
  }

  Future<void> _pickBufferLimit() async {
    final picked = await _pickOption(_bufferOptions, _bufferSeconds);
    if (picked == null || picked == _bufferSeconds) return;
    await DataSaver.setBufferSeconds(picked);
    if (!mounted) return;
    setState(() => _bufferSeconds = picked);
    AppLogger.info('Mobile read ahead limit: $picked');
  }

  /// 通用档位选择：底部弹窗勾选当前值，返回选中的档位（未选择返回 null）。
  Future<int?> _pickOption(List<(int, String)> options, int current) {
    return showModalBottomSheet<int>(
      context: context,
      showDragHandle: true,
      builder: (sheetContext) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (final (v, label) in options)
              ListTile(
                dense: true,
                title: Text(label),
                trailing: v == current ? const Icon(Icons.check) : null,
                onTap: () => Navigator.pop(sheetContext, v),
              ),
          ],
        ),
      ),
    );
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
          ListTile(
            leading: const Icon(Icons.hd_outlined),
            title: const Text('默认清晰度'),
            subtitle: Text(_maxHeight == 0
                ? '不限（使用片源默认排序）'
                : '最高 $_maxHeight p'),
            trailing: const Icon(Icons.arrow_drop_down),
            onTap: _pickMaxHeight,
          ),
          SwitchListTile(
            secondary: const Icon(Icons.data_saver_on),
            title: const Text('移动网络下限制加载'),
            subtitle: const Text('使用流量播放时限制预读与下载速率，避免跑满流量'),
            value: _limitOnMobile,
            onChanged: _toggleLimitOnMobile,
          ),
          if (_limitOnMobile) ...[
            ListTile(
              leading: const Icon(Icons.speed),
              title: const Text('下载速度上限'),
              subtitle: Text(_speedMbps == 0 ? '不限制' : '$_speedMbps MB/s'),
              trailing: const Icon(Icons.arrow_drop_down),
              onTap: _pickSpeedLimit,
            ),
            ListTile(
              leading: const Icon(Icons.schedule),
              title: const Text('预读上限'),
              subtitle: Text(_bufferSeconds == 0 ? '不限制' : '$_bufferSeconds 秒'),
              trailing: const Icon(Icons.arrow_drop_down),
              onTap: _pickBufferLimit,
            ),
          ],
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
