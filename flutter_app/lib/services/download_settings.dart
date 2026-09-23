import 'dart:io' show Platform;

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../api/client.dart';

/// 可选下载位置（Android 由原生侧枚举；桌面为固定下载目录）。
class StorageOption {
  final String label;
  final String path;
  final String kind; // internal / private / custom / desktop

  const StorageOption({
    required this.label,
    required this.path,
    required this.kind,
  });

  factory StorageOption.fromMap(Map<dynamic, dynamic> m) => StorageOption(
        label: m['label'] as String? ?? '',
        path: m['path'] as String? ?? '',
        kind: m['kind'] as String? ?? '',
      );
}

/// 存储位置原生桥（Android）：枚举可选目录、查询/申请系统授权。
///
/// Android 11+ 用「所有文件访问」（MANAGE_EXTERNAL_STORAGE）写公共目录，
/// 10 及以下用 WRITE_EXTERNAL_STORAGE；未授权时只能写应用私有目录。
class StorageService {
  static const _channel = MethodChannel('com.videoviewer/goserver');

  /// 公共存储是否已授权（桌面恒 true；Android 未授权时公共目录不可写）。
  static bool granted = true;

  /// 当前设备的可选位置（Android 从原生枚举，桌面为下载目录）。
  static List<StorageOption> options = [];

  /// 刷新位置列表与授权状态（非 Android 只刷新桌面默认目录）。
  static Future<void> refresh() async {
    if (kIsWeb) {
      granted = false;
      options = [];
      return;
    }
    if (!Platform.isAndroid) {
      final home = Platform.environment['USERPROFILE'] ??
          Platform.environment['HOME'] ??
          '';
      final sep = Platform.pathSeparator;
      options = home.isEmpty
          ? []
          : [
              StorageOption(
                label: '下载目录',
                path: '$home${sep}Downloads${sep}VideoViewer',
                kind: 'desktop',
              ),
            ];
      granted = true;
      return;
    }
    try {
      final res = await _channel.invokeMethod('getStorageOptions');
      if (res is Map) {
        granted = res['granted'] as bool? ?? false;
        final list = res['options'];
        if (list is List) {
          options = list
              .whereType<Map>()
              .map(StorageOption.fromMap)
              .where((o) => o.path.isNotEmpty)
              .toList();
        }
      }
    } catch (_) {}
  }

  /// 申请存储权限（Android 跳系统设置页；其他平台无操作）。
  static Future<void> requestPermission() async {
    if (kIsWeb || !Platform.isAndroid) {
      return;
    }
    try {
      await _channel.invokeMethod('requestStoragePermission');
    } catch (_) {}
  }

  /// 唤起系统文件管理器选择自定义下载目录（Android）。
  ///
  /// 返回 {status, path}：ok / canceled / need_permission / unsupported；
  /// 原生桥异常时返回 null。
  static Future<Map<dynamic, dynamic>?> pickDirectory() async {
    if (kIsWeb || !Platform.isAndroid) return null;
    try {
      final res = await _channel.invokeMethod('pickDownloadDir');
      if (res is Map) return res;
    } catch (_) {}
    return null;
  }

  /// 默认位置：优先内部存储（公共 Movies），其次列表首项。
  static StorageOption? defaultOption() {
    for (final o in options) {
      if (o.kind == 'internal') return o;
    }
    return options.isEmpty ? null : options.first;
  }

  /// 按 kind 查找位置。
  static StorageOption? byKind(String kind) {
    for (final o in options) {
      if (o.kind == kind) return o;
    }
    return null;
  }
}

/// 下载设置（并发数/限速/存储位置）：本地持久化，运行中同步到内嵌后端。
///
/// 与 DataSaver 相同：main 启动时预读；存储位置在启动后端时作为默认下载
/// 目录传入（downloadDir），创建任务时前端还会显式带上当前目录，保证
/// 设置页修改后立即生效。
class DownloadSettings {
  static const _maxConcurrentKey = 'download_max_concurrent';
  static const _speedLimitKey = 'download_speed_limit_mbps';
  static const _storageKindKey = 'download_storage_kind';
  static const _storageCustomPathKey = 'download_storage_custom_path';

  /// 同时下载的任务数上限（默认 1）。
  static int maxConcurrent = 1;

  /// 全局下载限速（MB/s，0 = 不限制；默认 0）。
  static int speedLimitMbps = 0;

  /// 当前存储位置（Android 内部存储/自定义/私有目录；桌面下载目录）。
  static StorageOption? storage;

  /// 用户经文件管理器选择的自定义位置（未设置时为 null）。
  static StorageOption? customStorage;

  static JavDBClient? _client;

  /// 绑定 API client（main 启动后调用），用于把配置同步给后端。
  static void bind(JavDBClient client) {
    _client = client;
  }

  /// 当前生效的下载目录；Web 无内嵌后端时为空。
  static String get currentDir => kIsWeb ? '' : (storage?.path ?? '');

  /// 当前选择需要申请系统权限（Android 公共目录且未授权）。
  static bool get needsPermission {
    if (kIsWeb || !Platform.isAndroid) return false;
    final s = storage;
    if (s == null || s.kind == 'private') return false;
    return !StorageService.granted;
  }

  /// 预读本地设置（main 启动时调用；存储位置需在启动后端前解析完成）。
  static Future<void> load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      maxConcurrent = prefs.getInt(_maxConcurrentKey) ?? 1;
      speedLimitMbps = prefs.getInt(_speedLimitKey) ?? 0;
      final kind = prefs.getString(_storageKindKey) ?? '';
      final customPath = prefs.getString(_storageCustomPathKey) ?? '';
      customStorage = customPath.isEmpty ? null : _customOption(customPath);
      await StorageService.refresh();
      storage = _resolve(kind);
    } catch (_) {}
  }

  /// 重新枚举存储位置（权限授予返回应用后调用）；已保存位置缺失时回退默认。
  static Future<void> refreshStorage() async {
    await StorageService.refresh();
    storage = _resolve(storage?.kind ?? '');
  }

  /// 按 kind 解析存储位置：自定义走已保存路径（丢失时回退默认）。
  static StorageOption? _resolve(String kind) {
    if (kind == 'custom') {
      return customStorage ?? StorageService.defaultOption();
    }
    return StorageService.byKind(kind) ?? StorageService.defaultOption();
  }

  /// 构造自定义位置选项（label 固定，路径即用户选择目录）。
  static StorageOption _customOption(String path) =>
      StorageOption(label: '自定义位置', path: path, kind: 'custom');

  /// 修改同时下载个数。
  static Future<void> setMaxConcurrent(int value) async {
    maxConcurrent = value < 1 ? 1 : value;
    await _persist(_maxConcurrentKey, maxConcurrent);
    await syncToBackend();
  }

  /// 修改全局下载限速（MB/s，0 = 不限制）。
  static Future<void> setSpeedLimitMbps(int value) async {
    speedLimitMbps = value < 0 ? 0 : value;
    await _persist(_speedLimitKey, speedLimitMbps);
    await syncToBackend();
  }

  /// 修改存储位置（写盘；后续创建的任务与下次启动生效）。
  static Future<void> setStorage(StorageOption option) async {
    storage = option;
    if (option.kind == 'custom') {
      customStorage = _customOption(option.path);
      await _persist(_storageCustomPathKey, option.path);
    }
    await _persist(_storageKindKey, option.kind);
  }

  /// 把并发/限速同步给内嵌后端（启动后与设置修改时调用）。
  static Future<void> syncToBackend() async {
    try {
      await _client?.setDownloadConfig(
        maxConcurrent: maxConcurrent,
        speedLimitMbps: speedLimitMbps,
      );
    } catch (_) {}
  }

  static Future<void> _persist(String key, Object value) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      if (value is int) {
        await prefs.setInt(key, value);
      } else if (value is String) {
        await prefs.setString(key, value);
      }
    } catch (_) {}
  }
}
