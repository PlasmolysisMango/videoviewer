import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../api/client.dart';
import 'data_cache.dart';
import 'logger.dart';

/// 一条字幕（SRT cue）。
class SubCue {
  final Duration start;
  final Duration end;
  final String text;
  const SubCue(this.start, this.end, this.text);
}

/// 已加载的字幕（含来源信息，用于"字幕已加载"提示）。
class LoadedSubtitle {
  final String code;
  final String source;
  final String lang;
  final String name;
  final List<SubCue> cues;

  const LoadedSubtitle({
    required this.code,
    required this.source,
    required this.lang,
    required this.name,
    required this.cues,
  });

  /// 语言展示名。
  String get langLabel {
    switch (lang.toLowerCase()) {
      case 'zh':
        return '中文';
      case 'zh-cn':
        return '中文(简)';
      case 'zh-tw':
      case 'zh-hk':
        return '中文(繁)';
      case 'en':
        return '英文';
      case 'ja':
      case 'jp':
        return '日文';
      case 'ko':
        return '韩文';
      default:
        return lang.toUpperCase();
    }
  }
}

/// 字幕服务：
///  - auto 加载：DataCache(7 天) → 网络 /api/subtitles/auto（中文优先），
///    进程内再去重；失败结果只在本会话内不重试（重启 app 即重置）。
///  - 播放器设置（开关/字号/时间轴偏移）持久化到 SharedPreferences，
///    任何修改立即保存并通知监听者（播放器实时生效）。
///  - SRT 解析兼容毫秒用 ',' 或 '.' 分隔、行首缩进、CRLF。
class SubtitleService extends ChangeNotifier {
  SubtitleService._();

  static final SubtitleService instance = SubtitleService._();

  JavDBClient? _client;

  /// 绑定全局 API client（main.dart 初始化时调用）。
  static void bind(JavDBClient client) => instance._client = client;

  // —— 播放器设置（自动保存） ——

  static const _kEnabled = 'subs.enabled';
  static const _kFontSize = 'subs.font_size';
  static const _kOffsetMs = 'subs.offset_ms';
  static const _kFontColor = 'subs.font_color';

  bool enabled = true;

  /// 字幕字号（逻辑像素）。
  double fontSize = 18;

  /// 时间轴偏移（毫秒）。正值 = 字幕延后显示，负值 = 提前。
  int offsetMs = 0;

  /// 字幕颜色（ARGB），默认白色。
  int fontColor = 0xFFFFFFFF;

  Future<void> loadSettings() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      enabled = prefs.getBool(_kEnabled) ?? true;
      fontSize = prefs.getDouble(_kFontSize) ?? 18;
      offsetMs = prefs.getInt(_kOffsetMs) ?? 0;
      fontColor = prefs.getInt(_kFontColor) ?? 0xFFFFFFFF;
      notifyListeners();
    } catch (_) {}
  }

  Future<void> _persistSettings() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setBool(_kEnabled, enabled);
      await prefs.setDouble(_kFontSize, fontSize);
      await prefs.setInt(_kOffsetMs, offsetMs);
      await prefs.setInt(_kFontColor, fontColor);
    } catch (_) {}
  }

  void setEnabled(bool v) {
    enabled = v;
    _persistSettings();
    notifyListeners();
  }

  void setFontSize(double v) {
    fontSize = v.clamp(12, 32);
    _persistSettings();
    notifyListeners();
  }

  void setOffsetMs(int v) {
    offsetMs = v.clamp(-10000, 10000);
    _persistSettings();
    notifyListeners();
  }

  void setFontColor(int argb) {
    fontColor = argb;
    _persistSettings();
    notifyListeners();
  }

  void resetOffset() {
    setOffsetMs(0);
  }

  // —— 字幕加载 ——

  /// 会话内结果缓存 + 失败记录时间：失败结果 2 分钟内不重试，
  /// 过后自动解禁——避免偶发失败（如后端被系统冻结瞬间）锁死整个会话。
  final Map<String, LoadedSubtitle?> _mem = {};
  final Map<String, DateTime> _negAt = {};
  final Map<String, Future<LoadedSubtitle?>> _inflight = {};

  /// 预加载 / 获取字幕；确认无字幕或失败时返回 null（字幕是可选增强）。
  Future<LoadedSubtitle?> load(String code) {
    code = code.trim();
    if (code.isEmpty || _client == null) return Future.value(null);
    final f = _inflight.putIfAbsent(code, () => _loadUncached(code));
    // 完成后必须清理：否则 putIfAbsent 永远返回旧结果，
    // 负缓存过期后的重试机制被完全架空（整会话锁死“未找到”）。
    return f.whenComplete(() => _inflight.remove(code));
  }

  Future<LoadedSubtitle?> _loadUncached(String code) async {
    final hit = _mem[code];
    if (_mem.containsKey(code)) {
      if (hit != null) return hit;
      final at = _negAt[code];
      if (at != null &&
          DateTime.now().difference(at) < const Duration(minutes: 2)) {
        return null;
      }
      // 负缓存过期：重新尝试。
      _mem.remove(code);
      _negAt.remove(code);
    }

    // 持久缓存（7 天）：重进页面零请求。
    try {
      final cached = await DataCache.instance
          .read('subs.auto.v2.$code', maxAge: const Duration(days: 7));
      if (cached is Map) {
        final ls = _fromCache(code, cached.cast<String, dynamic>());
        if (ls != null) {
          _mem[code] = ls;
          notifyListeners();
          return ls;
        }
      }
    } catch (_) {}

    try {
      final ls = await _fetchAuto(code);
      if (ls != null) return ls;
      // 确定性无结果（srt/解析为空）：负缓存。
      _mem[code] = null;
      _negAt[code] = DateTime.now();
      return null;
    } catch (_) {
      // 偶发失败（后端被冻结/网络抖动）：延时自动重试一次，
      // 仍失败才负缓存（2 分钟后也可手动搜索兑底）。
      await Future<void>.delayed(const Duration(milliseconds: 1200));
      try {
        final ls = await _fetchAuto(code);
        if (ls != null) return ls;
      } catch (_) {}
      _mem[code] = null;
      _negAt[code] = DateTime.now();
      return null;
    }
  }

  /// 请求 auto 端点并应用结果（缓存/通知）；确定性空返回 null，
  /// 网络异常抛出由调用方决定重试策略。
  Future<LoadedSubtitle?> _fetchAuto(String code) async {
    final r = await _client!.autoSubtitle(code);
    final srt = r['srt'] ?? '';
    if (srt.isEmpty) return null;
    final cues = parseSrt(srt);
    if (cues.isEmpty) return null;
    final ls = LoadedSubtitle(
      code: code,
      source: r['source'] ?? '',
      lang: r['lang'] ?? '',
      name: r['name'] ?? '',
      cues: cues,
    );
    _mem[code] = ls;
    unawaited(DataCache.instance.write('subs.auto.v2.$code', {
      'source': ls.source,
      'lang': ls.lang,
      'name': ls.name,
      'srt': srt,
    }));
    notifyListeners();
    return ls;
  }

  /// 手动选择条目：下载→解析→写缓存，与 auto 结果同一存储路径，
  /// 播放器/详情页通过监听器实时收到新字幕。
  Future<LoadedSubtitle?> applyManual(
      String code, String source, String ref, String lang) async {
    if (_client == null || code.isEmpty) return null;
    try {
      final r = await _client!.downloadSubtitle(
          code: code, source: source, ref: ref, lang: lang);
      final srt = r['srt'] ?? '';
      if (srt.isEmpty) return null;
      final cues = parseSrt(srt);
      if (cues.isEmpty) return null;
      final ls = LoadedSubtitle(
        code: code,
        source: source,
        lang: r['lang'] ?? lang,
        name: r['name'] ?? '',
        cues: cues,
      );
      _mem[code] = ls;
      _negAt.remove(code);
      unawaited(DataCache.instance.write('subs.auto.v2.$code', {
        'source': ls.source,
        'lang': ls.lang,
        'name': ls.name,
        'srt': srt,
      }));
      notifyListeners();
      return ls;
    } catch (e) {
      AppLogger.warning('Manual subtitle apply failed: $e');
      return null;
    }
  }

  LoadedSubtitle? _fromCache(String code, Map<String, dynamic> box) {
    final srt = box['srt'] as String? ?? '';
    if (srt.isEmpty) return null;
    final cues = parseSrt(srt);
    if (cues.isEmpty) return null;
    return LoadedSubtitle(
      code: code,
      source: box['source'] as String? ?? '',
      lang: box['lang'] as String? ?? '',
      name: box['name'] as String? ?? '',
      cues: cues,
    );
  }

  // —— SRT 解析与查找 ——

  static final _tsRe = RegExp(
      r'(\d{1,2}):(\d{2}):(\d{2})[,.](\d{1,3})\s*-->\s*(\d{1,2}):(\d{2}):(\d{2})[,.](\d{1,3})');

  static final _tagRe = RegExp(r'<[^>]+>');

  /// 解析 SRT。subtitlecat 的机器翻译时间戳用 '.' 分隔毫秒、行首带缩进，
  /// 标准字幕用 ','；两者都兼容。HTML 内联标签（<i> 等）剥掉。
  static List<SubCue> parseSrt(String raw) {
    final norm = raw.replaceAll('\r\n', '\n').replaceAll('\r', '\n');
    final cues = <SubCue>[];
    for (final block in norm.split('\n\n')) {
      final m = _tsRe.firstMatch(block);
      if (m == null) continue;
      final start = _duration(m[1]!, m[2]!, m[3]!, m[4]!);
      final end = _duration(m[5]!, m[6]!, m[7]!, m[8]!);
      // 文本取时间戳行之后的所有非空行；剥掉 <i>/<font> 等内联标签。
      final idx = block.indexOf(m[0]!) + m[0]!.length;
      final text = block
          .substring(idx)
          .split('\n')
          .map((l) => l.replaceAll(_tagRe, '').trim())
          .where((l) => l.isNotEmpty)
          .join('\n');
      if (text.isEmpty || end <= start) continue;
      cues.add(SubCue(start, end, text));
    }
    cues.sort((a, b) => a.start.compareTo(b.start));
    return cues;
  }

  static Duration _duration(String h, String m, String s, String ms) {
    // 毫秒段可能是 1-3 位，统一补齐到 3 位。
    final millis = int.parse(ms.padRight(3, '0'));
    return Duration(
      hours: int.parse(h),
      minutes: int.parse(m),
      seconds: int.parse(s),
      milliseconds: millis,
    );
  }

  /// 二分查找 pos 对应的 cue（不在任何 cue 时间段内返回 null）。
  static SubCue? cueAt(List<SubCue> cues, Duration pos) {
    int lo = 0, hi = cues.length - 1, ans = -1;
    while (lo <= hi) {
      final mid = (lo + hi) >> 1;
      if (cues[mid].start <= pos) {
        ans = mid;
        lo = mid + 1;
      } else {
        hi = mid - 1;
      }
    }
    if (ans < 0) return null;
    final c = cues[ans];
    return pos <= c.end ? c : null;
  }
}
