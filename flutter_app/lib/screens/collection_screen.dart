import 'dart:async';
import 'dart:math';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/client.dart';
import '../providers/subscription_provider.dart';
import '../services/backend_launcher.dart';
import '../services/data_cache.dart';
import '../services/logger.dart';
import 'list_detail_screen.dart';
import 'list_search_screen.dart';

/// 合集页（题材式二级结构）：
/// 一级用小框网格固定合集——顶部"我的合集"（已订阅），
/// 其下"来自订阅题材"（按已订阅题材名搜索出的合集）与"发现合集"（随机关键词）；
/// 点击小框进入二级影单详情页（ListDetailScreen），右上角搜索按钮搜索合集。
/// 关键词池用繁体：JavDB 站内影单标题以繁体为主，简体命中率低。
class CollectionScreen extends StatefulWidget {
  const CollectionScreen({super.key});

  @override
  State<CollectionScreen> createState() => _CollectionScreenState();
}

/// 订阅标记统一用低饱和灰，与题材页书签一致。
const _subMarkColor = Color(0xFF9AA3AD);

/// 随机发现的关键词池（繁体，覆盖常见影单命名习惯）。
const _discoverKeywords = [
  '精選', '推薦', '合集', '收藏', '必看', '經典', '中文', '無碼',
  '中字', '素人', '寫真', '盤點', '私藏', '稀有', '極品', '整理',
];

class _CollectionScreenState extends State<CollectionScreen> {
  late final JavDBClient _client;
  final _random = Random();

  /// 题材/随机发现区块的持久缓存（6 小时）：重进页面直接展示，
  /// 不再每次都等网络刷新；下拉刷新强制走网络并更新缓存。
  static const _discoverCacheKey = 'collection.discover.v1';

  List<Map<String, dynamic>> _genreLists = []; // 来自订阅题材的推荐
  List<Map<String, dynamic>> _discoverLists = []; // 随机关键词发现
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    context.read<SubscriptionProvider>().ensureLoaded().then((_) {
      if (mounted) _load();
    });
  }

  /// 并行聚合：订阅题材名搜索（各取前 3 条，最多 5 个题材）+
  /// 随机关键词（2 个，各取前 8 条，随机页码 1-3）。单一来源失败静默跳过。
  /// 默认缓存优先；force=true（下拉刷新/重试）跳过缓存。
  Future<void> _load({bool force = false}) async {
    if (!force) {
      try {
        final cached = await DataCache.instance
            .read(_discoverCacheKey, maxAge: const Duration(hours: 6));
        if (cached is Map && mounted) {
          final g = (cached['genre'] as List?)
                  ?.map((e) => e as Map<String, dynamic>)
                  .toList() ??
              const <Map<String, dynamic>>[];
          final d = (cached['discover'] as List?)
                  ?.map((e) => e as Map<String, dynamic>)
                  .toList() ??
              const <Map<String, dynamic>>[];
          if (g.isNotEmpty || d.isNotEmpty) {
            setState(() {
              _genreLists = g;
              _discoverLists = d;
              _loading = false;
            });
            return;
          }
        }
      } catch (_) {}
    }
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final genres = context
          .read<SubscriptionProvider>()
          .byKind(kSubGenre)
          .map((s) => (s['name'] as String?) ?? '')
          .where((n) => n.trim().isNotEmpty)
          .take(5)
          .toList();
      final pool = [..._discoverKeywords]..shuffle(_random);

      final futures = <Future<List<Map<String, dynamic>>>>[
        for (final g in genres) _searchSafe(g, 1),
        _searchSafe(pool[0], _random.nextInt(3) + 1),
        if (pool.length > 1) _searchSafe(pool[1], _random.nextInt(3) + 1),
      ];
      final batches = await Future.wait(futures);

      // 全局去重：题材推荐与随机发现互不重复；各区块上限 12。
      final seen = <String>{};
      final genreLists = _dedupe(batches.sublist(0, genres.length), seen, 12);
      final discoverLists =
          _dedupe(batches.sublist(genres.length), seen, 12);

      if (!mounted) return;
      setState(() {
        _genreLists = genreLists;
        _discoverLists = discoverLists;
        _loading = false;
      });
      unawaited(DataCache.instance.write(_discoverCacheKey, {
        'genre': genreLists,
        'discover': discoverLists,
      }));
      AppLogger.info(
          'Collection page loaded: ${genreLists.length} genre + ${discoverLists.length} discover');
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _loading = false;
      });
      AppLogger.error('Failed to load collection page', e);
    }
  }

  /// 搜索合集（容错：失败返回空列表，不阻塞其他来源）。
  Future<List<Map<String, dynamic>>> _searchSafe(String q, int page) async {
    try {
      final lists =
          await _client.searchLists(q, page: page).timeout(const Duration(seconds: 12));
      return lists;
    } catch (e) {
      AppLogger.warning('List search "$q" failed: $e');
      return const [];
    }
  }

  /// 按批次顺序去重取前 cap 条（id 为空或已见过的跳过）。
  List<Map<String, dynamic>> _dedupe(
      Iterable<List<Map<String, dynamic>>> batches, Set<String> seen, int cap) {
    final out = <Map<String, dynamic>>[];
    for (final batch in batches) {
      for (final l in batch) {
        final id = (l['id'] as String?) ?? '';
        if (id.isEmpty || seen.contains(id)) continue;
        seen.add(id);
        out.add(l);
        if (out.length >= cap) return out;
      }
    }
    return out;
  }

  @override
  Widget build(BuildContext context) {
    final subs = context.watch<SubscriptionProvider>();
    final subscribed = subs.byKind(kSubCollection);
    final genres = subs.byKind(kSubGenre);

    return Scaffold(
      appBar: AppBar(
        title: const Text('合集'),
        actions: [
          // 右上角搜索：进入合集搜索页
          IconButton(
            icon: const Icon(Icons.search),
            tooltip: '搜索合集',
            onPressed: () => Navigator.push(
              context,
              MaterialPageRoute(builder: (_) => const ListSearchScreen()),
            ),
          ),
        ],
      ),
      body: _loading
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
                          onPressed: () => _load(force: true),
                          child: const Text('重试')),
                    ],
                  ),
                )
              : RefreshIndicator(
                  onRefresh: () => _load(force: true),
                  child: ListView(
                    physics: const AlwaysScrollableScrollPhysics(),
                    padding: const EdgeInsets.fromLTRB(12, 8, 12, 24),
                    children: [
                      _sectionTitle('我的合集 (${subscribed.length})'),
                      if (subscribed.isEmpty)
                        Padding(
                          padding: const EdgeInsets.symmetric(vertical: 10),
                          child: Text('搜索并订阅合集后固定在这里',
                              style: TextStyle(
                                  fontSize: 12,
                                  color: Theme.of(context).hintColor)),
                        )
                      else
                        _listGrid(subscribed),
                      if (genres.isNotEmpty && _genreLists.isNotEmpty) ...[
                        const SizedBox(height: 8),
                        _sectionTitle('来自你的订阅题材'),
                        _listGrid(_genreLists),
                      ],
                      if (_discoverLists.isNotEmpty) ...[
                        const SizedBox(height: 8),
                        _sectionTitle('发现合集'),
                        _listGrid(_discoverLists),
                        Center(
                          child: Text('下拉刷新换一批',
                              style: TextStyle(
                                  fontSize: 11,
                                  color: Theme.of(context).hintColor)),
                        ),
                      ],
                      if (subscribed.isEmpty &&
                          _genreLists.isEmpty &&
                          _discoverLists.isEmpty)
                        Padding(
                          padding: const EdgeInsets.symmetric(vertical: 32),
                          child: Center(
                            child: Text('暂无合集数据，下拉重试或搜索合集',
                                style: TextStyle(
                                    color: Theme.of(context).hintColor)),
                          ),
                        ),
                    ],
                  ),
                ),
    );
  }

  Widget _sectionTitle(String text) => Padding(
        padding: const EdgeInsets.fromLTRB(4, 8, 4, 8),
        child: Text(text,
            style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w700)),
      );

  /// 合集小框网格：与题材页卡片同规格（圆角 12、居中名称、右上角订阅书签）。
  Widget _listGrid(List<Map<String, dynamic>> lists) {
    return GridView.builder(
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      padding: EdgeInsets.zero,
      gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
        maxCrossAxisExtent: 168,
        mainAxisSpacing: 10,
        crossAxisSpacing: 10,
        childAspectRatio: 3.0,
      ),
      itemCount: lists.length,
      itemBuilder: (context, i) =>
          _ListCard(list: lists[i], client: _client),
    );
  }
}

/// 合集小框卡片：点击进二级影单详情页；右上角书签切换订阅（低饱和灰）。
class _ListCard extends StatelessWidget {
  final Map<String, dynamic> list;
  final JavDBClient client;

  const _ListCard({required this.list, required this.client});

  @override
  Widget build(BuildContext context) {
    final subs = context.watch<SubscriptionProvider>();
    final id = (list['id'] as String?) ?? '';
    final name = (list['name'] as String?) ?? id;
    final count = (list['movies_count'] as num?)?.toInt() ?? 0;
    final subscribed = id.isNotEmpty && subs.isSubscribed(kSubCollection, id);

    return Stack(
      children: [
        InkWell(
          borderRadius: BorderRadius.circular(12),
          onTap: () {
            if (id.isEmpty) return;
            Navigator.push(
              context,
              MaterialPageRoute(
                builder: (_) => ListDetailScreen(
                    listId: id, listName: name, moviesCount: count),
              ),
            );
          },
          child: Container(
            decoration: BoxDecoration(
              color: Theme.of(context).cardColor,
              borderRadius: BorderRadius.circular(12),
              border: Border.all(color: Theme.of(context).dividerColor),
            ),
            child: Center(
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 10),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(name,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(fontSize: 13)),
                    if (count > 0)
                      Text('$count 部',
                          style: TextStyle(
                              fontSize: 11,
                              color: Theme.of(context).hintColor)),
                  ],
                ),
              ),
            ),
          ),
        ),
        // 右上角订阅书签：与题材卡一致的交互
        Positioned(
          top: 0,
          right: 0,
          child: InkWell(
            borderRadius: const BorderRadius.only(
              topRight: Radius.circular(12),
              bottomLeft: Radius.circular(12),
            ),
            onTap: () => _toggleSubscribe(context, id, name, count,
                subscribed: subscribed),
            child: Padding(
              padding: const EdgeInsets.all(5),
              child: Icon(
                subscribed
                    ? Icons.bookmark_added
                    : Icons.bookmark_add_outlined,
                size: 16,
                color: subscribed
                    ? _subMarkColor
                    : Theme.of(context).hintColor,
              ),
            ),
          ),
        ),
      ],
    );
  }

  Future<void> _toggleSubscribe(BuildContext context, String id, String name,
      int count, {required bool subscribed}) async {
    if (id.isEmpty) return;
    final subs = context.read<SubscriptionProvider>();
    try {
      if (subscribed) {
        await subs.unsubscribe(kSubCollection, id);
        if (context.mounted) _toast(context, '已取消订阅');
      } else {
        await subs.subscribeCollection(id, name, moviesCount: count);
        if (context.mounted) _toast(context, '已订阅，固定在“我的合集”');
      }
    } catch (e) {
      AppLogger.error('Failed to toggle collection subscription', e);
      if (context.mounted) _toast(context, '操作失败: $e', error: true);
    }
  }

  void _toast(BuildContext context, String msg, {bool error = false}) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(msg), backgroundColor: error ? Colors.red : null),
    );
  }
}
