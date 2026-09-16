import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../providers/subscription_provider.dart';
import '../services/backend_launcher.dart';
import '../services/logger.dart';
import '../widgets/common_ui.dart';

/// 影单详情页：展示一个 JavDB 社区影单（合集）的影片列表，
/// 支持翻页、排序与订阅（订阅后实时显示在首页"已订阅合集"区块）。
class ListDetailScreen extends StatefulWidget {
  const ListDetailScreen({
    super.key,
    required this.listId,
    required this.listName,
    this.moviesCount = 0,
  });

  final String listId;
  final String listName;
  /// 影片总数（来自搜索卡片/订阅记录），订阅时带上避免首页显示 0 部。
  final int moviesCount;

  @override
  State<ListDetailScreen> createState() => _ListDetailScreenState();
}

class _ListDetailScreenState extends State<ListDetailScreen> {
  late final JavDBClient _client;
  List<Movie> _movies = [];
  MovieViewMode _viewMode = MovieViewMode.list;
  String _sort = '';
  bool _isLoading = true;
  bool _subToggling = false;
  String? _error;
  int _page = 1;
  int _maxPage = 1;

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    context.read<SubscriptionProvider>().ensureLoaded();
    _loadMovies();
  }

  Future<void> _loadMovies({int page = 1}) async {
    setState(() {
      _isLoading = true;
      _error = null;
    });
    try {
      final result = await _client.getListMovies(widget.listId,
          page: page, sort: _sort);
      final moviesList = (result['movies'] as List?)
              ?.map((m) => Movie.fromJson(m as Map<String, dynamic>))
              .toList() ??
          const <Movie>[];
      if (!mounted) return;
      setState(() {
        _movies = moviesList;
        _page = (result['page'] as num?)?.toInt() ?? page;
        _maxPage = (result['maxPage'] as num?)?.toInt() ?? 1;
        _isLoading = false;
      });
      AppLogger.info(
          'List ${widget.listId}: ${moviesList.length} movies (page $_page/$_maxPage)');
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
      AppLogger.error('Failed to load list movies', e);
    }
  }

  Future<void> _toggleSubscribe() async {
    if (_subToggling) return;
    final subs = context.read<SubscriptionProvider>();
    final subscribed = subs.isSubscribed(widget.listId);
    setState(() => _subToggling = true);
    try {
      if (subscribed) {
        await subs.unsubscribe(widget.listId);
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('已取消订阅')),
          );
        }
      } else {
        await subs.subscribe(widget.listId, widget.listName,
            moviesCount: widget.moviesCount);
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('已订阅，可在首页查看')),
          );
        }
      }
    } catch (e) {
      AppLogger.error('Failed to toggle subscription', e);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('操作失败: $e'), backgroundColor: Colors.red),
        );
      }
    } finally {
      if (mounted) setState(() => _subToggling = false);
    }
  }

  void _goPage(int page) {
    if (page < 1 || page > _maxPage || page == _page) return;
    _loadMovies(page: page);
  }

  @override
  Widget build(BuildContext context) {
    final subscribed = context.watch<SubscriptionProvider>()
        .isSubscribed(widget.listId);
    return Scaffold(
      appBar: AppBar(
        title: Text(widget.listName.isEmpty ? '合集详情' : widget.listName,
            maxLines: 1, overflow: TextOverflow.ellipsis),
        actions: [
          SortSelector(
            options: actorSortOptions,
            selected: _sort,
            onChanged: (s) {
              setState(() => _sort = s);
              _loadMovies(page: 1);
            },
          ),
          ViewModeToggle(
            mode: _viewMode,
            onChanged: (m) => setState(() => _viewMode = m),
          ),
          IconButton(
            icon: subscribed
                ? const Icon(Icons.bookmark_added, color: Color(0xFFFFD54F))
                : const Icon(Icons.bookmark_add_outlined),
            tooltip: subscribed ? '取消订阅' : '订阅合集',
            onPressed: _subToggling ? null : _toggleSubscribe,
          ),
        ],
      ),
      body: Column(
        children: [
          Expanded(
            child: _isLoading
                ? const Center(child: CircularProgressIndicator())
                : _error != null
                    ? ErrorRetryView(
                        error: _error!,
                        onRetry: () => _loadMovies(page: _page))
                    : RefreshIndicator(
                        onRefresh: () => _loadMovies(page: _page),
                        child: _buildMovieList(context),
                      ),
          ),
          if (!_isLoading && _maxPage > 1) _buildPager(context),
        ],
      ),
    );
  }

  Widget _buildMovieList(BuildContext context) {
    if (_movies.isEmpty) {
      return Center(
        child: Text('暂无数据', style: TextStyle(color: Theme.of(context).hintColor)),
      );
    }
    if (_viewMode == MovieViewMode.grid) {
      return GridView.builder(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.fromLTRB(16, 4, 16, 24),
        gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
          maxCrossAxisExtent: 170,
          mainAxisSpacing: 14,
          crossAxisSpacing: 12,
          childAspectRatio: 0.58,
        ),
        itemCount: _movies.length,
        itemBuilder: (context, index) => MovieGridCard(movie: _movies[index]),
      );
    }
    return ListView.separated(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 24),
      itemCount: _movies.length,
      separatorBuilder: (_, __) => const SizedBox(height: 10),
      itemBuilder: (context, index) => RankingMovieTile(movie: _movies[index]),
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
            onPressed: _page > 1 ? () => _goPage(_page - 1) : null,
          ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Text('$_page / $_maxPage',
                style: Theme.of(context).textTheme.titleSmall),
          ),
          IconButton.outlined(
            icon: const Icon(Icons.chevron_right),
            tooltip: '下一页',
            onPressed: _page < _maxPage ? () => _goPage(_page + 1) : null,
          ),
        ],
      ),
    );
  }
}
