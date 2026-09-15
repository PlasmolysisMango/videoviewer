import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../providers/auth_provider.dart';
import '../providers/theme_provider.dart';
import '../services/backend_launcher.dart';
import '../services/image_url.dart';
import '../services/logger.dart';
import 'log_screen.dart';
import 'movie_detail_screen.dart';
import 'ranking_screen.dart';
import 'search_screen.dart';

/// 首页：影视风布局，背景跟随全局主题（默认白色）。
/// 数据来自榜单接口（无独立"最新上架"数据源）：
/// 热门推荐 Banner + 排行榜入口 + 热门影片海报栏 + 人气演员栏。
class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  late final JavDBClient _client;
  late final PageController _bannerController;
  List<Movie> _hotMovies = [];
  List<Actor> _hotActors = [];
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
      final results = await Future.wait(
          [_client.getRanking('playback'), _client.getRanking('actors')]);
      final movies = (results[0]['movies'] as List?)
              ?.map((m) => Movie.fromJson(m as Map<String, dynamic>))
              .toList() ??
          const <Movie>[];
      final actors = (results[1]['actors'] as List?)
              ?.map((a) => Actor.fromJson(a as Map<String, dynamic>))
              .toList() ??
          const <Actor>[];
      if (!mounted) return;
      setState(() {
        _hotMovies = movies;
        _hotActors = actors;
        _loading = false;
      });
      AppLogger.info(
          'Home loaded: ${movies.length} movies, ${actors.length} actors');
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _loading = false;
      });
      AppLogger.error('Failed to load home data', e);
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
                  _buildSectionHeader(context, '热门影片', Icons.movie),
                  _buildMovieRail(context),
                  _buildSectionHeader(context, '人气演员', Icons.face),
                  _buildActorRail(context),
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
    final items = _hotMovies.take(5).toList();
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
      ('影片榜', Icons.videocam, Color(0xFF4FC3F7), 'movies'),
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

  // ------------------------------------------------------------------ 横向海报栏

  Widget _buildMovieRail(BuildContext context) {
    if (_hotMovies.isEmpty) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 24),
        child: Center(
            child: Text('暂无数据',
                style: TextStyle(color: Theme.of(context).hintColor))),
      );
    }
    return SizedBox(
      height: 208,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 16),
        itemCount: _hotMovies.take(12).length,
        separatorBuilder: (_, __) => const SizedBox(width: 12),
        itemBuilder: (context, i) {
          final movie = _hotMovies[i];
          return _buildMovieCard(movie);
        },
      ),
    );
  }

  Widget _buildMovieCard(Movie movie) {
    return InkWell(
      borderRadius: BorderRadius.circular(12),
      onTap: () => _openMovie(movie),
      child: SizedBox(
        width: 118,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            ClipRRect(
              borderRadius: BorderRadius.circular(12),
              child: SizedBox(
                width: 118,
                height: 162,
                child: movie.coverUrl != null
                    ? Image.network(
                        resolveImageUrl(movie.coverUrl!),
                        fit: BoxFit.cover,
                        errorBuilder: (_, __, ___) => Container(
                            color: _cardColor,
                            child: const Icon(Icons.movie, size: 40)),
                      )
                    : Container(
                        color: _cardColor,
                        child: const Icon(Icons.movie, size: 40)),
              ),
            ),
            const SizedBox(height: 6),
            Text(
              movie.number,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600),
            ),
            if (movie.score != null)
              Text('★ ${movie.score}',
                  style: const TextStyle(
                      fontSize: 11, color: Colors.orangeAccent)),
          ],
        ),
      ),
    );
  }

  // ------------------------------------------------------------------ 演员栏

  Widget _buildActorRail(BuildContext context) {
    if (_hotActors.isEmpty) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 24),
        child: Center(
            child: Text('暂无数据',
                style: TextStyle(color: Theme.of(context).hintColor))),
      );
    }
    return SizedBox(
      height: 140,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 16),
        itemCount: _hotActors.take(12).length,
        separatorBuilder: (_, __) => const SizedBox(width: 16),
        itemBuilder: (context, i) {
          final actor = _hotActors[i];
          return _buildActorCard(context, actor);
        },
      ),
    );
  }

  Widget _buildActorCard(BuildContext context, Actor actor) {
    return InkWell(
      borderRadius: BorderRadius.circular(12),
      onTap: () => _push(context, SearchScreen(initialQuery: actor.name)),
      child: SizedBox(
        width: 72,
        child: Column(
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
