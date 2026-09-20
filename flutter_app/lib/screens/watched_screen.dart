import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../providers/user_state_provider.dart';
import '../services/image_url.dart';
import 'movie_detail_screen.dart';

/// 看过页：JavDB「看过」标记的影片（与登录账号同步 + 本地缓存）。
/// 竖版海报卡网格，右上角可取消标记。
class WatchedScreen extends StatelessWidget {
  const WatchedScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('看过')),
      body: const _WatchedGrid(),
    );
  }
}

class _WatchedGrid extends StatelessWidget {
  const _WatchedGrid();

  @override
  Widget build(BuildContext context) {
    final userState = context.watch<UserStateProvider>();
    final movies = userState.watched;
    if (movies.isEmpty) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Text('在详情页标记看过（或播放结束自动标记）后展示在这里'),
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
          return _WatchedCard(movie: movies[i]);
        },
      ),
    );
  }
}

class _WatchedCard extends StatelessWidget {
  final Map<String, dynamic> movie;

  const _WatchedCard({required this.movie});

  @override
  Widget build(BuildContext context) {
    final id = (movie['id'] as String?) ?? '';
    final number = (movie['number'] as String?) ?? '';
    // 优先竖版海报，回退封面/缩略图（与收藏夹卡一致）。
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
                            errorBuilder: (_, __, ___) => Container(
                              color: Theme.of(context).dividerColor,
                              child: const Icon(Icons.movie),
                            ),
                          )
                        : Container(
                            color: Theme.of(context).dividerColor,
                            child: const Icon(Icons.movie),
                          ),
                  ),
                ),
                // 左上角"看过"角标 + 右上角取消按钮
                Positioned(
                  left: 4,
                  top: 4,
                  child: Container(
                    padding: const EdgeInsets.all(4),
                    decoration: const BoxDecoration(
                      color: Colors.black38,
                      shape: BoxShape.circle,
                    ),
                    child: const Icon(Icons.done,
                        size: 14, color: Colors.lightGreenAccent),
                  ),
                ),
                Positioned(
                  right: 4,
                  top: 4,
                  child: _UnwatchButton(id: id),
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

/// 取消"看过"标记（与 JavDB 同步）。
class _UnwatchButton extends StatelessWidget {
  final String id;

  const _UnwatchButton({required this.id});

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(12),
      onTap: () async {
        try {
          await context
              .read<UserStateProvider>()
              .toggleMark({'id': id}, kMarkWatched);
          if (context.mounted) {
            ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
              content: Text('已取消看过'),
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
        padding: const EdgeInsets.all(4),
        decoration: const BoxDecoration(
          color: Colors.black38,
          shape: BoxShape.circle,
        ),
        child: const Icon(Icons.close, size: 14, color: Colors.white),
      ),
    );
  }
}
