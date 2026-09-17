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
import '../widgets/common_ui.dart';
import 'actor_catalog_screen.dart';
import 'collection_screen.dart';
import 'genre_catalog_screen.dart';
import 'genre_screen.dart';
import 'list_detail_screen.dart';
import 'log_screen.dart';
import 'movie_detail_screen.dart';
import 'ranking_screen.dart';
import 'search_screen.dart';

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

  /// 热门推荐池：随机 1 个订阅合集 + 1 个 TOP250 切面（无订阅时取
  /// 2 个 TOP250 切面），合并去重洗牌，每次进入首页/下拉刷新都不同。
  /// TOP250 需要登录，失败时回退热播榜，保证首页总有内容。
  Future<List<Movie>> _recommendFromPool() async {
    final provider = context.read<SubscriptionProvider>();
    await provider.ensureLoaded();
    final subs = [...provider.byKind(kSubCollection)]..shuffle();
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
                    Text('总榜 · 年度榜 · 类型切面',
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
  Widget _buildSectionEntryCard(
    BuildContext context, {
    required IconData icon,
    required Color iconColor,
    required String title,
    required String subtitle,
    required VoidCallback onTap,
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
