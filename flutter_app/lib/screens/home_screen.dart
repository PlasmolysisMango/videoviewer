import 'dart:async';
import 'dart:math';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../providers/auth_provider.dart';
import '../providers/subscription_provider.dart';
import '../providers/theme_provider.dart';
import '../services/backend_launcher.dart';
import '../services/data_cache.dart';
import '../services/image_url.dart';
import '../services/logger.dart';
import '../widgets/common_ui.dart';
import 'actor_catalog_screen.dart';
import 'collection_screen.dart';
import 'favorites_screen.dart';
import 'genre_catalog_screen.dart';
import 'genre_screen.dart';
import 'history_screen.dart';
import 'list_detail_screen.dart';
import 'log_screen.dart';
import 'movie_detail_screen.dart';
import 'ranking_screen.dart';
import 'search_screen.dart';
import 'settings_screen.dart';
import 'user_lists_screen.dart';
import 'watched_screen.dart';

/// 首页：现代流媒体风布局，背景跟随全局主题。
/// 区块：为你推荐（订阅合集 + TOP250 随机池）、榜单入口、
/// 合集/题材/演员三分栏——仅展示订阅内容，右上角灰色图钉标记订阅项。
class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

/// 订阅标记统一用低饱和灰，不做醒目强调。
const _subMarkColor = Color(0xFF9AA3AD);

class _HomeScreenState extends State<HomeScreen> {
  /// 随机抽取的年度切面数：底池 = 总榜 + 2009-2026 随机 4 年，
  /// 每次进入/刷新换一批年份，覆盖面随使用逐渐铺开。
  static const _top250RandomYears = 4;

  late final JavDBClient _client;
  late final PageController _bannerController;
  final _random = Random();
  List<Movie> _recMovies = [];
  bool _loading = true;
  String? _error;
  int _bannerPage = 0;

  /// TOP250 切面失败冷却：失败后 10 分钟内不再重试。未登录/app_token
  /// 过期时这些切面双端都注定失败，避免每次进首页都白等全部切面。
  static final Map<String, DateTime> _facetCooldown = {};
  static const _facetCooldownFor = Duration(minutes: 10);

  /// 推荐池持久缓存 key：重进首页/冷启动先展示上次结果再后台刷新。
  static const _poolCacheKey = 'home.pool.v1';

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    _bannerController = PageController(viewportFraction: 0.88);
    _restoreCachedPool().then((hasCache) {
      // 有缓存：只展示缓存，进首页不重新推荐（下拉刷新才换一批）；
      // 无缓存（首次/缓存过期）：走正常加载（全屏 loading）。
      if (!hasCache) _loadHome();
    });
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

  /// 恢复上次成功的推荐池缓存；返回是否命中。
  Future<bool> _restoreCachedPool() async {
    try {
      final data = await DataCache.instance
          .read(_poolCacheKey, maxAge: const Duration(days: 7));
      final list = (data as List?)
              ?.map((e) => Movie.fromJson((e as Map).cast<String, dynamic>()))
              .toList() ??
          const <Movie>[];
      if (list.isNotEmpty && mounted) {
        setState(() {
          _recMovies = list;
          _loading = false;
        });
        return true;
      }
    } catch (e) {
      AppLogger.warning('Restore home pool cache failed: $e');
    }
    return false;
  }

  /// [showLoading]：全屏 loading（进首页无缓存时）；下拉刷新传 false，
  /// 保留当前内容后台替换，避免列表被 loading 圈打断。
  Future<void> _loadHome({bool showLoading = true}) async {
    if (showLoading) {
      setState(() {
        _loading = true;
        _error = null;
      });
    } else {
      setState(() => _error = null);
    }
    try {
      final recMovies = await _recommendFromPool();
      if (!mounted) return;
      setState(() {
        _recMovies = recMovies;
        _loading = false;
      });
      unawaited(DataCache.instance
          .write(_poolCacheKey, recMovies.map((m) => m.toJson()).toList()));
      AppLogger.info('Home loaded: ${recMovies.length} recommended');
    } catch (e) {
      if (!mounted) return;
      setState(() {
        // 已有展示内容（缓存/上次成功）时刷新失败不打扰用户
        if (_recMovies.isEmpty) {
          _error = e.toString();
        }
        _loading = false;
      });
      AppLogger.error('Failed to load home data', e);
    }
  }

  /// 热门推荐池：TOP250 底池（总榜 + 2009-2026 随机 4 个年度切面）
  /// 叠加全部订阅（合集/题材/演员）的影片，合并去重洗牌，每次下拉
  /// 刷新换一批（进首页只展示缓存，不重新拉）。各源独立容错：
  /// 单个失败只缩池；全空时回退热播榜，保证首页总有内容。
  Future<List<Movie>> _recommendFromPool() async {
    final provider = context.read<SubscriptionProvider>();
    await provider.ensureLoaded();
    final requests = <Future<Map<String, dynamic>>>[
      // 底池：总榜 + 随机 4 个年度切面（失败冷却中的年份本轮跳过）
      if (!_top250OnCooldown('total'))
        _safeMovies(
          () => _client.getRanking('top250', limit: 20),
          cooldownKey: 'top250:total',
        ),
      for (final y in _pickRandomYears())
        if (!_top250OnCooldown(y))
          _safeMovies(
            () => _client.getRanking('top250', year: y, limit: 20),
            cooldownKey: 'top250:y$y',
          ),
      // 订阅合集
      for (final s in provider.byKind(kSubCollection))
        _safeMovies(() =>
            _client.getListMovies((s['id'] as String?) ?? '', limit: 20)),
      // 订阅题材（group 为 web 筛选组编号，id 为 tag ID）
      for (final s in provider.byKind(kSubGenre))
        _safeMovies(() => _client.getGenreMovies(
            (s['group'] as String?) ?? '', (s['id'] as String?) ?? '',
            limit: 20)),
      // 订阅演员
      for (final s in provider.byKind(kSubActor))
        _safeMovies(
            () => _client.actorMovies((s['id'] as String?) ?? '', limit: 20)),
    ];
    try {
      final results = await _runBatched(requests);
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
        // 冷缓存下全部切面同时请求可能被上游限流而全空；
        // 先试一次总榜单（单请求不易触发限流），仍失败才降级热播榜。
        AppLogger.warning('Random pool empty, retry top250 total list');
        final r = await _safeMovies(
            () => _client.getRanking('top250', limit: 40));
        final ms = (r['movies'] as List?)
                ?.map((m) => Movie.fromJson(m as Map<String, dynamic>))
                .toList() ??
            const <Movie>[];
        if (ms.isEmpty) {
          throw Exception('empty recommend pool');
        }
        return [...ms]..shuffle();
      }
      return pool.values.toList()..shuffle();
    } catch (e) {
      AppLogger.warning(
          'Random pool recommendation failed, fallback to playback: $e');
      try {
        final r = await _client.getRanking('playback');
        final ms = (r['movies'] as List?)
                ?.map((m) => Movie.fromJson(m as Map<String, dynamic>))
                .toList() ??
            const <Movie>[];
        return [...ms]..shuffle();
      } catch (e2) {
        AppLogger.error('Playback fallback failed', e2);
        rethrow;
      }
    }
  }

  /// 分批并发执行推荐源请求：冷缓存下十几路同时打向上游会触发
  /// 限流（表现为切面全空、推荐池被迫降级到热播榜），这里限制
  /// 并发度并在批间稍作间隔；缓存变热后各请求毫秒级返回，总体无感。
  Future<List<Map<String, dynamic>>> _runBatched(
    List<Future<Map<String, dynamic>>> jobs, {
    int size = 3,
    int gapMs = 300,
  }) async {
    final out = <Map<String, dynamic>>[];
    for (var i = 0; i < jobs.length; i += size) {
      final end = (i + size).clamp(0, jobs.length);
      out.addAll(await Future.wait(jobs.sublist(i, end)));
      if (end < jobs.length) {
        await Future<void>.delayed(Duration(milliseconds: gapMs));
      }
    }
    return out;
  }

  /// 单个推荐源的容错包装：失败返回空结构，只缩池不影响其余来源。
  /// cooldownKey 非空时记录失败时间（冷却期内调用方跳过该源）。
  Future<Map<String, dynamic>> _safeMovies(
      Future<Map<String, dynamic>> Function() fetch,
      {String? cooldownKey}) async {
    try {
      final r = await fetch();
      if (cooldownKey != null) _facetCooldown.remove(cooldownKey);
      return r;
    } catch (e) {
      AppLogger.warning('Recommend source failed: $e');
      if (cooldownKey != null) {
        _facetCooldown[cooldownKey] = DateTime.now();
      }
      return const {'movies': []};
    }
  }

  /// 从 2009-2026 随机抽 4 个年度切面（降序年份池）。
  List<String> _pickRandomYears() {
    final years = [for (var y = 2026; y >= 2009; y--) '$y']..shuffle(_random);
    return years.take(_top250RandomYears).toList();
  }

  /// 该 TOP250 切面是否处于失败冷却期（key：total / y{year}）。
  bool _top250OnCooldown(String facetKey) {
    final failedAt = _facetCooldown['top250:$facetKey'];
    return failedAt != null &&
        DateTime.now().difference(failedAt) < _facetCooldownFor;
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

  /// 侧边栏：收纳较复杂的入口（收藏夹/历史/清单/设置/日志/账号）。
  /// AppBar 仅保留深色模式等轻量操作，保持顶栏简洁。
  Widget _buildDrawer(BuildContext context, AuthProvider auth) {
    return Drawer(
      child: SafeArea(
        child: ListView(
          padding: EdgeInsets.zero,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 20, 20, 12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Icon(Icons.local_movies,
                          color: Theme.of(context).colorScheme.primary),
                      const SizedBox(width: 8),
                      const Text('JavDB',
                          style: TextStyle(
                              fontWeight: FontWeight.w800, fontSize: 20)),
                    ],
                  ),
                  const SizedBox(height: 10),
                  Text(
                    auth.isLoggedIn ? '已登录：${auth.username ?? ''}' : '未登录',
                    style: TextStyle(
                        fontSize: 13, color: Theme.of(context).hintColor),
                  ),
                ],
              ),
            ),
            const Divider(height: 1),
            _buildDrawerItem(context, Icons.favorite_border, '收藏夹（想看）',
                () => const FavoritesScreen()),
            _buildDrawerItem(context, Icons.done_all, '看过',
                () => const WatchedScreen()),
            _buildDrawerItem(context, Icons.history, '历史记录',
                () => const HistoryScreen()),
            _buildDrawerItem(context, Icons.playlist_add_check, '我的清单',
                () => const UserListsScreen()),
            _buildDrawerItem(context, Icons.settings_outlined, '设置',
                () => const SettingsScreen()),
            _buildDrawerItem(
                context, Icons.bug_report, '日志', () => const LogScreen()),
            const Divider(height: 1),
            if (auth.isLoggedIn)
              ListTile(
                leading: const Icon(Icons.logout),
                title: const Text('退出登录',
                    style: TextStyle(color: Colors.redAccent)),
                onTap: () async {
                  Navigator.pop(context); // 先关闭侧边栏
                  await auth.logout();
                  if (context.mounted) {
                    ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(content: Text('已退出登录')),
                    );
                  }
                },
              )
            else
              ListTile(
                leading: const Icon(Icons.login),
                title: const Text('登录'),
                onTap: () {
                  Navigator.pop(context);
                  Navigator.of(context).pushNamed('/login');
                },
              ),
          ],
        ),
      ),
    );
  }

  /// 侧边栏入口项：先关闭侧边栏再跳转目标页。
  Widget _buildDrawerItem(
    BuildContext context,
    IconData icon,
    String label,
    Widget Function() screenBuilder,
  ) {
    return ListTile(
      leading: Icon(icon),
      title: Text(label),
      onTap: () {
        Navigator.pop(context); // 关闭侧边栏
        _push(context, screenBuilder());
      },
    );
  }

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthProvider>();
    return Scaffold(
      drawer: _buildDrawer(context, auth),
      appBar: AppBar(
        elevation: 0,
        // AppBar 仅保留深色模式；收藏/历史/清单/设置/日志/账号
        // 等入口统一收纳进侧边栏（自动出现的汉堡按钮）。
        actions: [_buildThemeMenu(context)],
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
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : RefreshIndicator(
              // 下拉刷新才重新推荐（重新随机年份+拉池），保留内容原位替换
              onRefresh: () => _loadHome(showLoading: false),
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
                      context, '为你推荐', Icons.local_fire_department),
                  _buildBanner(context),
                  _buildSectionHeader(context, '榜单', Icons.emoji_events),
                  _buildRankingEntries(context),
                  _buildSectionHeader(context, '合集', Icons.collections_bookmark),
                  _buildCollectionEntry(context),
                  _buildSubscribedCollectionCards(context),
                  _buildSectionHeader(context, '题材', Icons.category),
                  _buildGenreSection(context),
                  _buildSectionHeader(context, '演员', Icons.face_retouching_natural),
                  _buildActorSection(context),
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

  /// 榜单入口：三张紧凑卡片（着色图标 + 名称），现代扁平风。
  Widget _buildRankingEntries(BuildContext context) {
    const entries = [
      ('热播榜', Icons.whatshot, Color(0xFFFF7043), 'playback'),
      ('Top250', Icons.emoji_events, Color(0xFFFFD54F), 'top250'),
      ('演员榜', Icons.face_retouching_natural, Color(0xFFE8506E), 'actors'),
    ];
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Row(
        children: [
          for (var i = 0; i < entries.length; i++)
            Expanded(
              child: Container(
                margin: EdgeInsets.only(left: i == 0 ? 0 : 8),
                decoration: BoxDecoration(
                  color: _cardColor,
                  borderRadius: BorderRadius.circular(14),
                  border: Border.all(color: Theme.of(context).dividerColor),
                ),
                child: InkWell(
                  borderRadius: BorderRadius.circular(14),
                  onTap: () =>
                      _push(context, RankingScreen(initialKind: entries[i].$4)),
                  child: Padding(
                    padding: const EdgeInsets.symmetric(vertical: 12),
                    child: Column(
                      children: [
                        Icon(entries[i].$2, color: entries[i].$3, size: 26),
                        const SizedBox(height: 6),
                        Text(entries[i].$1,
                            style: const TextStyle(fontSize: 12)),
                      ],
                    ),
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }

  // ------------------------------------------------------------------ 合集入口

  /// 合集入口卡片：进入合集页（社区合集目录：搜索 + 已订阅，二级进详情）。
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
                    Text('合集',
                        style: TextStyle(
                            fontSize: 15, fontWeight: FontWeight.w600)),
                    SizedBox(height: 2),
                    Text('社区影单 · 搜索与订阅',
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

  // ---------------------------------------------------------------- 分栏公共

  /// 分栏入口卡片（自带项，无图钉）：跳转对应的浏览/订阅管理页。
  /// 图标规则（同官方 App）：榜单/合集入口带图标；题材/演员入口不带
  /// （[showIcon] 传 false 时隐藏左侧图标块）。
  Widget _buildSectionEntryCard(
    BuildContext context, {
    required IconData icon,
    required Color iconColor,
    required String title,
    required String subtitle,
    required VoidCallback onTap,
    bool showIcon = true,
  }) {
    return InkWell(
      borderRadius: BorderRadius.circular(14),
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        decoration: BoxDecoration(
          color: _cardColor,
          borderRadius: BorderRadius.circular(14),
          border: Border.all(color: Theme.of(context).dividerColor),
        ),
        child: Row(
          children: [
            if (showIcon) ...[
              Container(
                width: 44,
                height: 44,
                decoration: BoxDecoration(
                  color: iconColor.withOpacity(0.15),
                  borderRadius: BorderRadius.circular(12),
                ),
                child: Icon(icon, color: iconColor, size: 24),
              ),
              const SizedBox(width: 12),
            ],
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(title,
                      style: const TextStyle(
                          fontSize: 15, fontWeight: FontWeight.w600)),
                  const SizedBox(height: 2),
                  Text(subtitle,
                      style:
                          const TextStyle(fontSize: 12, color: Colors.grey)),
                ],
              ),
            ),
            Icon(Icons.chevron_right, color: Theme.of(context).hintColor),
          ],
        ),
      ),
    );
  }

  // ------------------------------------------------------------------ 合集栏

  /// 已订阅合集卡片列：与自带 TOP250 入口同栏，图钉角标标记订阅项。
  Widget _buildSubscribedCollectionCards(BuildContext context) {
    final subs = context.watch<SubscriptionProvider>().byKind(kSubCollection);
    if (subs.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Column(
        children: [
          for (final s in subs) _buildSubscriptionCard(context, s),
        ],
      ),
    );
  }

  Widget _buildSubscriptionCard(BuildContext context, Map<String, dynamic> s) {
    final id = (s['id'] as String?) ?? '';
    final name = (s['name'] as String?) ?? id;
    final count = (s['movies_count'] as num?)?.toInt() ?? 0;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Stack(
        children: [
          InkWell(
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
          // 图钉：标记订阅项（灰色低饱和，不做醒目强调）
          const Positioned(
            top: 2,
            right: 4,
            child: Icon(Icons.push_pin, size: 14, color: _subMarkColor),
          ),
        ],
      ),
    );
  }

  // ------------------------------------------------------------------ 题材栏

  /// 题材栏：入口卡片（题材大类页）+ 已订阅题材 chip（灰色图钉标记）。
  Widget _buildGenreSection(BuildContext context) {
    final subs = context.watch<SubscriptionProvider>().byKind(kSubGenre);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _buildSectionEntryCard(
            context,
            icon: Icons.category,
            iconColor: const Color(0xFF4FC3F7),
            title: '浏览全部题材',
            subtitle: '角色 · 主题 · 服装 · 行为…',
            showIcon: false,
            onTap: () => _push(context, const GenreCatalogScreen()),
          ),
          if (subs.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 10),
              child: Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  for (final s in subs) _buildGenreChip(context, s),
                ],
              ),
            ),
        ],
      ),
    );
  }

  /// 已订阅题材 chip：灰图钉 + 题材名，点击进题材影片页。
  Widget _buildGenreChip(BuildContext context, Map<String, dynamic> s) {
    final tagId = (s['id'] as String?) ?? '';
    final name = (s['name'] as String?) ?? tagId;
    final group = (s['group'] as String?) ?? '';
    return InkWell(
      borderRadius: BorderRadius.circular(20),
      onTap: () => _push(
        context,
        GenreScreen(groupId: group, tagId: tagId, title: name),
      ),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        decoration: BoxDecoration(
          color: _cardColor,
          borderRadius: BorderRadius.circular(20),
          border: Border.all(color: Theme.of(context).dividerColor),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.push_pin, size: 12, color: _subMarkColor),
            const SizedBox(width: 5),
            Text(name, style: const TextStyle(fontSize: 13)),
          ],
        ),
      ),
    );
  }

  // ------------------------------------------------------------------ 演员栏

  /// 演员栏：入口卡片（演员页）+ 已订阅演员头像轨道（灰图钉标记）。
  /// 不展示默认演员——订阅后才会出现在首页。
  Widget _buildActorSection(BuildContext context) {
    final subs = context.watch<SubscriptionProvider>().byKind(kSubActor);
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
          child: _buildSectionEntryCard(
            context,
            icon: Icons.face_retouching_natural,
            iconColor: const Color(0xFFE8506E),
            title: '全部演员',
            subtitle: '热门演员 · 演员榜',
            showIcon: false,
            onTap: () => _push(context, const ActorCatalogScreen()),
          ),
        ),
        if (subs.isNotEmpty)
          SizedBox(height: 118, child: _buildActorRail(context)),
      ],
    );
  }

  Widget _buildActorRail(BuildContext context) {
    final subs = context.watch<SubscriptionProvider>().byKind(kSubActor);
    return ListView.separated(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
      itemCount: subs.length,
      separatorBuilder: (_, __) => const SizedBox(width: 16),
      itemBuilder: (context, i) {
        final s = subs[i];
        final actor = Actor(
          id: (s['id'] as String?) ?? '',
          name: (s['name'] as String?) ?? '',
          avatarUrl: (s['avatar'] as String?),
        );
        return _buildActorAvatar(context, actor);
      },
    );
  }

  Widget _buildActorAvatar(BuildContext context, Actor actor) {
    return InkWell(
      borderRadius: BorderRadius.circular(12),
      onTap: () {
        if (actor.id.isNotEmpty) {
          pushActorScreen(context, actor);
        } else {
          _push(context, SearchScreen(initialQuery: actor.name));
        }
      },
      child: SizedBox(
        width: 72,
        child: Column(
          children: [
            Stack(
              clipBehavior: Clip.none,
              children: [
                CircleAvatar(
                  radius: 32,
                  backgroundColor: _cardColor,
                  backgroundImage: actor.avatarUrl != null
                      ? NetworkImage(resolveImageUrl(actor.avatarUrl!))
                      : null,
                  onBackgroundImageError:
                      actor.avatarUrl != null ? (_, __) {} : null,
                  child: actor.avatarUrl == null
                      ? const Icon(Icons.person, size: 32)
                      : null,
                ),
                // 灰色图钉：标记订阅演员
                Positioned(
                  top: -2,
                  right: -2,
                  child: Container(
                    padding: const EdgeInsets.all(3),
                    decoration: BoxDecoration(
                      color: Theme.of(context).cardColor,
                      shape: BoxShape.circle,
                      border: Border.all(color: Theme.of(context).dividerColor),
                    ),
                    child: const Icon(Icons.push_pin,
                        size: 10, color: _subMarkColor),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 6),
            Text(
              actor.name,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}
