import 'dart:convert';

import 'package:shared_preferences/shared_preferences.dart';

/// 轻量 JSON 数据缓存：内存 + SharedPreferences 双层。
/// 用于首页推荐池、影片详情等「先展示旧数据、后台再刷新」的场景
/// （stale-while-revalidate），冷启动/重进页面时不必干等网络。
class DataCache {
  DataCache._();

  static final DataCache instance = DataCache._();

  static const _prefix = 'datacache.';

  /// key -> (写入时间, 数据)
  final Map<String, (DateTime, dynamic)> _mem = {};

  /// 读取缓存；超过 maxAge 视为未命中（返回 null，不删除磁盘副本）。
  /// 数据损坏一律静默吞掉并视为未命中。
  Future<dynamic> read(
    String key, {
    Duration maxAge = const Duration(hours: 6),
  }) async {
    final hit = _mem[key];
    if (hit != null) {
      if (DateTime.now().difference(hit.$1) <= maxAge) return hit.$2;
      _mem.remove(key);
    }
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString(_prefix + key);
      if (raw == null) return null;
      final box = jsonDecode(raw) as Map<String, dynamic>;
      final at =
          DateTime.tryParse(box['at'] as String? ?? '') ?? DateTime(1970);
      if (DateTime.now().difference(at) > maxAge) return null;
      final data = box['data'];
      _mem[key] = (at, data);
      return data;
    } catch (_) {
      return null;
    }
  }

  /// 写入缓存（内存 + 磁盘）；磁盘失败只影响下次冷启动，不影响本次。
  Future<void> write(String key, dynamic data) async {
    final at = DateTime.now();
    _mem[key] = (at, data);
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(
          _prefix + key, jsonEncode({'at': at.toIso8601String(), 'data': data}));
    } catch (_) {}
  }
}
