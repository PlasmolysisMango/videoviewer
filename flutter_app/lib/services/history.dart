import 'dart:convert';

import 'package:shared_preferences/shared_preferences.dart';

/// 计入"看过"的播放时长阈值（秒）；低于它且大于 0 为"观看中"。
const kWatchedThresholdSeconds = 300;

/// 本地观影历史条目：进入详情页即记录（浏览过），播放页按进度更新
/// 观看秒数。状态由 watchSeconds 派生：>= 阈值为"看过"，>0 为"观看中"。
class HistoryEntry {
  final String id;
  final String number;
  final String title;
  final String cover;
  final int viewedAt; // epoch ms，排序用
  final int watchSeconds; // 累计播放秒数

  HistoryEntry({
    required this.id,
    required this.number,
    required this.title,
    required this.cover,
    required this.viewedAt,
    this.watchSeconds = 0,
  });

  /// viewed / watching / watched
  String get state {
    if (watchSeconds >= kWatchedThresholdSeconds) return 'watched';
    if (watchSeconds > 0) return 'watching';
    return 'viewed';
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'number': number,
        'title': title,
        'cover': cover,
        'viewedAt': viewedAt,
        'watchSeconds': watchSeconds,
      };

  static HistoryEntry fromJson(Map<String, dynamic> j) => HistoryEntry(
        id: (j['id'] as String?) ?? '',
        number: (j['number'] as String?) ?? '',
        title: (j['title'] as String?) ?? '',
        cover: (j['cover'] as String?) ?? '',
        viewedAt: (j['viewedAt'] as num?)?.toInt() ?? 0,
        watchSeconds: (j['watchSeconds'] as num?)?.toInt() ?? 0,
      );
}

/// 观影历史（shared_preferences 持久化，最近 200 条）。
class HistoryService {
  static const _key = 'movie_history';
  static const _max = 200;

  /// 全部历史，按最近浏览倒序。
  static Future<List<HistoryEntry>> list() async {
    final sp = await SharedPreferences.getInstance();
    final raw = sp.getString(_key);
    if (raw == null || raw.isEmpty) return const [];
    try {
      final arr = (jsonDecode(raw) as List).cast<Map<String, dynamic>>();
      final entries = arr.map(HistoryEntry.fromJson).toList()
        ..sort((a, b) => b.viewedAt.compareTo(a.viewedAt));
      return entries;
    } catch (_) {
      return const [];
    }
  }

  /// 进入详情页调用：新增或刷新 viewedAt，保留已有观看进度。
  static Future<void> recordView({
    required String id,
    required String number,
    required String title,
    required String cover,
  }) => _upsert(HistoryEntry(
        id: id,
        number: number,
        title: title,
        cover: cover,
        viewedAt: DateTime.now().millisecondsSinceEpoch,
      ));

  /// 播放页退出/换源时调用：seconds 为本次会话累计播放秒数。
  static Future<void> recordProgress({
    required String id,
    required String number,
    required String title,
    required String cover,
    required int seconds,
  }) =>
      _upsert(HistoryEntry(
        id: id,
        number: number,
        title: title,
        cover: cover,
        viewedAt: DateTime.now().millisecondsSinceEpoch,
        watchSeconds: seconds,
      ));

  static Future<void> clear() async {
    final sp = await SharedPreferences.getInstance();
    await sp.remove(_key);
  }

  /// 同一影片只保留一条：viewedAt 取最新、watchSeconds 取较大值。
  static Future<void> _upsert(HistoryEntry entry) async {
    final sp = await SharedPreferences.getInstance();
    // list() 空时返回 const []（不可变），这里必须拷贝成可变列表再增删。
    final entries = List<HistoryEntry>.from(await list());
    final prevSeconds = entries
        .firstWhere((e) => e.id == entry.id, orElse: () => entry)
        .watchSeconds;
    entries.removeWhere((e) => e.id == entry.id);
    final merged = HistoryEntry(
      id: entry.id,
      number: entry.number,
      title: entry.title,
      cover: entry.cover,
      viewedAt: entry.viewedAt,
      watchSeconds: entry.watchSeconds > prevSeconds
          ? entry.watchSeconds
          : prevSeconds,
    );
    entries.insert(0, merged);
    if (entries.length > _max) {
      entries.removeRange(_max, entries.length);
    }
    await sp.setString(
        _key, jsonEncode(entries.map((e) => e.toJson()).toList()));
  }
}
