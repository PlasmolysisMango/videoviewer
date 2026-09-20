import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../services/logger.dart';

/// 标记状态常量（与后端 / JavDB 一致）。
const kMarkWantWatch = 'want_watch';
const kMarkWatched = 'watched';

/// 用户态（想看 / 看过 / 清单）：与 JavDB 登录账号双向同步。
///
/// 数据同时存 SharedPreferences 本地缓存；App 启动或下拉刷新时从后端拉取
/// 最新数据并更新缓存，离线/未登录时展示缓存内容。收藏（♥）即"想看"。
///
/// 收到 401（登录态失效）时通过 [AuthProvider.silentRelogin] 自动重登一次
/// 后重试，全程无感。
class UserStateProvider extends ChangeNotifier {
  UserStateProvider(this._client, this._auth);

  final JavDBClient _client;
  final dynamic _auth; // AuthProvider（避免循环 import，动态调用 silentRelogin）

  List<Map<String, dynamic>> _wantWatch = [];
  List<Map<String, dynamic>> _watched = [];
  List<Map<String, dynamic>> _lists = [];
  bool _marksLoaded = false;

  List<Map<String, dynamic>> get wantWatch => _wantWatch;
  List<Map<String, dynamic>> get watched => _watched;
  List<Map<String, dynamic>> get lists => _lists;
  bool get marksLoaded => _marksLoaded;

  // ---------------------------------------------------------------------------
  // 去除看过的（列表过滤开关：全局 UI 偏好，默认关）
  // ---------------------------------------------------------------------------

  static const _kHideWatched = 'ui.hide_watched';
  bool _hideWatched = false;

  bool get hideWatched => _hideWatched;

  /// 已看过的番号集合（大写化，供列表过滤比对）。
  Set<String> get watchedNumbers => _watched
      .map((m) => ((m['number'] as String?) ?? '').trim().toUpperCase())
      .where((n) => n.isNotEmpty)
      .toSet();

  /// 开关开启时过滤掉已看过的影片（按番号匹配，忽略大小写）。
  List<Movie> filterWatchedMovies(List<Movie> movies) {
    if (!_hideWatched) return movies;
    final watched = watchedNumbers;
    return movies
        .where((m) => !watched.contains(m.number.trim().toUpperCase()))
        .toList();
  }

  Future<void> toggleHideWatched() async {
    _hideWatched = !_hideWatched;
    notifyListeners();
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setBool(_kHideWatched, _hideWatched);
    } catch (_) {}
  }

  Set<String> get _wantWatchIds =>
      _wantWatch.map((m) => (m['id'] as String?) ?? '').toSet();
  Set<String> get _watchedIds =>
      _watched.map((m) => (m['id'] as String?) ?? '').toSet();

  bool isWantWatch(String movieId) => _wantWatchIds.contains(movieId);
  bool isWatched(String movieId) => _watchedIds.contains(movieId);

  // ---------------------------------------------------------------------------
  // 本地缓存
  // ---------------------------------------------------------------------------

  Future<void> _loadCache() async {
    final prefs = await SharedPreferences.getInstance();
    _wantWatch = _decode(prefs.getString('user_marks_want_watch'));
    _watched = _decode(prefs.getString('user_marks_watched'));
    _lists = _decode(prefs.getString('user_lists'));
    _marksLoaded = _wantWatch.isNotEmpty || _watched.isNotEmpty;
    _hideWatched = prefs.getBool(_kHideWatched) ?? false;
    notifyListeners();
  }

  Future<void> _saveCache(String key, List<Map<String, dynamic>> data) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(key, jsonEncode(data));
  }

  List<Map<String, dynamic>> _decode(String? raw) {
    if (raw == null || raw.isEmpty) return [];
    try {
      return (jsonDecode(raw) as List)
          .map((e) => e as Map<String, dynamic>)
          .toList();
    } catch (_) {
      return [];
    }
  }

  // ---------------------------------------------------------------------------
  // 同步（拉取）
  // ---------------------------------------------------------------------------

  /// 初始化：先读缓存立即可用，再后台拉最新。
  Future<void> init() async {
    await _loadCache();
    await refreshAll();
  }

  /// 拉取想看/看过/清单并更新缓存；失败保留缓存内容。
  Future<void> refreshAll() async {
    await Future.wait([
      _refreshMarks(kMarkWantWatch),
      _refreshMarks(kMarkWatched),
      refreshLists(),
    ]);
  }

  Future<void> _refreshMarks(String status, {int depth = 0}) async {
    try {
      final movies = await _client.getUserMarks(status);
      if (status == kMarkWantWatch) {
        _wantWatch = movies;
        await _saveCache('user_marks_want_watch', movies);
      } else {
        _watched = movies;
        await _saveCache('user_marks_watched', movies);
      }
      _marksLoaded = true;
      notifyListeners();
    } catch (e) {
      if (await _retryAfterRelogin(e, depth, () => _refreshMarks(status, depth: depth + 1))) {
        return;
      }
      AppLogger.warning('Failed to refresh marks($status): $e');
    }
  }

  /// 拉取清单。带 [movieId] 时额外刷新该影片的 has_movie 状态视图。
  Future<void> refreshLists({String? movieId, int depth = 0}) async {
    try {
      _lists = await _client.getUserLists(movieId: movieId);
      await _saveCache('user_lists', _lists);
      notifyListeners();
    } catch (e) {
      if (await _retryAfterRelogin(e, depth,
          () => refreshLists(movieId: movieId, depth: depth + 1))) {
        return;
      }
      AppLogger.warning('Failed to refresh lists: $e');
    }
  }

  /// 401 时静默重登一次并重试；非 401 或重试失败返回 false。
  Future<bool> _retryAfterRelogin(
      Object error, int depth, Future<void> Function() retry) async {
    if (depth > 0 || !error.toString().contains('login required')) return false;
    try {
      final ok = await (_auth as dynamic).silentRelogin() as bool;
      if (ok != true) return false;
    } catch (_) {
      return false;
    }
    try {
      await retry();
      return true;
    } catch (_) {
      return false;
    }
  }

  // ---------------------------------------------------------------------------
  // 标记（写操作：先远端，成功后本地更新 + 缓存）
  // ---------------------------------------------------------------------------

  /// 标记想看/看过。已标记同一状态时为取消。
  /// 返回操作后的实际状态（null 表示已取消）。
  Future<String?> toggleMark(
      Map<String, dynamic> movie, String status) async {
    final id = movie['id'] as String?;
    if (id == null || id.isEmpty) return null;
    final marked = status == kMarkWantWatch ? isWantWatch(id) : isWatched(id);
    try {
      if (marked) {
        await _client.clearUserMark(id);
        _removeLocal(id);
        await _saveCache('user_marks_want_watch', _wantWatch);
        await _saveCache('user_marks_watched', _watched);
        notifyListeners();
        return null;
      }
      await _client.setUserMark(id, status);
      _applyLocal(movie, status);
      await _saveCache('user_marks_want_watch', _wantWatch);
      await _saveCache('user_marks_watched', _watched);
      notifyListeners();
      return status;
    } catch (e) {
      if (await _retryAfterRelogin(e, 0, () async {
        if (marked) {
          await _client.clearUserMark(id);
        } else {
          await _client.setUserMark(id, status);
        }
      })) {
        if (marked) {
          _removeLocal(id);
        } else {
          _applyLocal(movie, status);
        }
        await _saveCache('user_marks_want_watch', _wantWatch);
        await _saveCache('user_marks_watched', _watched);
        notifyListeners();
        return marked ? null : status;
      }
      rethrow;
    }
  }

  /// 播放结束后自动标看过（设置开关控制；已看过则跳过）。
  Future<void> markWatchedQuietly(Map<String, dynamic> movie) async {
    final id = movie['id'] as String?;
    if (id == null || id.isEmpty || isWatched(id)) return;
    try {
      await _client.setUserMark(id, kMarkWatched);
      _applyLocal(movie, kMarkWatched);
      await _saveCache('user_marks_watched', _watched);
      notifyListeners();
      AppLogger.info('Auto-marked watched: ${movie['number'] ?? id}');
    } catch (e) {
      AppLogger.warning('Auto-mark watched failed: $e');
    }
  }

  void _applyLocal(Map<String, dynamic> movie, String status) {
    final id = movie['id'] as String?;
    _wantWatch.removeWhere((m) => m['id'] == id);
    _watched.removeWhere((m) => m['id'] == id);
    if (status == kMarkWantWatch) {
      _wantWatch.insert(0, movie);
    } else {
      _watched.insert(0, movie);
    }
  }

  void _removeLocal(String id) {
    _wantWatch.removeWhere((m) => m['id'] == id);
    _watched.removeWhere((m) => m['id'] == id);
  }

  // ---------------------------------------------------------------------------
  // 清单（建/删/改名/移出影片；加入端点移动端 API 不提供）
  // ---------------------------------------------------------------------------

  Future<Map<String, dynamic>?> createList(String name) async {
    final list = await _client.createUserList(name);
    if ((list['id'] as String?)?.isNotEmpty == true) {
      await refreshLists();
    }
    return list;
  }

  Future<void> deleteList(String id) async {
    await _client.deleteUserList(id);
    await refreshLists();
  }

  Future<void> renameList(String id, String name) async {
    await _client.renameUserList(id, name);
    await refreshLists();
  }

  Future<void> removeMovieFromList(String listId, String listName,
      String movieId) async {
    await _client.removeMovieFromList(listId, listName, movieId);
    await refreshLists();
  }

  /// 登出时清空内存态（缓存由 AuthProvider.logout 一并清除）。
  void reset() {
    _wantWatch = [];
    _watched = [];
    _lists = [];
    _marksLoaded = false;
    notifyListeners();
  }
}
