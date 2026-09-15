import 'package:flutter/material.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../services/backend_launcher.dart';
import '../services/logger.dart';
import '../widgets/common_ui.dart';

/// 搜索页：圆角填充搜索框 + 演员快捷入口 + 结果视图（大图网格/小图列表）。
class SearchScreen extends StatefulWidget {
  /// 初始关键字（如从首页点演员头像进入时自动搜索）。
  final String? initialQuery;

  const SearchScreen({super.key, this.initialQuery});

  @override
  State<SearchScreen> createState() => _SearchScreenState();
}

class _SearchScreenState extends State<SearchScreen> {
  final _searchController = TextEditingController();
  late final JavDBClient _client;
  List<Movie> _movies = [];
  List<Actor> _actors = [];
  MovieViewMode _viewMode = MovieViewMode.grid;
  bool _isLoading = false;
  bool _hasSearched = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    final initial = widget.initialQuery?.trim() ?? '';
    if (initial.isNotEmpty) {
      _searchController.text = initial;
      _search();
    }
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  Future<void> _search() async {
    final query = _searchController.text.trim();
    if (query.isEmpty) return;

    setState(() {
      _isLoading = true;
      _error = null;
      _hasSearched = true;
    });

    try {
      AppLogger.info('Searching: $query');
      // 影片结果 + 演员档案并行请求；演员失败不影响影片结果展示
      final movieFuture = _client.search(query, limit: 20);
      final actorFuture = _client
          .search(query, limit: 12, scope: 'actor')
          .catchError((Object e) {
        AppLogger.warning('Actor search failed: $e');
        return <String, dynamic>{};
      });

      final result = await movieFuture;
      final moviesList = (result['movies'] as List)
          .map((m) => Movie.fromJson(m as Map<String, dynamic>))
          .toList();

      // 合并演员搜索结果与影片结果附带的演员，按 id 去重
      final actorMap = <String, Actor>{};
      final actorResult = await actorFuture;
      final fromActorSearch = (actorResult['actors'] as List?)
              ?.map((a) => Actor.fromJson(a as Map<String, dynamic>)) ??
          const <Actor>[];
      final fromMovies = (result['actors'] as List?)
              ?.map((a) => Actor.fromJson(a as Map<String, dynamic>)) ??
          const <Actor>[];
      for (final a in [...fromActorSearch, ...fromMovies]) {
        if (a.id.isNotEmpty) actorMap[a.id] = a;
      }

      setState(() {
        _movies = moviesList;
        _actors = actorMap.values.toList();
        _isLoading = false;
      });
      AppLogger.info(
          'Search returned ${moviesList.length} movies, ${_actors.length} actors');
    } catch (e) {
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
      AppLogger.error('Search failed', e);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.search, color: Theme.of(context).colorScheme.primary),
            const SizedBox(width: 8),
            const Text('搜索'),
          ],
        ),
        actions: [
          ViewModeToggle(
            mode: _viewMode,
            onChanged: (m) => setState(() => _viewMode = m),
          ),
        ],
      ),
      body: Column(
        children: [
          // 搜索框：圆角填充样式
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
            child: TextField(
              controller: _searchController,
              onSubmitted: (_) => _search(),
              textInputAction: TextInputAction.search,
              decoration: InputDecoration(
                hintText: '输入番号 / 演员 / 关键词',
                prefixIcon: const Icon(Icons.search),
                suffixIcon: IconButton(
                  icon: const Icon(Icons.arrow_forward),
                  tooltip: '搜索',
                  onPressed: _isLoading ? null : _search,
                ),
                filled: true,
                fillColor: cardColor(context),
                contentPadding: EdgeInsets.zero,
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(28),
                  borderSide: BorderSide.none,
                ),
              ),
            ),
          ),
          Expanded(
            child: _isLoading
                ? const Center(child: CircularProgressIndicator())
                : _error != null
                    ? ErrorRetryView(error: _error!, onRetry: _search)
                    : !_hasSearched
                        ? _initialView(context)
                        : _resultView(context),
          ),
        ],
      ),
    );
  }

  /// 初始引导态（未搜索）。
  Widget _initialView(BuildContext context) {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.manage_search,
              size: 64, color: Theme.of(context).hintColor),
          const SizedBox(height: 12),
          Text('输入番号、演员名或关键词开始搜索',
              style: TextStyle(color: Theme.of(context).hintColor)),
        ],
      ),
    );
  }

  /// 结果视图：相关演员横滑卡片 + 影片（大图网格/小图列表）。
  Widget _resultView(BuildContext context) {
    if (_movies.isEmpty && _actors.isEmpty) {
      return _emptyResultView(context);
    }
    return CustomScrollView(
      physics: const AlwaysScrollableScrollPhysics(),
      slivers: [
        if (_actors.isNotEmpty) _actorSection(context),
        if (_movies.isEmpty)
          const SliverFillRemaining(
            hasScrollBody: false,
            child: SizedBox(),
          )
        else if (_viewMode == MovieViewMode.grid)
          SliverPadding(
            padding: const EdgeInsets.fromLTRB(16, 4, 16, 24),
            sliver: SliverGrid(
              gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
                maxCrossAxisExtent: 170,
                mainAxisSpacing: 14,
                crossAxisSpacing: 12,
                childAspectRatio: 0.58,
              ),
              delegate: SliverChildBuilderDelegate(
                (context, index) => MovieGridCard(movie: _movies[index]),
                childCount: _movies.length,
              ),
            ),
          )
        else
          SliverPadding(
            padding: const EdgeInsets.fromLTRB(16, 4, 16, 24),
            sliver: SliverList.separated(
              itemBuilder: (context, index) =>
                  MovieCompactTile(movie: _movies[index]),
              separatorBuilder: (_, __) => const SizedBox(height: 10),
            ),
          ),
      ],
    );
  }

  /// 相关演员区：点击进入演员专题页而非关键词结果。
  Widget _actorSection(BuildContext context) {
    return SliverToBoxAdapter(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 8, 16, 8),
            child: Text('相关演员',
                style: TextStyle(fontSize: 15, fontWeight: FontWeight.bold)),
          ),
          SizedBox(
            height: 108,
            child: ListView.separated(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(horizontal: 16),
              itemCount: _actors.length,
              separatorBuilder: (_, __) => const SizedBox(width: 10),
              itemBuilder: (context, index) =>
                  ActorSearchCard(actor: _actors[index]),
            ),
          ),
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 12, 16, 8),
            child: Text('影片',
                style: TextStyle(fontSize: 15, fontWeight: FontWeight.bold)),
          ),
        ],
      ),
    );
  }

  /// 无结果态。
  Widget _emptyResultView(BuildContext context) {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.search_off, size: 64, color: Theme.of(context).hintColor),
          const SizedBox(height: 12),
          Text('没有找到相关影片或演员',
              style: TextStyle(color: Theme.of(context).hintColor)),
        ],
      ),
    );
  }
}
