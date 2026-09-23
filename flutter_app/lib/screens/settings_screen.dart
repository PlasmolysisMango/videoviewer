import 'dart:io' show Platform;

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../api/client.dart';
import '../services/data_saver.dart';
import '../services/download_settings.dart';
import '../services/logger.dart';
import 'video_player_screen.dart' show PlayerDefaults;

/// 存储位置弹窗中「自定义位置（文件管理器）」条目的哨兵值。
const _pickCustomDir = 'pick_custom_directory';

/// 设置页（侧边栏入口）：播放行为、网页版 Cookie、关于。
class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen>
    with WidgetsBindingObserver {
  bool _autoWatched = false;
  int _maxHeight = 0; // 默认清晰度上限（px），0 = 不限
  bool _limitOnMobile = true; // 移动网络下限制加载（默认开）
  int _speedMbps = 2; // 缓存速率上限（MB/s），0 = 不限制
  int _bufferSeconds = 20; // 预读上限（秒），0 = 不限制
  // 下载设置：同时下载个数 / 全局限速 / 存储位置。
  int _dlConcurrent = 1;
  int _dlSpeedMbps = 0;
  StorageOption? _dlStorage;
  bool _pickDirAfterGrant = false; // 授权返回后自动打开目录选择器

  static const _autoWatchedKey = 'auto_mark_watched';
  static const _maxHeightKey = 'default_max_height';

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _load();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // 从系统授权页返回：刷新存储授权状态与位置列表。
    if (state == AppLifecycleState.resumed) {
      DownloadSettings.refreshStorage().then((_) {
        if (!mounted) return;
        setState(() => _dlStorage = DownloadSettings.storage);
        // 授权后自动续接：直接打开文件管理器选择自定义目录。
        if (_pickDirAfterGrant) {
          _pickDirAfterGrant = false;
          if (StorageService.granted) _pickCustomDir();
        }
      });
    }
  }

  Future<void> _load() async {
    await DataSaver.load();
    // 下载设置：main 启动时已预读，这里刷新存储位置（SD 卡/授权状态变化）。
    await DownloadSettings.refreshStorage();
    final prefs = await SharedPreferences.getInstance();
    if (!mounted) return;
    setState(() {
      _autoWatched = prefs.getBool(_autoWatchedKey) ?? false;
      _maxHeight = prefs.getInt(_maxHeightKey) ?? 0;
      _limitOnMobile = DataSaver.limitOnMobile;
      _speedMbps = DataSaver.speedLimitMbps;
      _bufferSeconds = DataSaver.bufferSeconds;
      _dlConcurrent = DownloadSettings.maxConcurrent;
      _dlSpeedMbps = DownloadSettings.speedLimitMbps;
      _dlStorage = DownloadSettings.storage;
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

  /// 同时下载个数档位（队列并发上限）。
  static const _dlConcurrentOptions = <(int, String)>[
    (1, '1 个'),
    (2, '2 个'),
    (3, '3 个'),
    (4, '4 个'),
  ];

  Future<void> _pickDownloadConcurrent() async {
    final picked = await _pickOption(_dlConcurrentOptions, _dlConcurrent);
    if (picked == null || picked == _dlConcurrent) return;
    await DownloadSettings.setMaxConcurrent(picked);
    if (!mounted) return;
    setState(() => _dlConcurrent = picked);
    AppLogger.info('Download max concurrent: $picked');
  }

  /// 下载队列全局限速（与移动网络限速独立；档位沿用同一套：1/2/3/5/不限）。
  Future<void> _pickDownloadSpeedLimit() async {
    final picked = await _pickOption(_speedOptions, _dlSpeedMbps);
    if (picked == null || picked == _dlSpeedMbps) return;
    await DownloadSettings.setSpeedLimitMbps(picked);
    if (!mounted) return;
    setState(() => _dlSpeedMbps = picked);
    AppLogger.info('Download speed limit: $picked');
  }

  /// 存储位置选择：Android 走内置/自定义（文件管理器）/私有目录；桌面为下载目录。
  Future<void> _pickStorage() async {
    if (!kIsWeb && Platform.isAndroid) {
      await _pickAndroidStorage();
      return;
    }
    final options = StorageService.options;
    if (options.isEmpty) {
      _toast('当前平台没有可选的存储位置');
      return;
    }
    final picked = await showModalBottomSheet<StorageOption>(
      context: context,
      showDragHandle: true,
      builder: (sheetContext) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (final o in options)
              ListTile(
                dense: true,
                title: Text(o.label),
                subtitle:
                    Text(o.path, maxLines: 1, overflow: TextOverflow.ellipsis),
                trailing:
                    o.kind == _dlStorage?.kind ? const Icon(Icons.check) : null,
                onTap: () => Navigator.pop(sheetContext, o),
              ),
          ],
        ),
      ),
    );
    if (picked == null || picked.kind == _dlStorage?.kind) return;
    await _applyStorage(picked);
  }

  /// Android 存储位置：内置位置（默认）/ 自定义位置（文件管理器）/ 私有目录。
  Future<void> _pickAndroidStorage() async {
    final internalOpt = StorageService.byKind('internal');
    final privateOpt = StorageService.byKind('private');
    final custom = DownloadSettings.customStorage;
    final picked = await showModalBottomSheet<Object>(
      context: context,
      showDragHandle: true,
      builder: (sheetContext) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (internalOpt != null)
              ListTile(
                dense: true,
                title: Text(internalOpt.label),
                subtitle: Text(internalOpt.path,
                    maxLines: 1, overflow: TextOverflow.ellipsis),
                trailing: _dlStorage?.kind == internalOpt.kind
                    ? const Icon(Icons.check)
                    : null,
                onTap: () => Navigator.pop(sheetContext, internalOpt),
              ),
            ListTile(
              dense: true,
              title: const Text('自定义位置（文件管理器）'),
              subtitle: Text(
                custom?.path ?? '通过系统文件管理器选择目录',
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
              trailing:
                  _dlStorage?.kind == 'custom' ? const Icon(Icons.check) : null,
              onTap: () => Navigator.pop(sheetContext, _pickCustomDir),
            ),
            if (privateOpt != null)
              ListTile(
                dense: true,
                title: Text(privateOpt.label),
                subtitle: Text(privateOpt.path,
                    maxLines: 1, overflow: TextOverflow.ellipsis),
                trailing: _dlStorage?.kind == privateOpt.kind
                    ? const Icon(Icons.check)
                    : null,
                onTap: () => Navigator.pop(sheetContext, privateOpt),
              ),
          ],
        ),
      ),
    );
    if (picked == null) return;
    if (picked == _pickCustomDir) {
      await _pickCustomDir();
      return;
    }
    final opt = picked as StorageOption;
    if (opt.kind != _dlStorage?.kind) await _applyStorage(opt);
  }

  /// 唤起系统文件管理器选择自定义目录。
  ///
  /// 下载走路径直写文件系统，需先有「所有文件访问」授权（SAF 仅用于选择
  /// 位置）；未授权时先引导授权，返回后自动续接选择流程。
  Future<void> _pickCustomDir() async {
    if (!StorageService.granted) {
      _showPermissionHint(thenPickDir: true);
      return;
    }
    final res = await StorageService.pickDirectory();
    if (!mounted) return;
    if (res == null) {
      _toast('当前设备不支持文件管理器选择');
      return;
    }
    switch (res['status'] as String? ?? '') {
      case 'ok':
        final path = res['path'] as String? ?? '';
        if (path.isEmpty) return;
        await _applyStorage(
          StorageOption(label: '自定义位置', path: path, kind: 'custom'),
        );
        break;
      case 'need_permission':
        _showPermissionHint(thenPickDir: true);
        break;
      case 'unsupported':
        _toast('该位置不可用：请选择本地存储中的子目录（不支持根目录/云盘）');
        break;
      case 'canceled':
        break;
      default:
        _toast('选择目录失败，请重试');
    }
  }

  /// 应用存储位置（写盘并刷新界面；需要时提示授权）。
  Future<void> _applyStorage(StorageOption option) async {
    await DownloadSettings.setStorage(option);
    if (!mounted) return;
    setState(() => _dlStorage = option);
    AppLogger.info('Download storage: ${option.kind} ${option.path}');
    // 选中的是公共目录且未授权：引导去系统设置开启「所有文件访问」。
    if (DownloadSettings.needsPermission) _showPermissionHint();
  }

  /// 授权引导弹窗；[thenPickDir] 为 true 时授权返回后自动打开目录选择器。
  void _showPermissionHint({bool thenPickDir = false}) {
    showDialog<void>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('需要存储权限'),
        content: const Text('下载到公共目录需要「所有文件访问」权限，去系统设置开启后返回即可生效。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext),
            child: const Text('稍后'),
          ),
          FilledButton(
            onPressed: () {
              Navigator.pop(dialogContext);
              if (thenPickDir) _pickDirAfterGrant = true;
              StorageService.requestPermission();
            },
            child: const Text('去授权'),
          ),
        ],
      ),
    );
  }

  void _toast(String message) {
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  /// 存储位置副标题：未授权时提示（红色）。
  String _storageSubtitle() {
    final s = _dlStorage;
    if (s == null) return '未设置';
    if (DownloadSettings.needsPermission) return '${s.label} · 未授权（点此去设置）';
    return '${s.label} · ${s.path}';
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
            subtitle:
                Text(_maxHeight == 0 ? '不限（使用片源默认排序）' : '最高 $_maxHeight p'),
            trailing: const Icon(Icons.arrow_drop_down),
            onTap: _pickMaxHeight,
          ),
          SwitchListTile(
            secondary: const Icon(Icons.data_saver_on),
            title: const Text('移动网络下限制加载'),
            subtitle: const Text('使用流量播放时限制播放缓存速率，避免跑满流量'),
            value: _limitOnMobile,
            onChanged: _toggleLimitOnMobile,
          ),
          if (_limitOnMobile) ...[
            ListTile(
              leading: const Icon(Icons.speed),
              title: const Text('缓存速度'),
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
          const _SectionHeader('下载'),
          ListTile(
            leading: const Icon(Icons.download_outlined),
            title: const Text('同时下载个数'),
            subtitle: Text('$_dlConcurrent 个'),
            trailing: const Icon(Icons.arrow_drop_down),
            onTap: _pickDownloadConcurrent,
          ),
          ListTile(
            leading: const Icon(Icons.speed),
            title: const Text('下载限速'),
            subtitle: Text(_dlSpeedMbps == 0 ? '不限制' : '$_dlSpeedMbps MB/s'),
            trailing: const Icon(Icons.arrow_drop_down),
            onTap: _pickDownloadSpeedLimit,
          ),
          ListTile(
            leading: const Icon(Icons.folder_outlined),
            title: const Text('存储位置'),
            subtitle: Text(
              _storageSubtitle(),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: DownloadSettings.needsPermission
                  ? TextStyle(color: Theme.of(context).colorScheme.error)
                  : null,
            ),
            trailing: const Icon(Icons.arrow_drop_down),
            onTap: _pickStorage,
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
