import 'package:flutter/material.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../services/backend_launcher.dart';
import '../services/image_url.dart';
import '../services/logger.dart';
import '../widgets/common_ui.dart';

/// 演员专题页：头部资料卡 + 作品列表（大图网格 / 小图列表切换，滚动分页）。
class ActorScreen extends StatefulWidget {
  /// 演员档案（至少含 id 与 name，来自搜索结果/榜单/详情页跳转）。
  final Actor actor;

  const ActorScreen({super.key, required this.actor});

  @override
  State<ActorScreen> createState() => _ActorScreenState();
}

class _ActorScreenState extends State<ActorScreen> {
  late final JavDBClient _client;
  final _scrollController = ScrollController();
  List<Movie> _movies = [];
  MovieViewMode _viewMode = MovieViewMode.grid;
  String _sort = '';  // 当前排序方式
  bool _isLoading = true;
  bool _isLoadingMore = false;
  bool _hasMore = true;
  int _page = 1;
  String? _error;

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    _loadFirstPage();
    _scrollController.addListener(_onScroll);
  }

  @override
  void dispose() {
    _scrollController.dispose();
    super.dispose();
  }

  /// 距底部不足一屏时加载下一页。
  void _onScroll() {
    if (_scrollController.position.pixels >=
            _scrollController.position.maxScrollExtent - 400 &&
        !_isLoading &&
        !_isLoadingMore &&
        _hasMore) {
      _loadMore();
    }
  }

  Future<void> _loadFirstPage({String? sort}) async {
    final currentSort = sort ?? _sort;
    setState(() {
      _isLoading = true;
      _error = null;
    });
    try {
      AppLogger.info('Loading actor movies: ${widget.actor.id}, sort=$currentSort');
      final result = await _client.actorMovies(
        widget.actor.id,
        page: 1,
        sort: currentSort.isNotEmpty ? currentSort : null,
      );
      _page = 1;
      _hasMore = _moviesFrom(result).isNotEmpty;
      setState(() {
        _movies = _moviesFrom(result);
        if (sort != null) _sort = sort;
        _isLoading = false;
      });
    } catch (e) {
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
      AppLogger.error('Actor movies failed', e);
    }
  }

  Future<void> _loadMore() async {
    setState(() => _isLoadingMore = true);
    try {
      final next = _page + 1;
      final result = await _client.actorMovies(
        widget.actor.id,
        page: next,
        sort: _sort.isNotEmpty ? _sort : null,
      );
      final more = _moviesFrom(result);
      setState(() {
        _page = next;
        if (more.isEmpty) {
          _hasMore = false;
        } else {
          // 按 id 去重，避免后端分页边界重复
          final seen = _movies.map((m) => m.id).toSet();
          _movies.addAll(more.where((m) => !seen.contains(m.id)));
        }
        _isLoadingMore = false;
      });
    } catch (e) {
      setState(() => _isLoadingMore = false);
      AppLogger.error('Actor movies load more failed', e);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('加载更多失败，请继续滚动重试')),
        );
      }
    }
  }

  List<Movie> _moviesFrom(Map<String, dynamic> result) {
    return (result['movies'] as List?)
            ?.map((m) => Movie.fromJson(m as Map<String, dynamic>))
            .toList() ??
        const <Movie>[];
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          widget.actor.name,
          overflow: TextOverflow.ellipsis,
        ),
        actions: [
          Padding(
            padding: const EdgeInsets.only(right: 8),
            child: SortSelector(
              options: actorSortOptions,
              selected: _sort,
              onChanged: (s) => _loadFirstPage(sort: s),
            ),
          ),
          ViewModeToggle(
            mode: _viewMode,
            onChanged: (m) => setState(() => _viewMode = m),
          ),
        ],
      ),
      body: _buildBody(context),
    );
  }

  Widget _buildBody(BuildContext context) {
    if (_isLoading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return ErrorRetryView(error: _error!, onRetry: _loadFirstPage);
    }
    return RefreshIndicator(
      onRefresh: _loadFirstPage,
      child: CustomScrollView(
        controller: _scrollController,
        physics: const AlwaysScrollableScrollPhysics(),
        slivers: [
          _buildHeader(context),
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(16, 4, 16, 10),
              child: Text(
                _movies.isEmpty ? '暂无作品' : '作品 (${_movies.length}${_hasMore ? '+' : ''})',
                style: const TextStyle(fontSize: 16, fontWeight: FontWeight.bold),
              ),
            ),
          ),
          if (_movies.isEmpty)
            const SliverFillRemaining(
              hasScrollBody: false,
              child: SizedBox(),
            )
          else ...[
            if (_viewMode == MovieViewMode.grid)
              SliverPadding(
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 0),
                sliver: SliverGrid(
                  gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
                    maxCrossAxisExtent: 170,
                    mainAxisSpacing: 14,
                    crossAxisSpacing: 12,
                    childAspectRatio: 0.58,
                  ),
                  delegate: SliverChildBuilderDelegate(
                    (context, index) =>
                        MovieGridCard(movie: _movies[index]),
                    childCount: _movies.length,
                  ),
                ),
              )
            else
              SliverPadding(
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 0),
                sliver: SliverList.separated(
                  itemBuilder: (context, index) =>
                      MovieCompactTile(movie: _movies[index]),
                  separatorBuilder: (_, __) => const SizedBox(height: 10),
                ),
              ),
          ],
          // 底部加载状态
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 16),
              child: Center(
                child: _isLoadingMore
                    ? const SizedBox(
                        width: 22,
                        height: 22,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : (_hasMore
                        ? Text('上拉加载更多',
                            style: TextStyle(
                                fontSize: 12,
                                color: Theme.of(context).hintColor))
                        : const SizedBox.shrink()),
              ),
            ),
          ),
          const SliverToBoxAdapter(child: SizedBox(height: 12)),
        ],
      ),
    );
  }

  /// 头部资料卡：头像 + 名字 + 作品数。
  Widget _buildHeader(BuildContext context) {
    final avatar = widget.actor.avatarUrl;
    return SliverToBoxAdapter(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
        child: Container(
          decoration: cardDecoration(context),
          padding: const EdgeInsets.all(14),
          child: Row(
            children: [
              ClipRRect(
                borderRadius: BorderRadius.circular(12),
                child: SizedBox(
                  width: 84,
                  height: 112,
                  child: avatar != null
                      ? Image.network(
                          resolveImageUrl(avatar),
                          fit: BoxFit.cover,
                          errorBuilder: (_, __, ___) => Container(
                            color: Theme.of(context).dividerColor,
                            child: const Icon(Icons.person, size: 40),
                          ),
                        )
                      : Container(
                          color: Theme.of(context).dividerColor,
                          child: const Icon(Icons.person, size: 40),
                        ),
                ),
              ),
              const SizedBox(width: 14),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      widget.actor.name,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                          fontSize: 18, fontWeight: FontWeight.bold),
                    ),
                    if (widget.actor.videosCount != null) ...[
                      const SizedBox(height: 6),
                      Text(
                        '${widget.actor.videosCount} 部作品',
                        style: TextStyle(
                            fontSize: 13,
                            color: Theme.of(context).hintColor),
                      ),
                    ],
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
