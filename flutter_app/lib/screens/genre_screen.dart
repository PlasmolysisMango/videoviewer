import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../providers/subscription_provider.dart';
import '../services/backend_launcher.dart';
import '../services/logger.dart';
import '../widgets/common_ui.dart';
import 'login_screen.dart';

/// 题材影片页：按 tag 组（角色/主題/服裝…）浏览影片。
/// 数据走网页版 /tags?c{group}={tag}，需要网页版登录态（未导入 Cookie 时
/// 报错信息下提供"登录"入口，Cookie 导入已合并进登录页）；
/// AppBar 的书签按钮把当前题材订阅到首页。
class GenreScreen extends StatefulWidget {
  const GenreScreen({
    super.key,
    required this.groupId,
    required this.tagId,
    required this.title,
  });

  /// web 端筛选组编号（c{N} 的 N），如 role=2、subject=3。
  final String groupId;
  final String tagId;
  final String title;

  @override
  State<GenreScreen> createState() => _GenreScreenState();
}

class _GenreScreenState extends State<GenreScreen> {
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
      final result = await _client.getGenreMovies(widget.groupId, widget.tagId,
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
          'Genre ${widget.groupId}/${widget.tagId}: ${moviesList.length} movies (page $_page/$_maxPage)');
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
      AppLogger.error('Failed to load genre movies', e);
    }
  }

  Future<void> _toggleSubscribe() async {
    if (_subToggling) return;
    final subs = context.read<SubscriptionProvider>();
    final subscribed = subs.isSubscribed(kSubGenre, widget.tagId);
    setState(() => _subToggling = true);
    try {
      if (subscribed) {
        await subs.unsubscribe(kSubGenre, widget.tagId);
        _toast('已取消订阅');
      } else {
        await subs.subscribeGenre(widget.groupId, widget.tagId, widget.title);
        _toast('已订阅，可在首页查看');
      }
    } catch (e) {
      AppLogger.error('Failed to toggle genre subscription', e);
      _toast('操作失败: $e', error: true);
    } finally {
      if (mounted) setState(() => _subToggling = false);
    }
  }

  void _toast(String msg, {bool error = false}) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(msg), backgroundColor: error ? Colors.red : null),
    );
  }

  void _goPage(int page) {
    if (page < 1 || page > _maxPage || page == _page) return;
    _loadMovies(page: page);
  }

  @override
  Widget build(BuildContext context) {
    final subscribed = context
        .watch<SubscriptionProvider>()
        .isSubscribed(kSubGenre, widget.tagId);
    return Scaffold(
      appBar: AppBar(
        title: Text(widget.title.isEmpty ? '题材影片' : widget.title,
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
            tooltip: subscribed ? '取消订阅题材' : '订阅题材到首页',
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
                    ? _buildError(context)
                    : RefreshIndicator(
                        onRefresh: () => _loadMovies(page: _page),
                        child: _buildMovieList(context),
                      ),
          ),
          if (!_isLoading && _error == null && _maxPage > 1)
            _buildPager(context),
        ],
      ),
    );
  }

  /// 加载失败视图：登录墙错误（未导入网页版 Cookie）在下方面给"登录"入口，
  /// 从登录页导入 Cookie 返回后自动重试。
  Widget _buildError(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 32),
            child: Text('加载失败: $_error',
                textAlign: TextAlign.center,
                style: TextStyle(
                    color: Theme.of(context).colorScheme.error, fontSize: 12)),
          ),
          const SizedBox(height: 16),
          Row(mainAxisSize: MainAxisSize.min, children: [
            OutlinedButton(
              onPressed: () => _loadMovies(page: _page),
              child: const Text('重试'),
            ),
            const SizedBox(width: 12),
            FilledButton.icon(
              onPressed: () => Navigator.push(
                context,
                MaterialPageRoute(builder: (_) => const LoginScreen()),
              ).then((_) => _loadMovies(page: _page)),
              icon: const Icon(Icons.login, size: 18),
              label: const Text('登录'),
            ),
          ]),
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
