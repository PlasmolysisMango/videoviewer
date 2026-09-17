import 'package:flutter/foundation.dart';

import '../api/client.dart';
import '../services/logger.dart';

/// 订阅类别常量，与后端 subscriptions.go 对应。
const kSubCollection = 'collection';
const kSubGenre = 'genre';
const kSubActor = 'actor';

/// 订阅状态：合集/题材/演员的订阅与取消会立即通知监听者（首页各分栏），
/// 避免依赖页面返回时机手动刷新。记录为后端原样的 map：
/// 合集 {id,name,movies_count}、题材 {id,name,group}、演员 {id,name,avatar}。
class SubscriptionProvider extends ChangeNotifier {
  SubscriptionProvider(this._client);

  final JavDBClient _client;
  List<Map<String, dynamic>> _subscriptions = [];
  bool _loaded = false;

  List<Map<String, dynamic>> get subscriptions => _subscriptions;

  /// 某类别下的全部订阅（缺 kind 的旧记录视为合集）。
  List<Map<String, dynamic>> byKind(String kind) => _subscriptions
      .where(((s) => ((s['kind'] as String?) ?? kSubCollection) == kind))
      .toList();

  bool isSubscribed(String kind, String id) => _subscriptions.any((s) =>
      ((s['kind'] as String?) ?? kSubCollection) == kind &&
      (s['id'] as String?) == id);

  /// 首次加载（失败静默，首页仍可用；订阅/取消时会重新拉取）。
  Future<void> load() async {
    try {
      _subscriptions = await _client.getSubscriptions();
      _loaded = true;
      notifyListeners();
    } catch (e) {
      AppLogger.warning('Failed to load subscriptions: $e');
    }
  }

  /// 若尚未成功加载过（比如首页数据加载失败），先拉一次再订阅。
  Future<void> ensureLoaded() async {
    if (!_loaded) await load();
  }

  Future<void> subscribeCollection(String id, String name,
      {int moviesCount = 0}) async {
    await _client.subscribe(kSubCollection, id, name,
        moviesCount: moviesCount);
    await load();
    AppLogger.info('Subscribed collection $id');
  }

  /// 题材订阅：group 为 web 筛选组编号（c{N} 的 N），id 为 tag ID。
  Future<void> subscribeGenre(String group, String tagId, String name) async {
    await _client.subscribe(kSubGenre, tagId, name, group: group);
    await load();
    AppLogger.info('Subscribed genre $group/$tagId');
  }

  Future<void> subscribeActor(String id, String name, {String? avatar}) async {
    await _client.subscribe(kSubActor, id, name, avatar: avatar);
    await load();
    AppLogger.info('Subscribed actor $id');
  }

  Future<void> unsubscribe(String kind, String id) async {
    await _client.unsubscribe(kind, id);
    await load();
    AppLogger.info('Unsubscribed $kind $id');
  }
}
