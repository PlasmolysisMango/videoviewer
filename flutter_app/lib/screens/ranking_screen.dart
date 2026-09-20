import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../providers/user_state_provider.dart';
import '../services/backend_launcher.dart';
import '../services/logger.dart';
import '../widgets/common_ui.dart';
import '../widgets/hide_watched_toggle.dart';

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
    'censored': '有码榜',
    'uncensored': '无码榜',
    'western': '欧美榜',
    'fc2': 'FC2榜',
    'actors': '演员榜',
  };

  /// 四个专门类型榜：数据走 TOP250 接口 + vtype 切面（官方 App 能力）。
  static const _top250KindVtypes = {
    'censored': 'censored',
    'uncensored': 'uncensored',
    'western': 'western',
    'fc2': 'fc2',
  };

  /// 当前 kind 是否走 TOP250 接口（TOP250 本身或四个专门类型榜）。
  bool get _isTop250Based =>
      _selectedKind == 'top250' || _top250KindVtypes.containsKey(_selectedKind);

  late final JavDBClient _client;
  List<Movie> _movies = [];
  List<Actor> _actors = [];
  /// 影片榜的展示模式（大图网格/小图列表）；演员榜不参与切换。
  MovieViewMode _viewMode = MovieViewMode.list;
  bool _isLoading = true;
  String? _error;
  late String _selectedKind;
  /// 演员榜的类别维度（JavDB App 接口按 有码/无码/欧美/素人 分榜，
  /// 顺序为官方当前时期热门排序，本地仅透传）。
  String _actorCategory = 'censored';

  /// TOP250 切面（官方 App 能力）：类型 + 年份（2009 至今）。
  /// 全部传 null；与演员榜类别行同风格的二级胶囊切换。
  String? _top250Vtype;
  String? _top250Year;

  static const _top250Types = <String?, String>{
    null: '全部',
    'censored': '有码',
    'uncensored': '无码',
    'western': '欧美',
    'fc2': 'FC2',
  };

  /// 年份切面：官方 App 从 2009 起逐年分榜（后端透传 /api/ranking/top250?year=）。
  static List<String> get _top250Years =>
      [for (var y = DateTime.now().year; y >= 2009; y--) '$y'];

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
      final result = await _client.getRanking(
        _isTop250Based ? 'top250' : _selectedKind,
        category: _selectedKind == 'actors' ? _actorCategory : null,
        year: _isTop250Based ? _top250Year : null,
        vtype: _selectedKind == 'top250'
            ? _top250Vtype
            : _top250KindVtypes[_selectedKind],
      );
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

  void _switchActorCategory(String category) {
    if (category == _actorCategory) return;
    setState(() => _actorCategory = category);
    _loadRanking();
  }

  /// 类型/年份两个维度独立切换，互不重置（可组合如：有码 + 2023）。
  void _switchTop250Vtype(String? vtype) {
    if (vtype == _top250Vtype) return;
    setState(() => _top250Vtype = vtype);
    _loadRanking();
  }

  void _switchTop250Year(String? year) {
    if (year == _top250Year) return;
    setState(() => _top250Year = year);
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
          // 右上角：去除看过的开关（默认关，全局持久化）
          const HideWatchedToggle(),
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
          // TOP250 切面（官方 App 能力）：类型行 + 年份行（2009 至今）
          // 专门类型榜（有码/无码/欧美/FC2）类型已锁定，仅展示年份行。
          if (_selectedKind == 'top250')
            _buildTop250FacetRow(
              [
                for (final e in _top250Types.entries) (e.value, e.key),
              ],
              selected: _top250Vtype,
              onSelected: _switchTop250Vtype,
            ),
          if (_isTop250Based)
            _buildTop250FacetRow(
              [
                const ('全部', null),
                for (final y in _top250Years) (y, y),
              ],
              selected: _top250Year,
              onSelected: _switchTop250Year,
            ),
          if (_selectedKind == 'actors')
            SizedBox(
              height: 48,
              child: ListView(
                scrollDirection: Axis.horizontal,
                padding: const EdgeInsets.fromLTRB(12, 0, 12, 8),
                children: [
                  for (final e in const {
                    'censored': '有码',
                    'uncensored': '无码',
                    'western': '欧美',
                    'amateur': '素人',
                  }.entries)
                    Padding(
                      padding: const EdgeInsets.only(right: 8),
                      child: ChoiceChip(
                        label: Text(e.value),
                        selected: e.key == _actorCategory,
                        onSelected: (_) => _switchActorCategory(e.key),
                      ),
                    ),
                ],
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
    // 去除看过的开关开启时按番号过滤（过滤后为空时保留提示友好）。
    final movies =
        context.watch<UserStateProvider>().filterWatchedMovies(_movies);
    if (movies.isEmpty) {
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
        itemCount: movies.length,
        itemBuilder: (context, index) => MovieGridCard(movie: movies[index]),
      );
    }
    return ListView.separated(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 24),
      itemCount: movies.length,
      separatorBuilder: (_, __) => const SizedBox(height: 10),
      itemBuilder: (context, index) => RankingMovieTile(movie: movies[index]),
    );
  }

  /// TOP250 切面胶囊行：value 可为 null（="全部"）。
  Widget _buildTop250FacetRow(
    List<(String, String?)> items, {
    required String? selected,
    required ValueChanged<String?> onSelected,
  }) {
    return SizedBox(
      height: 48,
      child: ListView(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.fromLTRB(12, 0, 12, 8),
        children: [
          for (final (label, value) in items)
            Padding(
              padding: const EdgeInsets.only(right: 8),
              child: ChoiceChip(
                label: Text(label),
                selected: value == selected,
                onSelected: (_) => onSelected(value),
              ),
            ),
        ],
      ),
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
