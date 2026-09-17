import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/models.dart';
import '../providers/subscription_provider.dart';
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

/// 影片收藏：竖版海报卡网格，点击进入详情。
class _MovieFavorites extends StatelessWidget {
  const _MovieFavorites();

  @override
  Widget build(BuildContext context) {
    final subs = context
        .watch<SubscriptionProvider>()
        .byKind(kSubMovie);
    if (subs.isEmpty) {
      return const Center(child: Text('在影片详情页点击 ♥ 收藏后展示在这里'));
    }
    return GridView.builder(
      padding: const EdgeInsets.all(12),
      gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
        maxCrossAxisExtent: 120,
        mainAxisSpacing: 12,
        crossAxisSpacing: 12,
        childAspectRatio: 2 / 3.4,
      ),
      itemCount: subs.length,
      itemBuilder: (context, i) {
        final s = subs[i];
        final id = (s['id'] as String?) ?? '';
        final number = (s['name'] as String?) ?? '';
        final cover = (s['avatar'] as String?) ?? '';
        return _MovieFavCard(id: id, number: number, cover: cover);
      },
    );
  }
}

class _MovieFavCard extends StatelessWidget {
  final String id;
  final String number;
  final String cover;

  const _MovieFavCard({required this.id, required this.number, required this.cover});

  @override
  Widget build(BuildContext context) {
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
                // 取消收藏
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

/// 取消收藏/订阅按钮。
class _UnfavButton extends StatelessWidget {
  final String id;
  final String kind;
  final bool small;

  const _UnfavButton({required this.id, this.kind = kSubMovie, this.small = false});

  @override
  Widget build(BuildContext context) {
    final provider = context.read<SubscriptionProvider>();
    return InkWell(
      borderRadius: BorderRadius.circular(12),
      onTap: () async {
        await provider.unsubscribe(kind, id);
        if (context.mounted) {
          ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
            content: Text('已取消'),
            duration: Duration(seconds: 1),
          ));
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
