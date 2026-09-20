import 'package:flutter/material.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../services/backend_launcher.dart';
import '../services/logger.dart';
import '../widgets/common_ui.dart';
import 'list_search_screen.dart';

/// 合集定义：一个 TOP250 榜单切面（年份或类型）。
class CollectionSpec {
  final String label;
  final String? year;
  final String? vtype;

  const CollectionSpec(this.label, {this.year, this.vtype});
}

/// 合集页：TOP250 系列榜单集合（总榜 / 年度榜 / 类型榜）。
/// 数据走 /api/ranking/top250 的 year/vtype 切面参数。
class CollectionScreen extends StatefulWidget {
  const CollectionScreen({super.key});

  @override
  State<CollectionScreen> createState() => _CollectionScreenState();
}

class _CollectionScreenState extends State<CollectionScreen> {
  /// 合集清单：总榜 + 年度榜（含 2026）+ 四个类型榜。
  static const _collections = [
    CollectionSpec('TOP250'),
    CollectionSpec('2026', year: '2026'),
    CollectionSpec('2025', year: '2025'),
    CollectionSpec('2024', year: '2024'),
    CollectionSpec('2023', year: '2023'),
    CollectionSpec('2022', year: '2022'),
    CollectionSpec('2021', year: '2021'),
    CollectionSpec('有码', vtype: 'censored'),
    CollectionSpec('无码', vtype: 'uncensored'),
    CollectionSpec('欧美', vtype: 'western'),
    CollectionSpec('FC2', vtype: 'fc2'),
  ];

  late final JavDBClient _client;
  List<Movie> _movies = [];
  MovieViewMode _viewMode = MovieViewMode.list;
  bool _isLoading = true;
  String? _error;
  int _selected = 0;

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    _loadCollection();
  }

  CollectionSpec get _current => _collections[_selected];

  Future<void> _loadCollection() async {
    setState(() {
      _isLoading = true;
      _error = null;
    });

    try {
      final spec = _current;
      AppLogger.info('Loading collection: ${spec.label}');
      final result = await _client.getRanking('top250',
          year: spec.year, vtype: spec.vtype, limit: 100);
      final moviesList = (result['movies'] as List?)
              ?.map((m) => Movie.fromJson(m as Map<String, dynamic>))
              .toList() ??
          const <Movie>[];
      setState(() {
        _movies = moviesList;
        _isLoading = false;
      });
      AppLogger.info('Loaded ${moviesList.length} movies in ${spec.label}');
    } catch (e) {
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
      AppLogger.error('Failed to load collection', e);
    }
  }

  void _switchCollection(int index) {
    if (index == _selected) return;
    setState(() => _selected = index);
    _loadCollection();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.collections_bookmark,
                color: Theme.of(context).colorScheme.primary),
            const SizedBox(width: 8),
            Text('合集 · ${_current.label}'),
          ],
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.search),
            tooltip: '搜索合集',
            onPressed: () => Navigator.push(context,
                MaterialPageRoute(builder: (_) => const ListSearchScreen())),
          ),
          ViewModeToggle(
            mode: _viewMode,
            onChanged: (m) => setState(() => _viewMode = m),
          ),
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: '刷新',
            onPressed: _isLoading ? null : _loadCollection,
          ),
        ],
      ),
      body: Column(
        children: [
          // 合集切换（横向滚动胶囊）
          SizedBox(
            height: 52,
            child: ListView(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
              children: [
                for (var i = 0; i < _collections.length; i++)
                  Padding(
                    padding: const EdgeInsets.only(right: 8),
                    child: ChoiceChip(
                      label: Text(_collections[i].label),
                      selected: i == _selected,
                      onSelected: (_) => _switchCollection(i),
                    ),
                  ),
              ],
            ),
          ),
          Expanded(
            child: _isLoading
                ? const Center(child: CircularProgressIndicator())
                : _error != null
                    ? ErrorRetryView(error: _error!, onRetry: _loadCollection)
                    : RefreshIndicator(
                        onRefresh: _loadCollection,
                        child: _buildMovieList(context),
                      ),
          ),
        ],
      ),
    );
  }

  Widget _buildMovieList(BuildContext context) {
    if (_movies.isEmpty) {
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
}
