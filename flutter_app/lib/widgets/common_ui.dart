import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';

import '../api/models.dart';
import '../screens/actor_screen.dart';
import '../screens/movie_detail_screen.dart';
import '../screens/search_screen.dart';
import '../services/image_url.dart';

/// 跟随主题的卡片底色（浅色模式浅灰、深色模式深灰）。
Color cardColor(BuildContext context) =>
    Theme.of(context).colorScheme.brightness == Brightness.dark
        ? const Color(0xFF1C2027)
        : const Color(0xFFF2F4F8);

/// 卡片容器装饰：圆角 + 卡片底色 + 描边，颜色全部跟随主题。
BoxDecoration cardDecoration(BuildContext context, {double radius = 14}) {
  return BoxDecoration(
    color: cardColor(context),
    borderRadius: BorderRadius.circular(radius),
    border: Border.all(color: Theme.of(context).dividerColor),
  );
}

/// 排名徽章：前三名橙色高亮，其余跟随主题灰色。
class RankBadge extends StatelessWidget {
  final int ranking;

  const RankBadge({super.key, required this.ranking});

  @override
  Widget build(BuildContext context) {
    final top = ranking <= 3;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: top ? const Color(0xFFFF7043) : Theme.of(context).dividerColor,
        borderRadius: BorderRadius.circular(12),
      ),
      child: Text(
        '#$ranking',
        style: TextStyle(
          color: top ? Colors.white : Theme.of(context).hintColor,
          fontWeight: FontWeight.bold,
          fontSize: 12,
        ),
      ),
    );
  }
}

/// 列表视图模式：大图海报网格 / 小图+文字列表。
enum MovieViewMode { grid, list }

/// 视图模式切换按钮（AppBar action）：展示的是切换目标的图标。
class ViewModeToggle extends StatelessWidget {
  final MovieViewMode mode;
  final ValueChanged<MovieViewMode> onChanged;

  const ViewModeToggle({super.key, required this.mode, required this.onChanged});

  @override
  Widget build(BuildContext context) {
    final toGrid = mode == MovieViewMode.list;
    return IconButton(
      tooltip: toGrid ? '大图模式' : '小图列表',
      icon: Icon(toGrid ? Icons.grid_view_outlined : Icons.view_list_outlined),
      onPressed: () =>
          onChanged(toGrid ? MovieViewMode.grid : MovieViewMode.list),
    );
  }
}

/// 点击跳演员专题页。
void pushActorScreen(BuildContext context, Actor actor) {
  Navigator.push(
    context,
    MaterialPageRoute(builder: (_) => ActorScreen(actor: actor)),
  );
}

/// 搜索结果中的演员卡片：圆形头像 + 名字 + 作品数，点击跳演员专题页。
class ActorSearchCard extends StatelessWidget {
  final Actor actor;

  const ActorSearchCard({super.key, required this.actor});

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(12),
      onTap: () => pushActorScreen(context, actor),
      child: SizedBox(
        width: 76,
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            CircleAvatar(
              radius: 28,
              backgroundColor: Theme.of(context).dividerColor,
              backgroundImage: actor.avatarUrl != null
                  ? NetworkImage(resolveImageUrl(actor.avatarUrl!))
                  : null,
              onBackgroundImageError:
                  actor.avatarUrl != null ? (_, __) {} : null,
              child: actor.avatarUrl == null
                  ? const Icon(Icons.person, size: 28)
                  : null,
            ),
            const SizedBox(height: 6),
            Text(
              actor.name,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              textAlign: TextAlign.center,
              style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w600),
            ),
            if (actor.videosCount != null)
              Text(
                '${actor.videosCount} 部',
                style:
                    TextStyle(fontSize: 10, color: Theme.of(context).hintColor),
              ),
          ],
        ),
      ),
    );
  }
}

/// 小图列表条目：封面 + 标题 + 演员（列表无演员数据时回退番号）+ 日期/评分。
class MovieCompactTile extends StatelessWidget {
  final Movie movie;

  const MovieCompactTile({super.key, required this.movie});

  @override
  Widget build(BuildContext context) {
    // 小图模式是竖版容器，优先用竖版海报，避免横版剧照被裁成中间竖条。
    final cover = movie.posterUrl ?? movie.thumbUrl ?? movie.coverUrl;
    final actors = movie.actors;
    final actorText =
        (actors != null && actors.isNotEmpty) ? actors.join(' / ') : movie.number;
    return InkWell(
      borderRadius: BorderRadius.circular(14),
      onTap: () => pushMovieDetail(context, movie),
      child: Container(
        decoration: cardDecoration(context),
        padding: const EdgeInsets.all(10),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            ClipRRect(
              borderRadius: BorderRadius.circular(10),
              child: SizedBox(
                width: 92,
                height: 124,
                child: cover != null
                    ? CachedNetworkImage(
                        imageUrl: resolveImageUrl(cover),
                        fit: BoxFit.cover,
                        placeholder: (_, __) => Container(
                          color: Theme.of(context).dividerColor,
                        ),
                        errorWidget: (_, __, ___) => Container(
                          color: Theme.of(context).dividerColor,
                          child: const Icon(Icons.movie, size: 28),
                        ),
                      )
                    : Container(
                        color: Theme.of(context).dividerColor,
                        child: const Icon(Icons.movie, size: 28),
                      ),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: SizedBox(
                height: 124,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      movie.title.isNotEmpty ? movie.title : movie.number,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                          fontSize: 14, fontWeight: FontWeight.w600, height: 1.3),
                    ),
                    const SizedBox(height: 5),
                    Text(
                      actorText,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                          fontSize: 12, color: Theme.of(context).hintColor),
                    ),
                    const Spacer(),
                    Row(
                      children: [
                        if (movie.releaseDate != null && movie.releaseDate!.isNotEmpty)
                          Text(
                            movie.releaseDate!,
                            style: TextStyle(
                                fontSize: 11,
                                color: Theme.of(context).hintColor),
                          ),
                        if (movie.hasCnsub == true) ...[
                          const SizedBox(width: 8),
                          Text('中字',
                              style: TextStyle(
                                  fontSize: 11,
                                  color: Colors.green.shade600,
                                  fontWeight: FontWeight.w600)),
                        ],
                        const Spacer(),
                        if (movie.score != null && movie.score! > 0)
                          Text(
                            '★ ${movie.score}',
                            style: const TextStyle(
                                color: Colors.orangeAccent,
                                fontWeight: FontWeight.bold,
                                fontSize: 12),
                          ),
                      ],
                    ),
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// 点击跳影片详情。
void pushMovieDetail(BuildContext context, Movie movie) {
  Navigator.push(
    context,
    MaterialPageRoute(
      builder: (_) => MovieDetailScreen(
        movieId: movie.id,
        movieNumber: movie.number,
      ),
    ),
  );
}

/// 榜单/列表用影片条目：圆角缩略图 + 番号/标题/日期 + 排名徽章 + 评分。
class RankingMovieTile extends StatelessWidget {
  final Movie movie;

  const RankingMovieTile({super.key, required this.movie});

  @override
  Widget build(BuildContext context) {
    // 竖版缩略图：优先竖版海报，避免横版剧照被裁剪。
    final cover = movie.posterUrl ?? movie.thumbUrl ?? movie.coverUrl;
    return InkWell(
      borderRadius: BorderRadius.circular(14),
      onTap: () => pushMovieDetail(context, movie),
      child: Container(
        decoration: cardDecoration(context),
        padding: const EdgeInsets.all(10),
        child: Row(
          children: [
            ClipRRect(
              borderRadius: BorderRadius.circular(10),
              child: SizedBox(
                width: 64,
                height: 86,
                child: cover != null
                    ? CachedNetworkImage(
                        imageUrl: resolveImageUrl(cover),
                        fit: BoxFit.cover,
                        placeholder: (_, __) => Container(
                          color: Theme.of(context).dividerColor,
                        ),
                        errorWidget: (_, __, ___) => Container(
                          color: Theme.of(context).dividerColor,
                          child: const Icon(Icons.movie, size: 28),
                        ),
                      )
                    : Container(
                        color: Theme.of(context).dividerColor,
                        child: const Icon(Icons.movie, size: 28),
                      ),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    movie.number,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                        fontSize: 15, fontWeight: FontWeight.w700),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    movie.title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                        fontSize: 13, color: Theme.of(context).hintColor),
                  ),
                  const SizedBox(height: 4),
                  Row(
                    children: [
                      if (movie.releaseDate != null)
                        Text(
                          movie.releaseDate!,
                          style: TextStyle(
                              fontSize: 11, color: Theme.of(context).hintColor),
                        ),
                      if (movie.hasCnsub == true) ...[
                        const SizedBox(width: 8),
                        Text('中字',
                            style: TextStyle(
                                fontSize: 11,
                                color: Colors.green.shade600,
                                fontWeight: FontWeight.w600)),
                      ],
                    ],
                  ),
                ],
              ),
            ),
            const SizedBox(width: 8),
            Column(
              mainAxisAlignment: MainAxisAlignment.center,
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                if (movie.ranking != null && movie.ranking! > 0)
                  RankBadge(ranking: movie.ranking!),
                if (movie.score != null)
                  Padding(
                    padding: const EdgeInsets.only(top: 6),
                    child: Text(
                      '★ ${movie.score}',
                      style: const TextStyle(
                          color: Colors.orangeAccent,
                          fontWeight: FontWeight.bold,
                          fontSize: 13),
                    ),
                  ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

/// 演员条目：圆形头像 + 名字 + 作品数 + 排名徽章。
class RankingActorTile extends StatelessWidget {
  final Actor actor;

  const RankingActorTile({super.key, required this.actor});

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(14),
      onTap: () {
        if (actor.id.isNotEmpty) {
          pushActorScreen(context, actor);
        } else {
          Navigator.push(
            context,
            MaterialPageRoute(
              builder: (_) => SearchScreen(initialQuery: actor.name),
            ),
          );
        }
      },
      child: Container(
        decoration: cardDecoration(context),
        padding: const EdgeInsets.all(12),
        child: Row(
          children: [
            CircleAvatar(
              radius: 28,
              backgroundColor: Theme.of(context).dividerColor,
              backgroundImage: actor.avatarUrl != null
                  ? NetworkImage(resolveImageUrl(actor.avatarUrl!))
                  : null,
              onBackgroundImageError:
                  actor.avatarUrl != null ? (_, __) {} : null,
              child: actor.avatarUrl == null
                  ? const Icon(Icons.person, size: 30)
                  : null,
            ),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    actor.name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                        fontSize: 15, fontWeight: FontWeight.w700),
                  ),
                  if (actor.videosCount != null)
                    Padding(
                      padding: const EdgeInsets.only(top: 4),
                      child: Text(
                        '${actor.videosCount} 部作品',
                        style: TextStyle(
                            fontSize: 13, color: Theme.of(context).hintColor),
                      ),
                    ),
                ],
              ),
            ),
            if (actor.ranking != null && actor.ranking! > 0)
              RankBadge(ranking: actor.ranking!),
          ],
        ),
      ),
    );
  }
}

/// 网格海报卡（搜索结果）：封面 + 番号 + 日期，点击跳详情。
class MovieGridCard extends StatelessWidget {
  final Movie movie;

  const MovieGridCard({super.key, required this.movie});

  @override
  Widget build(BuildContext context) {
    // 网格卡片为竖版海报比例：优先竖版海报（横版图会只显示中间竖条）。
    final cover = movie.posterUrl ?? movie.coverUrl ?? movie.thumbUrl;
    return InkWell(
      borderRadius: BorderRadius.circular(12),
      onTap: () => pushMovieDetail(context, movie),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            child: ClipRRect(
              borderRadius: BorderRadius.circular(12),
              child: Stack(
                fit: StackFit.expand,
                children: [
                  if (cover != null)
                    CachedNetworkImage(
                      imageUrl: resolveImageUrl(cover),
                      fit: BoxFit.cover,
                      placeholder: (_, __) =>
                          Container(color: cardColor(context)),
                      errorWidget: (_, __, ___) => Container(
                        color: cardColor(context),
                        child: const Icon(Icons.movie, size: 40),
                      ),
                    )
                  else
                    Container(
                      color: cardColor(context),
                      child: const Icon(Icons.movie, size: 40),
                    ),
                  if (movie.hasCnsub == true)
                    Positioned(
                      top: 6,
                      left: 6,
                      child: Container(
                        padding: const EdgeInsets.symmetric(
                            horizontal: 6, vertical: 2),
                        decoration: BoxDecoration(
                          color: Colors.green.shade600,
                          borderRadius: BorderRadius.circular(6),
                        ),
                        child: const Text('中字',
                            style:
                                TextStyle(color: Colors.white, fontSize: 10)),
                      ),
                    ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 6),
          Text(
            movie.number,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600),
          ),
          if (movie.releaseDate != null)
            Text(
              movie.releaseDate!,
              style:
                  TextStyle(fontSize: 11, color: Theme.of(context).hintColor),
            ),
        ],
      ),
    );
  }
}

/// 统一的错误态视图：图标 + 错误信息 + 重试按钮。
class ErrorRetryView extends StatelessWidget {
  final String error;
  final VoidCallback onRetry;

  const ErrorRetryView({super.key, required this.error, required this.onRetry});

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.error_outline,
              size: 48, color: Theme.of(context).colorScheme.error),
          const SizedBox(height: 16),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 32),
            child: Text(
              '错误: $error',
              textAlign: TextAlign.center,
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          ),
          const SizedBox(height: 16),
          FilledButton.icon(
            onPressed: onRetry,
            icon: const Icon(Icons.refresh),
            label: const Text('重试'),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// 排序选择器
// ---------------------------------------------------------------------------

/// 排序选项：value 传后端，label 显示给用户。
class SortOption {
  final String value;
  final String label;
  const SortOption(this.value, this.label);
}

/// 搜索结果页可用的排序选项。
const searchSortOptions = [
  SortOption('', '相关度'),
  SortOption('most_played', '最多播放'),
  SortOption('most_comments', '最多评论'),
  SortOption('newest', '最新发行'),
  SortOption('oldest', '最早发行'),
  SortOption('highest', '最高评分'),
  SortOption('lowest', '最低评分'),
  SortOption('most_magnets', '最多磁链'),
];

/// 演员作品页可用的排序选项。后端透传 app API 服务端排序
///（release/score/hit）；上游无对应取值的选项（最多评论、最低评分：
/// 列表行无该数据，选了也不生效）已移除。
const actorSortOptions = [
  SortOption('', '默认'),
  SortOption('most_played', '最多播放'),
  SortOption('highest', '最高评分'),
  SortOption('newest', '最新发行'),
  SortOption('oldest', '最早发行'),
  SortOption('most_magnets', '最多磁链'),
];

/// 排序下拉按钮：显示当前排序方式，点击弹出菜单切换。
class SortSelector extends StatelessWidget {
  final List<SortOption> options;
  final String selected;
  final ValueChanged<String> onChanged;

  const SortSelector({
    super.key,
    required this.options,
    required this.selected,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    final current = options.firstWhere(
      (o) => o.value == selected,
      orElse: () => options.first,
    );
    return PopupMenuButton<String>(
      tooltip: '排序方式',
      onSelected: onChanged,
      itemBuilder: (context) => [
        for (final o in options)
          CheckedPopupMenuItem<String>(
            value: o.value,
            checked: o.value == selected,
            child: Text(o.label),
          ),
      ],
      child: Container(
        height: 36,
        padding: const EdgeInsets.symmetric(horizontal: 10),
        decoration: BoxDecoration(
          border: Border.all(color: Theme.of(context).dividerColor),
          borderRadius: BorderRadius.circular(10),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.sort, size: 16, color: Theme.of(context).hintColor),
            const SizedBox(width: 4),
            Text(
              current.label,
              style: TextStyle(fontSize: 13, color: Theme.of(context).hintColor),
            ),
            const SizedBox(width: 2),
            Icon(Icons.expand_more,
                size: 16, color: Theme.of(context).hintColor),
          ],
        ),
      ),
    );
  }
}

