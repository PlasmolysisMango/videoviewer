import 'package:flutter/foundation.dart';

import '../api/client.dart';
import '../services/logger.dart';

/// 订阅状态：影单订阅/取消后立即通知监听者（首页已订阅合集区块），
/// 避免依赖页面返回时机手动刷新。
class SubscriptionProvider extends ChangeNotifier {
  SubscriptionProvider(this._client);

  final JavDBClient _client;
  List<Map<String, dynamic>> _subscriptions = [];
  bool _loaded = false;

  List<Map<String, dynamic>> get subscriptions => _subscriptions;

  bool isSubscribed(String listId) =>
      _subscriptions.any((s) => (s['id'] as String?) == listId);

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

  Future<void> subscribe(String listId, String name,
      {int moviesCount = 0}) async {
    await _client.subscribeList(listId, name, moviesCount: moviesCount);
    await load();
    AppLogger.info('Subscribed list $listId');
  }

  Future<void> unsubscribe(String listId) async {
    await _client.unsubscribeList(listId);
    await load();
    AppLogger.info('Unsubscribed list $listId');
  }
}
