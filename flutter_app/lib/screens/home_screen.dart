import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../providers/auth_provider.dart';
import '../providers/subscription_provider.dart';
import '../providers/theme_provider.dart';
import '../services/backend_launcher.dart';
import '../services/image_url.dart';
import '../services/logger.dart';
import 'collection_screen.dart';
import 'genre_screen.dart';
import 'list_detail_screen.dart';
import 'log_screen.dart';
import 'movie_detail_screen.dart';
import 'ranking_screen.dart';
import 'search_screen.dart';

/// 首页：影视风布局，背景跟随全局主题（默认白色）。
/// 区块：热门推荐（订阅合集 + TOP250 随机池）、排行榜（热播/Top250/演员）、
/// 题材分类（tag 分组浏览）、合集入口与已订阅合集（实时更新）。
class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  /// TOP250 合集切面（与合集页一致）：热门推荐从其中随机取样。
  static const _top250Facets = <(String?, String?)>[
    (null, null), // 总榜
    ('2025', null),
    ('2024', null),
    ('2023', null),
    ('2022', null),
    ('2021', null),
    (null, 'censored'),
    (null, 'uncensored'),
    (null, 'western'),
    (null, 'fc2'),
  ];

  late final JavDBClient _client;
  late final PageController _bannerController;
  List<Movie> _recMovies = [];
  List<Map<String, dynamic>> _genreGroups = [];
  int _genreGroupIndex = 0;
  String? _genreError;
  bool _loading = true;
  String? _error;
  int _bannerPage = 0;

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    _bannerController = PageController(viewportFraction: 0.88);
    _loadHome();
  }

  @override
  void dispose() {
    _bannerController.dispose();
    super.dispose();
  }

  /// 卡片底色：浅色模式浅灰、深色模式深灰，其余颜色跟随主题。
  Color get _cardColor {
    final brightness = Theme.of(context).colorScheme.brightness;
    return brightness == Brightness.dark
        ? const Color(0xFF1C2027)
        : const Color(0xFFF2F4F8);
  }

  Future<void> _loadHome() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      // 题材分组并行拉取但不阻塞主体：失败时仅降级题材区块。
      _loadGenres();
      final recMovies = await _recommendFromPool();
      if (!mounted) return;
      setState(() {
        _recMovies = recMovies;
        _loading = false;
      });
      AppLogger.info('Home loaded: ${recMovies.length} recommended');
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _loading = false;
      });
      AppLogger.error('Failed to load home data', e);
    }
  }

  /// 题材分组（角色/主題/服裝…）：mobile API 匿名可拉；
  /// 只保留有 web_group_id 映射的组，浏览具体题材影片才需要网页 Cookie。
  void _loadGenres() {
    _client.getTags().then((data) {
      if (!mounted) return;
      final groups = (data['tags'] as List?)
              ?.map((e) => e as Map<String, dynamic>)
              .where((g) =>
                  ((g['web_group_id'] as String?) ?? '').isNotEmpty &&
                  ((g['options'] as List?)?.isNotEmpty ?? false))
              .toList() ??
          const <Map<String, dynamic>>[];
      setState(() {
        _genreGroups = groups;
        _genreGroupIndex = 0;
        _genreError = groups.isEmpty ? '暂无题材数据' : null;
      });
    }).catchError((Object e) {
      AppLogger.warning('Failed to load tags: $e');
      if (mounted) setState(() => _genreError = e.toString());
    });
  }

  /// 热门推荐池：随机 1 个订阅合集 + 1 个 TOP250 切面（无订阅时取
  /// 2 个 TOP250 切面），合并去重洗牌，每次进入首页/下拉刷新都不同。
  /// TOP250 需要登录，失败时回退热播榜，保证首页总有内容。
  Future<List<Movie>> _recommendFromPool() async {
    final provider = context.read<SubscriptionProvider>();
    await provider.ensureLoaded();
    final subs = [...provider.subscriptions]..shuffle();
    final facets = [..._top250Facets]..shuffle();
    try {
      final requests = <Future<Map<String, dynamic>>>[
        if (subs.isNotEmpty) ...[
          _subMovies((subs.first['id'] as String?) ?? ''),
          _client.getRanking('top250',
              year: facets[0].$1, vtype: facets[0].$2, limit: 20),
        ] else
          for (final (year, vtype) in facets.take(2))
            _client.getRanking('top250', year: year, vtype: vtype, limit: 20),
      ];
      final results = await Future.wait(requests);
      final pool = <String, Movie>{};
      for (final r in results) {
        final ms = (r['movies'] as List?)
                ?.map((m) => Movie.fromJson(m as Map<String, dynamic>)) ??
            const <Movie>[];
        for (final m in ms) {
          pool[m.id] = m;
        }
      }
      if (pool.isEmpty) {
        throw Exception('empty recommend pool');
      }
      return pool.values.toList()..shuffle();
    } catch (e) {
      AppLogger.warning(
          'Random pool recommendation failed, fallback to playback: $e');
      final r = await _client.getRanking('playback');
      return (r['movies'] as List?)
              ?.map((m) => Movie.fromJson(m as Map<String, dynamic>))
              .toList() ??
          const <Movie>[];
    }
  }

  /// 单个订阅合集的影片：失败返回空结构，不影响 TOP250 部分。
  Future<Map<String, dynamic>> _subMovies(String listId) async {
    try {
      return await _client.getListMovies(listId, limit: 20);
    } catch (e) {
      AppLogger.warning('Failed to load subscribed list $listId: $e');
      return const {'movies': []};
    }
  }

  void _push(BuildContext context, Widget screen) {
    if (!BackendLauncher.isInitialized) {
      AppLogger.error('Backend not initialized, cannot navigate');
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('后端服务未启动，部分功能不可用'),
          backgroundColor: Colors.red,
        ),
      );
      return;
    }
    Navigator.push(context, MaterialPageRoute(builder: (context) => screen));
  }

  void _openMovie(Movie movie) => _push(
      context, MovieDetailScreen(movieId: movie.id, movieNumber: movie.number));

  /// 深色模式三态切换菜单：跟随系统 / 浅色 / 深色。
  Widget _buildThemeMenu(BuildContext context) {
    final mode = context.watch<ThemeProvider>().mode;
    return PopupMenuButton<ThemeMode>(
      icon: const Icon(Icons.brightness_6_outlined),
      tooltip: '深色模式',
      onSelected: (m) => context.read<ThemeProvider>().setMode(m),
      itemBuilder: (_) => const [
        (ThemeMode.system, '跟随系统', Icons.brightness_auto),
        (ThemeMode.light, '浅色模式', Icons.light_mode),
        (ThemeMode.dark, '深色模式', Icons.dark_mode),
      ].map((e) {
        final (m, label, icon) = e;
        return PopupMenuItem(
          value: m,
          child: Row(
            children: [
              Icon(icon, size: 18),
              const SizedBox(width: 10),
              Expanded(child: Text(label)),
              if (mode == m) const Icon(Icons.check, size: 18),
            ],
          ),
        );
      }).toList(),
    );
  }

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthProvider>();
    return Scaffold(
      appBar: AppBar(
        elevation: 0,
        title: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.local_movies,
                color: Theme.of(context).colorScheme.primary),
            const SizedBox(width: 8),
            const Text('JavDB',
                style: TextStyle(fontWeight: FontWeight.w800, fontSize: 20)),
          ],
        ),
        actions: [
          _buildThemeMenu(context),
          IconButton(
            icon: const Icon(Icons.bug_report),
            tooltip: '日志',
            onPressed: () => _push(context, const LogScreen()),
          ),
          if (auth.isLoggedIn)
            IconButton(
              icon: const Icon(Icons.logout),
              tooltip: '退出登录',
              onPressed: () async {
                await auth.logout();
                if (context.mounted) {
                  ScaffoldMessenger.of(context).showSnackBar(
                    const SnackBar(content: Text('已退出登录')),
                  );
                }
              },
            )
          else
            TextButton.icon(
              onPressed: () => Navigator.of(context).pushNamed('/login'),
              icon: const Icon(Icons.login, size: 18),
              label: const Text('登录'),
            ),
        ],
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : RefreshIndicator(
              onRefresh: _loadHome,
              child: ListView(
                physics: const AlwaysScrollableScrollPhysics(),
                padding: const EdgeInsets.only(bottom: 32),
                children: [
                  _buildSearchBar(context),
                  if (_error != null)
                    Padding(
                      padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
                      child: Text('数据加载失败: $_error（下拉重试）',
                          style: TextStyle(
                              color: Theme.of(context).colorScheme.error,
                              fontSize: 12)),
                    ),
                  _buildSectionHeader(
                      context, '热门推荐', Icons.local_fire_department),
                  _buildBanner(context),
                  _buildSectionHeader(context, '排行榜', Icons.emoji_events),
                  _buildRankingEntries(context),
                  _buildSectionHeader(context, '题材分类', Icons.category),
                  _buildGenreSection(context),
                  _buildSectionHeader(context, '合集', Icons.collections_bookmark),
                  _buildCollectionEntry(context),
                  _buildSubscribedLists(context),
                ],
              ),
            ),
    );
  }

  // ------------------------------------------------------------------ 区块

  Widget _buildSearchBar(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 4),
      child: InkWell(
        borderRadius: BorderRadius.circular(28),
        onTap: () => _push(context, const SearchScreen()),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
          decoration: BoxDecoration(
            color: _cardColor,
            borderRadius: BorderRadius.circular(28),
            border: Border.all(color: Theme.of(context).dividerColor),
          ),
          child: Row(
            children: [
              Icon(Icons.search, color: Theme.of(context).hintColor),
              const SizedBox(width: 10),
              Text('搜索番号 / 演员 / 系列',
                  style: TextStyle(
                      color: Theme.of(context).hintColor, fontSize: 14)),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildSectionHeader(
      BuildContext context, String title, IconData icon) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 12),
      child: Row(
        children: [
          Icon(icon, color: Theme.of(context).colorScheme.primary, size: 20),
          const SizedBox(width: 6),
          Text(title,
              style: Theme.of(context)
                  .textTheme
                  .titleMedium
                  ?.copyWith(fontWeight: FontWeight.w700)),
        ],
      ),
    );
  }

  // ------------------------------------------------------------------ Banner

  Widget _buildBanner(BuildContext context) {
    final items = _recMovies.take(5).toList();
    if (items.isEmpty) {
      return Container(
        height: 160,
        margin: const EdgeInsets.symmetric(horizontal: 16),
        decoration: BoxDecoration(
          color: _cardColor,
          borderRadius: BorderRadius.circular(16),
        ),
        child: Center(
          child: Text('暂无推荐数据',
              style: TextStyle(color: Theme.of(context).hintColor)),
        ),
      );
    }
    return Column(
      children: [
        SizedBox(
          height: 200,
          child: PageView.builder(
            controller: _bannerController,
            itemCount: items.length,
            onPageChanged: (i) => setState(() => _bannerPage = i),
            itemBuilder: (context, i) {
              final movie = items[i];
              return _buildBannerItem(context, movie);
            },
          ),
        ),
        const SizedBox(height: 10),
        Row(
          mainAxisAlignment: MainAxisAlignment.center,
          children: List.generate(items.length, (i) {
            return AnimatedContainer(
              duration: const Duration(milliseconds: 200),
              width: i == _bannerPage ? 18 : 7,
              height: 7,
              margin: const EdgeInsets.symmetric(horizontal: 3),
              decoration: BoxDecoration(
                color: i == _bannerPage
                    ? Theme.of(context).colorScheme.primary
                    : Theme.of(context).hintColor.withOpacity(0.3),
                borderRadius: BorderRadius.circular(4),
              ),
            );
          }),
        ),
      ],
    );
  }

  Widget _buildBannerItem(BuildContext context, Movie movie) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 6),
      child: InkWell(
        borderRadius: BorderRadius.circular(16),
        onTap: () => _openMovie(movie),
        child: ClipRRect(
          borderRadius: BorderRadius.circular(16),
          child: Stack(
            fit: StackFit.expand,
            children: [
              if (movie.coverUrl != null)
                Image.network(
                  resolveImageUrl(movie.coverUrl!),
                  fit: BoxFit.cover,
                  errorBuilder: (_, __, ___) => Container(
                      color: _cardColor,
                      child: const Icon(Icons.movie, size: 56)),
                )
              else
                Container(
                    color: _cardColor,
                    child: const Icon(Icons.movie, size: 56)),
              // 底部渐变遮罩 + 信息（图片上的文字固定用白色保证可读）
              Positioned(
                left: 0,
                right: 0,
                bottom: 0,
                child: Container(
                  padding: const EdgeInsets.all(14),
                  decoration: const BoxDecoration(
                    gradient: LinearGradient(
                      begin: Alignment.bottomCenter,
                      end: Alignment.topCenter,
                      colors: [Colors.black87, Colors.transparent],
                    ),
                  ),
                  child: Row(
                    children: [
                      Expanded(
                        child: Text(
                          '${movie.number}  ${movie.title}',
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                              fontWeight: FontWeight.w700,
                              fontSize: 15,
                              color: Colors.white),
                        ),
                      ),
                      if (movie.score != null)
                        Container(
                          padding: const EdgeInsets.symmetric(
                              horizontal: 8, vertical: 3),
                          decoration: BoxDecoration(
                            color: Colors.orange,
                            borderRadius: BorderRadius.circular(10),
                          ),
                          child: Text(
                            movie.score.toString(),
                            style: const TextStyle(
                                fontSize: 12,
                                fontWeight: FontWeight.w700,
                                color: Colors.white),
                          ),
                        ),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  // ------------------------------------------------------------------ 榜单入口

  Widget _buildRankingEntries(BuildContext context) {
    final entries = [
      ('热播榜', Icons.whatshot, Color(0xFFFF7043), 'playback'),
      ('Top250', Icons.emoji_events, Color(0xFFFFD54F), 'top250'),
      ('演员榜', Icons.face_retouching_natural, Color(0xFFE8506E), 'actors'),
    ];
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12),
      child: Row(
        children: entries.map((e) {
          final (label, icon, color, kind) = e;
          return Expanded(
            child: InkWell(
              borderRadius: BorderRadius.circular(14),
              onTap: () => _push(context, RankingScreen(initialKind: kind)),
              child: Padding(
                padding: const EdgeInsets.symmetric(vertical: 8),
                child: Column(
                  children: [
                    Container(
                      width: 52,
                      height: 52,
                      decoration: BoxDecoration(
                        color: color.withOpacity(0.15),
                        borderRadius: BorderRadius.circular(16),
                      ),
                      child: Icon(icon, color: color, size: 28),
                    ),
                    const SizedBox(height: 8),
                    Text(label, style: const TextStyle(fontSize: 13)),
                  ],
                ),
              ),
            ),
          );
        }).toList(),
      ),
    );
  }

  // ------------------------------------------------------------------ 合集入口

  /// 合集入口卡片：进入合集页（TOP250 总榜/年度榜/类型榜）。
  Widget _buildCollectionEntry(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: InkWell(
        borderRadius: BorderRadius.circular(14),
        onTap: () => _push(context, const CollectionScreen()),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
          decoration: BoxDecoration(
            color: _cardColor,
            borderRadius: BorderRadius.circular(14),
            border: Border.all(color: Theme.of(context).dividerColor),
          ),
          child: Row(
            children: [
              Container(
                width: 44,
                height: 44,
                decoration: BoxDecoration(
                  color: const Color(0xFFFFD54F).withOpacity(0.15),
                  borderRadius: BorderRadius.circular(12),
                ),
                child: const Icon(Icons.collections_bookmark,
                    color: Color(0xFFFFD54F), size: 24),
              ),
              const SizedBox(width: 12),
              const Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('TOP250 合集',
                        style: TextStyle(
                            fontSize: 15, fontWeight: FontWeight.w600)),
                    SizedBox(height: 2),
                    Text('总榜 · 年度榜 · 有码 / 无码 / 欧美 / FC2',
                        style: TextStyle(fontSize: 12, color: Colors.grey)),
                  ],
                ),
              ),
              Icon(Icons.chevron_right, color: Theme.of(context).hintColor),
            ],
          ),
        ),
      ),
    );
  }

  // ------------------------------------------------------------- 已订阅合集

  /// 已订阅合集区块：数据来自 SubscriptionProvider（订阅/取消后实时更新），
  /// 无订阅时隐藏。
  Widget _buildSubscribedLists(BuildContext context) {
    final subs = context.watch<SubscriptionProvider>().subscriptions;
    if (subs.isEmpty) return const SizedBox.shrink();
    return Column(
      children: [
        _buildSectionHeader(context, '已订阅合集', Icons.bookmarks),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
          child: Column(
            children: [
              for (final s in subs) _buildSubscriptionCard(context, s),
            ],
          ),
        ),
      ],
    );
  }

  Widget _buildSubscriptionCard(BuildContext context, Map<String, dynamic> s) {
    final id = (s['id'] as String?) ?? '';
    final name = (s['name'] as String?) ?? id;
    final count = (s['movies_count'] as num?)?.toInt() ?? 0;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: InkWell(
        borderRadius: BorderRadius.circular(14),
        onTap: () => _push(
          context,
          ListDetailScreen(
              listId: id, listName: name, moviesCount: count),
        ),
        child: Container(
          padding:
              const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
          decoration: BoxDecoration(
            color: _cardColor,
            borderRadius: BorderRadius.circular(14),
            border: Border.all(color: Theme.of(context).dividerColor),
          ),
          child: Row(
            children: [
              Container(
                width: 38,
                height: 38,
                decoration: BoxDecoration(
                  color: const Color(0xFFFFD54F).withOpacity(0.15),
                  borderRadius: BorderRadius.circular(10),
                ),
                child: const Icon(Icons.collections_bookmark,
                    color: Color(0xFFFFD54F), size: 20),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Text(name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                        fontSize: 14, fontWeight: FontWeight.w600)),
              ),
              Text('$count 部',
                  style: TextStyle(
                      fontSize: 12, color: Theme.of(context).hintColor)),
              const SizedBox(width: 4),
              Icon(Icons.chevron_right, color: Theme.of(context).hintColor),
            ],
          ),
        ),
      ),
    );
  }

  // ------------------------------------------------------------------ 题材分类

  /// 题材分类区块：横向组切换（角色/主題/服裝…）+ 下方 tag 标签云，
  /// 点击 tag 进入题材影片页；需要网页版 Cookie 时降级为导入提示卡片。
  Widget _buildGenreSection(BuildContext context) {
    if (_genreError != null) {
      return Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16),
        child: InkWell(
          borderRadius: BorderRadius.circular(14),
          onTap: () => showWebCookieImportDialog(context, _client),
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
            decoration: BoxDecoration(
              color: _cardColor,
              borderRadius: BorderRadius.circular(14),
              border: Border.all(color: Theme.of(context).dividerColor),
            ),
            child: Row(
              children: [
                const Icon(Icons.lock_outline, size: 20),
                const SizedBox(width: 10),
                Expanded(
                  child: Text('题材分类需要网页版登录态，点击导入 Cookie 解锁',
                      style: TextStyle(
                          fontSize: 13,
                          color: Theme.of(context).hintColor)),
                ),
                Icon(Icons.chevron_right, color: Theme.of(context).hintColor),
              ],
            ),
          ),
        ),
      );
    }
    if (_genreGroups.isEmpty) return const SizedBox.shrink();
    final index =
        _genreGroupIndex < _genreGroups.length ? _genreGroupIndex : 0;
    final group = _genreGroups[index];
    final options = (group['options'] as List?) ?? const [];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(
          height: 44,
          child: ListView.separated(
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.symmetric(horizontal: 16),
            itemCount: _genreGroups.length,
            separatorBuilder: (_, __) => const SizedBox(width: 8),
            itemBuilder: (context, i) {
              final g = _genreGroups[i];
              return Center(
                child: ChoiceChip(
                  label: Text((g['name'] as String?) ?? '',
                      style: const TextStyle(fontSize: 12)),
                  labelPadding:
                      const EdgeInsets.symmetric(horizontal: 10),
                  selected: i == index,
                  visualDensity: VisualDensity.compact,
                  onSelected: (_) => setState(() => _genreGroupIndex = i),
                ),
              );
            },
          ),
        ),
        const SizedBox(height: 4),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
          child: Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final o in options)
                ActionChip(
                  visualDensity: VisualDensity.compact,
                  label: Text(
                      (o as Map<String, dynamic>)['name'] as String? ?? '',
                      style: const TextStyle(fontSize: 12)),
                  onPressed: () => _push(
                    context,
                    GenreScreen(
                      groupId: (group['web_group_id'] as String?) ?? '',
                      tagId: (o['id'] as String?) ?? '',
                      title: (o['name'] as String?) ?? '',
                    ),
                  ),
                ),
            ],
          ),
        ),
      ],
    );
  }
}
