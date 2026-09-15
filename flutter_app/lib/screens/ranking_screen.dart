import 'package:flutter/material.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../services/backend_launcher.dart';
import '../services/logger.dart';
import '../widgets/common_ui.dart';

/// 榜单页：顶部胶囊切换榜单类型，下方卡片化条目列表（影片/演员）。
class RankingScreen extends StatefulWidget {
  /// 初始展示的榜单类型（playback/movies/top250/actors），首页入口使用。
  final String? initialKind;

  const RankingScreen({super.key, this.initialKind});

  @override
  State<RankingScreen> createState() => _RankingScreenState();
}

class _RankingScreenState extends State<RankingScreen> {
  static const _kindLabels = {
    'playback': '热播榜',
    'movies': '影片榜',
    'top250': 'TOP250',
    'actors': '演员榜',
  };

  late final JavDBClient _client;
  List<Movie> _movies = [];
  List<Actor> _actors = [];
  /// 影片榜的展示模式（大图网格/小图列表）；演员榜不参与切换。
  MovieViewMode _viewMode = MovieViewMode.list;
  bool _isLoading = true;
  String? _error;
  late String _selectedKind;

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    _selectedKind = widget.initialKind ?? 'playback';
    _loadRanking();
  }

  Future<void> _loadRanking() async {
    setState(() {
      _isLoading = true;
      _error = null;
    });

    try {
      AppLogger.info('Loading ranking: $_selectedKind');
      final result = await _client.getRanking(_selectedKind);
      // 后端 movies/actors 互斥返回（演员榜只有 actors），需 null 安全解析
      final moviesList = (result['movies'] as List?)
              ?.map((m) => Movie.fromJson(m as Map<String, dynamic>))
              .toList() ??
          const <Movie>[];
      final actorsList = (result['actors'] as List?)
              ?.map((a) => Actor.fromJson(a as Map<String, dynamic>))
              .toList() ??
          const <Actor>[];
      setState(() {
        _movies = moviesList;
        _actors = actorsList;
        _isLoading = false;
      });
      AppLogger.info(
          'Loaded ${moviesList.length} movies, ${actorsList.length} actors');
    } catch (e) {
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
      AppLogger.error('Failed to load ranking', e);
    }
  }

  void _switchKind(String kind) {
    if (kind == _selectedKind) return;
    setState(() => _selectedKind = kind);
    _loadRanking();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.emoji_events,
                color: Theme.of(context).colorScheme.primary),
            const SizedBox(width: 8),
            const Text('榜单'),
          ],
        ),
        actions: [
          if (_selectedKind != 'actors')
            ViewModeToggle(
              mode: _viewMode,
              onChanged: (m) => setState(() => _viewMode = m),
            ),
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: '刷新',
            onPressed: _isLoading ? null : _loadRanking,
          ),
        ],
      ),
      body: Column(
        children: [
          // 榜单类型切换（横向滚动胶囊）
          SizedBox(
            height: 52,
            child: ListView(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
              children: _kindLabels.entries.map((e) {
                final selected = e.key == _selectedKind;
                return Padding(
                  padding: const EdgeInsets.only(right: 8),
                  child: ChoiceChip(
                    label: Text(e.value),
                    selected: selected,
                    onSelected: (_) => _switchKind(e.key),
                  ),
                );
              }).toList(),
            ),
          ),
          Expanded(
            child: _isLoading
                ? const Center(child: CircularProgressIndicator())
                : _error != null
                    ? ErrorRetryView(error: _error!, onRetry: _loadRanking)
                    : RefreshIndicator(
                        onRefresh: _loadRanking,
                        child: _selectedKind == 'actors'
                            ? _buildActorList(context)
                            : _buildMovieList(context),
                      ),
          ),
        ],
      ),
    );
  }

  Widget _buildMovieList(BuildContext context) {
    if (_movies.isEmpty) {
      return _emptyView(context);
    }
    // 大图模式：海报网格；小图模式：带排名徽章的列表
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

  Widget _buildActorList(BuildContext context) {
    if (_actors.isEmpty) {
      return _emptyView(context);
    }
    return ListView.separated(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 24),
      itemCount: _actors.length,
      separatorBuilder: (_, __) => const SizedBox(height: 10),
      itemBuilder: (context, index) => RankingActorTile(actor: _actors[index]),
    );
  }

  Widget _emptyView(BuildContext context) {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.inbox_outlined,
              size: 56, color: Theme.of(context).hintColor),
          const SizedBox(height: 12),
          Text('暂无数据', style: TextStyle(color: Theme.of(context).hintColor)),
        ],
      ),
    );
  }
}
