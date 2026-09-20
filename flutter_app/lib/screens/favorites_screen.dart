import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/models.dart';
import '../providers/subscription_provider.dart';
import '../providers/user_state_provider.dart';
import '../services/image_url.dart';
import 'actor_screen.dart';
import 'genre_screen.dart';
import 'list_detail_screen.dart';
import 'movie_detail_screen.dart';

/// 收藏夹：收藏的影片 + 订阅的合集/题材/演员（订阅即收藏，分类展示）。
class FavoritesScreen extends StatelessWidget {
  const FavoritesScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return DefaultTabController(
      length: 4,
      child: Scaffold(
        appBar: AppBar(
          title: const Text('收藏夹'),
          bottom: const TabBar(
            tabs: [
              Tab(text: '电影'),
              Tab(text: '合集'),
              Tab(text: '题材'),
              Tab(text: '演员'),
            ],
          ),
        ),
        body: const TabBarView(
          children: [
            _MovieFavorites(),
            _KindFavorites(kind: kSubCollection, emptyHint: '在合集页订阅合集后展示在这里'),
            _KindFavorites(kind: kSubGenre, emptyHint: '在题材页订阅题材后展示在这里'),
            _KindFavorites(kind: kSubActor, emptyHint: '在演员页订阅演员后展示在这里'),
          ],
        ),
      ),
    );
  }
}

/// 影片收藏（= JavDB "想看"标记，与登录账号双向同步）：竖版海报卡网格。
/// 本地缓存离线可看，下拉刷新从 JavDB 拉取最新。
class _MovieFavorites extends StatelessWidget {
  const _MovieFavorites();

  @override
  Widget build(BuildContext context) {
    final userState = context.watch<UserStateProvider>();
    final movies = userState.wantWatch;
    if (movies.isEmpty) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Text('在影片详情页点击 ♥（想看）后展示在这里'),
            const SizedBox(height: 8),
            TextButton.icon(
              onPressed: () => userState.refreshAll(),
              icon: const Icon(Icons.refresh, size: 18),
              label: const Text('从 JavDB 同步'),
            ),
          ],
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: () => userState.refreshAll(),
      child: GridView.builder(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.all(12),
        gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
          maxCrossAxisExtent: 120,
          mainAxisSpacing: 12,
          crossAxisSpacing: 12,
          childAspectRatio: 2 / 3.4,
        ),
        itemCount: movies.length,
        itemBuilder: (context, i) {
          final m = movies[i];
          return _MovieFavCard(movie: m);
        },
      ),
    );
  }
}

class _MovieFavCard extends StatelessWidget {
  final Map<String, dynamic> movie;

  const _MovieFavCard({required this.movie});

  @override
  Widget build(BuildContext context) {
    final id = (movie['id'] as String?) ?? '';
    final number = (movie['number'] as String?) ?? '';
    // 优先竖版海报，回退封面/缩略图（与榜单卡一致）。
    final cover = (movie['poster_url'] as String?) ??
        (movie['cover_url'] as String?) ??
        (movie['thumb_url'] as String?) ??
        '';
    return InkWell(
      borderRadius: BorderRadius.circular(10),
      onTap: () => Navigator.push(
        context,
        MaterialPageRoute(
          builder: (_) => MovieDetailScreen(movieId: id, movieNumber: number),
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Expanded(
            child: Stack(
              children: [
                Positioned.fill(
                  child: ClipRRect(
                    borderRadius: BorderRadius.circular(10),
                    child: cover.isNotEmpty
                        ? Image.network(
                            resolveImageUrl(cover),
                            fit: BoxFit.cover,
                            errorBuilder: (_, __, ___) =>
                                const _CoverFallback(),
                          )
                        : const _CoverFallback(),
                  ),
                ),
                // 取消想看
                Positioned(
                  right: 4,
                  top: 4,
                  child: _UnfavButton(id: id, small: true),
                ),
              ],
            ),
          ),
          const SizedBox(height: 4),
          Text(
            number,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            textAlign: TextAlign.center,
            style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w600),
          ),
        ],
      ),
    );
  }
}

class _CoverFallback extends StatelessWidget {
  const _CoverFallback();

  @override
  Widget build(BuildContext context) {
    return Container(
      color: Theme.of(context).dividerColor,
      child: const Icon(Icons.movie),
    );
  }
}

/// 合集/题材/演员订阅列表：点击跳对应浏览页，右侧取消订阅。
class _KindFavorites extends StatelessWidget {
  final String kind;
  final String emptyHint;

  const _KindFavorites({required this.kind, required this.emptyHint});

  @override
  Widget build(BuildContext context) {
    final subs = context.watch<SubscriptionProvider>().byKind(kind);
    if (subs.isEmpty) {
      return Center(child: Text(emptyHint, style: const TextStyle(color: Colors.grey)));
    }
    return ListView.separated(
      itemCount: subs.length,
      separatorBuilder: (_, __) => const Divider(height: 1),
      itemBuilder: (context, i) {
        final s = subs[i];
        final id = (s['id'] as String?) ?? '';
        final name = (s['name'] as String?) ?? '';
        return ListTile(
          leading: Icon(_iconFor(kind)),
          title: Text(name, maxLines: 1, overflow: TextOverflow.ellipsis),
          subtitle: _subtitleFor(kind, s),
          trailing: _UnfavButton(id: id, kind: kind),
          onTap: () => _open(context, kind, s),
        );
      },
    );
  }

  IconData _iconFor(String kind) => switch (kind) {
        kSubCollection => Icons.video_library_outlined,
        kSubGenre => Icons.style_outlined,
        kSubActor => Icons.face_retouching_natural,
        _ => Icons.bookmark_border,
      };

  Widget? _subtitleFor(String kind, Map<String, dynamic> s) {
    if (kind == kSubCollection) {
      final n = (s['movies_count'] as num?)?.toInt() ?? 0;
      if (n > 0) return Text('$n 部影片');
    }
    return null;
  }

  void _open(BuildContext context, String kind, Map<String, dynamic> s) {
    final id = (s['id'] as String?) ?? '';
    final name = (s['name'] as String?) ?? '';
    switch (kind) {
      case kSubCollection:
        final n = (s['movies_count'] as num?)?.toInt() ?? 0;
        Navigator.push(
          context,
          MaterialPageRoute(
            builder: (_) => ListDetailScreen(
              listId: id,
              listName: name,
              moviesCount: n,
            ),
          ),
        );
      case kSubGenre:
        final group = (s['group'] as String?) ?? '';
        Navigator.push(
          context,
          MaterialPageRoute(
            builder: (_) => GenreScreen(groupId: group, tagId: id, title: name),
          ),
        );
      case kSubActor:
        Navigator.push(
          context,
          MaterialPageRoute(
            builder: (_) => ActorScreen(
              actor: Actor(id: id, name: name, avatarUrl: s['avatar'] as String?),
            ),
          ),
        );
    }
  }
}

/// 取消收藏/订阅按钮：影片（想看）走 JavDB 同步，其余走本地订阅。
class _UnfavButton extends StatelessWidget {
  final String id;
  final String kind;
  final bool small;

  const _UnfavButton({required this.id, this.kind = kSubMovie, this.small = false});

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(12),
      onTap: () async {
        try {
          if (kind == kSubMovie) {
            await context.read<UserStateProvider>().toggleMark(
                  {'id': id},
                  kMarkWantWatch,
                );
          } else {
            await context
                .read<SubscriptionProvider>()
                .unsubscribe(kind, id);
          }
          if (context.mounted) {
            ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
              content: Text('已取消'),
              duration: Duration(seconds: 1),
            ));
          }
        } catch (e) {
          if (context.mounted) {
            ScaffoldMessenger.of(context).showSnackBar(SnackBar(
              content: Text('操作失败: $e'),
              backgroundColor: Colors.red,
            ));
          }
        }
      },
      child: Container(
        padding: EdgeInsets.all(small ? 4 : 8),
        decoration: const BoxDecoration(
          color: Colors.black38,
          shape: BoxShape.circle,
        ),
        child: Icon(
          Icons.close,
          size: small ? 14 : 18,
          color: Colors.white,
        ),
      ),
    );
  }
}
