import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../providers/subscription_provider.dart';
import '../services/backend_launcher.dart';
import '../services/image_url.dart';
import '../services/logger.dart';
import '../widgets/common_ui.dart';

/// 演员页：演员榜名单浏览，支持翻页；书签按钮把演员订阅到首页
/// （首页"演员"分栏带图钉角标），点击头像进入演员专题页（作品列表）。
class ActorCatalogScreen extends StatefulWidget {
  const ActorCatalogScreen({super.key});

  @override
  State<ActorCatalogScreen> createState() => _ActorCatalogScreenState();
}

class _ActorCatalogScreenState extends State<ActorCatalogScreen> {
  late final JavDBClient _client;
  List<Actor> _actors = [];
  bool _isLoading = true;
  String? _error;
  int _page = 1;
  int _maxPage = 1;

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    context.read<SubscriptionProvider>().ensureLoaded();
    _load();
  }

  Future<void> _load({int page = 1}) async {
    setState(() {
      _isLoading = true;
      _error = null;
    });
    try {
      final result = await _client.getRanking('actors', page: page, limit: 40);
      final actors = (result['actors'] as List?)
              ?.map((a) => Actor.fromJson(a as Map<String, dynamic>))
              .toList() ??
          const <Actor>[];
      if (!mounted) return;
      setState(() {
        _actors = actors;
        _page = (result['page'] as num?)?.toInt() ?? page;
        _maxPage = (result['maxPage'] as num?)?.toInt() ?? 1;
        _isLoading = false;
      });
      AppLogger.info('Actor catalog: ${actors.length} actors (page $_page)');
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
      AppLogger.error('Failed to load actor catalog', e);
    }
  }

  Future<void> _toggleSubscribe(Actor actor, {required bool subscribed}) async {
    final subs = context.read<SubscriptionProvider>();
    try {
      if (subscribed) {
        await subs.unsubscribe(kSubActor, actor.id);
        if (mounted) _toast('已取消订阅');
      } else {
        await subs.subscribeActor(actor.id, actor.name,
            avatar: actor.avatarUrl);
        if (mounted) _toast('已订阅，可在首页查看');
      }
    } catch (e) {
      AppLogger.error('Failed to toggle actor subscription', e);
      if (mounted) _toast('操作失败: $e', error: true);
    }
  }

  void _toast(String msg, {bool error = false}) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(msg), backgroundColor: error ? Colors.red : null),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('演员')),
      body: Column(
        children: [
          Expanded(
            child: _isLoading
                ? const Center(child: CircularProgressIndicator())
                : _error != null
                    ? Center(
                        child: Column(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Text('加载失败: $_error',
                                textAlign: TextAlign.center,
                                style: TextStyle(
                                    color: Theme.of(context).colorScheme.error,
                                    fontSize: 12)),
                            const SizedBox(height: 12),
                            OutlinedButton(
                                onPressed: () => _load(page: _page),
                                child: const Text('重试')),
                          ],
                        ),
                      )
                    : RefreshIndicator(
                        onRefresh: () => _load(page: _page),
                        child: _buildGrid(context),
                      ),
          ),
          if (!_isLoading && _error == null && _maxPage > 1) _buildPager(context),
        ],
      ),
    );
  }

  Widget _buildGrid(BuildContext context) {
    if (_actors.isEmpty) {
      return Center(
        child: Text('暂无数据', style: TextStyle(color: Theme.of(context).hintColor)),
      );
    }
    final subs = context.watch<SubscriptionProvider>();
    return GridView.builder(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
      gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
        maxCrossAxisExtent: 110,
        mainAxisSpacing: 16,
        crossAxisSpacing: 12,
        childAspectRatio: 0.72,
      ),
      itemCount: _actors.length,
      itemBuilder: (context, i) {
        final actor = _actors[i];
        final subscribed = subs.isSubscribed(kSubActor, actor.id);
        return _buildActorCard(context, actor, subscribed: subscribed);
      },
    );
  }

  Widget _buildActorCard(BuildContext context, Actor actor,
      {required bool subscribed}) {
    return InkWell(
      borderRadius: BorderRadius.circular(12),
      onTap: () => pushActorScreen(context, actor),
      child: Column(
        children: [
          Stack(
            clipBehavior: Clip.none,
            children: [
              CircleAvatar(
                radius: 38,
                backgroundColor: Theme.of(context).colorScheme.surfaceContainerHighest,
                backgroundImage: actor.avatarUrl != null
                    ? NetworkImage(resolveImageUrl(actor.avatarUrl!))
                    : null,
                onBackgroundImageError:
                    actor.avatarUrl != null ? (_, __) {} : null,
                child: actor.avatarUrl == null
                    ? const Icon(Icons.person, size: 36)
                    : null,
              ),
              // 订阅标记：头像右上角图钉角标
              if (subscribed)
                Positioned(
                  top: -2,
                  right: -2,
                  child: Container(
                    padding: const EdgeInsets.all(3),
                    decoration: const BoxDecoration(
                      color: Color(0xFFFFD54F),
                      shape: BoxShape.circle,
                    ),
                    child: const Icon(Icons.push_pin,
                        size: 10, color: Colors.black87),
                  ),
                ),
            ],
          ),
          const SizedBox(height: 6),
          Text(
            actor.name,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(fontSize: 12),
          ),
          SizedBox(
            height: 26,
            child: IconButton(
              visualDensity: VisualDensity.compact,
              padding: EdgeInsets.zero,
              iconSize: 18,
              icon: subscribed
                  ? const Icon(Icons.bookmark_added, color: Color(0xFFFFD54F))
                  : const Icon(Icons.bookmark_add_outlined),
              tooltip: subscribed ? '取消订阅' : '订阅到首页',
              onPressed: () =>
                  _toggleSubscribe(actor, subscribed: subscribed),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildPager(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          IconButton.outlined(
            icon: const Icon(Icons.chevron_left),
            tooltip: '上一页',
            onPressed: _page > 1 ? () => _load(page: _page - 1) : null,
          ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Text('$_page / $_maxPage',
                style: Theme.of(context).textTheme.titleSmall),
          ),
          IconButton.outlined(
            icon: const Icon(Icons.chevron_right),
            tooltip: '下一页',
            onPressed: _page < _maxPage ? () => _load(page: _page + 1) : null,
          ),
        ],
      ),
    );
  }
}
