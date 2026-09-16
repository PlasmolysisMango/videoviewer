import 'package:flutter/material.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../services/backend_launcher.dart';
import '../services/logger.dart';
import '../widgets/common_ui.dart';

/// 弹出网页版 Cookie 导入对话框：题材浏览（/tags?c{N} 页面）需要网页登录态，
/// 而网页登录表单带图形验证码无法自动化，只能由用户从浏览器复制 Cookie 导入。
Future<void> showWebCookieImportDialog(
    BuildContext context, JavDBClient client) async {
  final controller = TextEditingController();
  final confirmed = await showDialog<bool>(
    context: context,
    builder: (dialogContext) => AlertDialog(
      title: const Text('导入网页版 Cookie'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text(
            '题材分类走 JavDB 网页版页面，需要登录态。请在浏览器登录 javdb.com 后，'
            '从开发者工具复制请求头里的整段 Cookie 粘贴到这里。',
            style: TextStyle(fontSize: 13),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: controller,
            maxLines: 4,
            decoration: const InputDecoration(
              hintText: '粘贴 Cookie ...',
              border: OutlineInputBorder(),
              isDense: true,
            ),
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(dialogContext, false),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: () => Navigator.pop(dialogContext, true),
          child: const Text('导入'),
        ),
      ],
    ),
  );
  if (confirmed != true || !context.mounted) return;
  var cookie = controller.text.trim();
  if (cookie.toLowerCase().startsWith('cookie:')) {
    cookie = cookie.substring(7).trim();
  }
  if (cookie.isEmpty) return;
  try {
    await client.setWebCookie(cookie);
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Cookie 已导入')),
      );
    }
  } catch (e) {
    AppLogger.error('Failed to import web cookie', e);
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('导入失败: $e'), backgroundColor: Colors.red),
      );
    }
  }
}

/// 题材影片页：按 tag 组（角色/主題/服裝…）浏览影片，
/// 数据走网页版 /tags?c{group}={tag}，未导入网页版 Cookie 时提示导入。
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
  String? _error;
  int _page = 1;
  int _maxPage = 1;

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
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

  void _goPage(int page) {
    if (page < 1 || page > _maxPage || page == _page) return;
    _loadMovies(page: page);
  }

  @override
  Widget build(BuildContext context) {
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
            FilledButton.tonal(
              onPressed: () => showWebCookieImportDialog(context, _client)
                  .then((_) => _loadMovies(page: _page)),
              child: const Text('导入网页版 Cookie'),
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
