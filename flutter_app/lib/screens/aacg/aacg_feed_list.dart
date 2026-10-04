import 'dart:async';

import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';

import '../../api/aacg_models.dart';
import '../../services/data_cache.dart';
import '../../services/image_url.dart';
import '../../services/logger.dart';
import '../../widgets/common_ui.dart';
import 'aacg_article_screen.dart';

/// AACG 信息流共享分页列表：推荐/分类/搜索三个入口复用同一套
/// 「首屏加载 → 触底续页 → 下拉刷新」状态机。
class AacgFeedList extends StatefulWidget {
  /// 拉取第 page 页（1 起）的回调，由调用方绑定具体接口。
  final Future<AacgFeedPage> Function(int page) load;

  /// 空结果时的提示文案。
  final String emptyText;

  /// 持久缓存 key（仅第 1 页快照）；null = 不缓存（搜索等一次性场景）。
  /// 命中缓存时进入直接展示、不发网络，仅下拉刷新重新拉取。
  final String? cacheKey;

  const AacgFeedList({
    super.key,
    required this.load,
    this.emptyText = '暂无内容',
    this.cacheKey,
  });

  @override
  State<AacgFeedList> createState() => _AacgFeedListState();
}

class _AacgFeedListState extends State<AacgFeedList> {
  List<AacgArticle> _items = [];
  int _page = 1;
  bool _hasNext = false;
  bool _loading = true;
  bool _loadingMore = false;
  String? _error;

  /// 请求代际：刷新时递增，旧响应返回后直接丢弃。
  int _loadSeq = 0;

  static const _cacheMaxAge = Duration(hours: 6);

  @override
  void initState() {
    super.initState();
    // 有缓存：只展示缓存，进入不重新拉取（下拉刷新才刷新）；
    // 无缓存/缓存过期：正常加载。
    if (widget.cacheKey == null) {
      _reload();
      return;
    }
    _restoreCache().then((hit) {
      if (!hit && mounted) _reload();
    });
  }

  /// 恢复上次成功拉取的第 1 页快照；返回是否命中。
  Future<bool> _restoreCache() async {
    try {
      final data =
          await DataCache.instance.read(widget.cacheKey!, maxAge: _cacheMaxAge);
      if (data is! Map || !mounted) return false;
      final box = data.cast<String, dynamic>();
      final items = (box['items'] as List<dynamic>?)
              ?.map((e) =>
                  AacgArticle.fromJson((e as Map).cast<String, dynamic>()))
              .toList() ??
          const <AacgArticle>[];
      if (items.isEmpty) return false;
      setState(() {
        _items = items;
        _page = box['page'] as int? ?? 1;
        _hasNext = box['hasNext'] as bool? ?? false;
        _loading = false;
      });
      return true;
    } catch (e) {
      AppLogger.warning('Restore aacg feed cache failed: $e');
      return false;
    }
  }

  /// 回写第 1 页快照（缓存只承载首屏，续页不入缓存）。
  void _saveCache() {
    final key = widget.cacheKey;
    if (key == null) return;
    unawaited(DataCache.instance.write(key, {
      'items': [for (final item in _items) item.toJson()],
      'page': _page,
      'hasNext': _hasNext,
    }));
  }

  /// 首屏/下拉刷新：整表替换；列表已有内容时不闪加载态，
  /// 刷新失败也保留旧内容。
  Future<void> _reload() async {
    final seq = ++_loadSeq;
    setState(() {
      _loading = true;
      _error = null;
      _loadingMore = false;
    });
    try {
      final result = await widget.load(1);
      if (!mounted || seq != _loadSeq) return;
      setState(() {
        _items = result.items;
        _page = result.page;
        _hasNext = result.hasNext;
        _loading = false;
      });
      _saveCache();
      AppLogger.info('Loaded aacg feed page $_page (${_items.length} items)');
    } catch (e) {
      if (!mounted || seq != _loadSeq) return;
      setState(() {
        if (_items.isEmpty) _error = e.toString();
        _loading = false;
      });
      AppLogger.error('Failed to load aacg feed', e);
    }
  }

  /// 触底续页：追加下一页；失败静默（保留已加载内容，继续下滑重试）。
  Future<void> _loadMore() async {
    if (_loading || _loadingMore || !_hasNext) return;
    final seq = _loadSeq;
    setState(() => _loadingMore = true);
    try {
      final result = await widget.load(_page + 1);
      if (!mounted || seq != _loadSeq) return;
      setState(() {
        _items = [..._items, ...result.items];
        // 上游页码未回显时按请求页码推进，避免原地打转重复拉取。
        _page = result.page > _page ? result.page : _page + 1;
        _hasNext = result.hasNext;
        _loadingMore = false;
      });
    } catch (e) {
      if (!mounted || seq != _loadSeq) return;
      setState(() => _loadingMore = false);
      AppLogger.warning('Load more aacg feed failed: $e');
    }
  }

  /// 列表滚动监听：距离底部 800px 内触发下一页。
  bool _onScrollNotification(ScrollNotification n) {
    if (n.metrics.extentAfter < 800) _loadMore();
    return false;
  }

  @override
  Widget build(BuildContext context) {
    if (_items.isEmpty) {
      if (_loading) return const Center(child: CircularProgressIndicator());
      if (_error != null) {
        return ErrorRetryView(error: _error!, onRetry: _reload);
      }
      return _buildEmpty(context);
    }
    return RefreshIndicator(
      onRefresh: _reload,
      child: NotificationListener<ScrollNotification>(
        onNotification: _onScrollNotification,
        child: ListView.separated(
          physics: const AlwaysScrollableScrollPhysics(),
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 0),
          itemCount: _items.length + 1,
          separatorBuilder: (_, __) => const SizedBox(height: 10),
          itemBuilder: (context, index) => index == _items.length
              ? _buildFooter(context)
              : _AacgArticleTile(article: _items[index]),
        ),
      ),
    );
  }

  Widget _buildEmpty(BuildContext context) {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.inbox_outlined,
              size: 56, color: Theme.of(context).hintColor),
          const SizedBox(height: 12),
          Text(widget.emptyText,
              style: TextStyle(color: Theme.of(context).hintColor)),
        ],
      ),
    );
  }

  /// 底部状态条：续页加载中 / 已加载全部 / 占位。
  Widget _buildFooter(BuildContext context) {
    if (_loadingMore) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 20),
        child: Center(
          child: SizedBox(
            width: 22,
            height: 22,
            child: CircularProgressIndicator(strokeWidth: 2.4),
          ),
        ),
      );
    }
    if (!_hasNext) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 20),
        child: Center(
          child: Text('已加载全部',
              style:
                  TextStyle(fontSize: 12, color: Theme.of(context).hintColor)),
        ),
      );
    }
    return const SizedBox(height: 24);
  }
}

/// 信息流条目卡片：视频封面 + 标题 + 摘要 + 发布日期。
class _AacgArticleTile extends StatelessWidget {
  final AacgArticle article;

  const _AacgArticleTile({required this.article});

  @override
  Widget build(BuildContext context) {
    final cover = article.coverUrl;
    final date = article.publishedAt.split('T').first;
    return InkWell(
      borderRadius: BorderRadius.circular(14),
      onTap: () => pushAacgArticle(context, article),
      child: Container(
        decoration: cardDecoration(context),
        padding: const EdgeInsets.all(10),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            ClipRRect(
              borderRadius: BorderRadius.circular(10),
              child: SizedBox(
                width: 112,
                height: 72,
                child: cover.isNotEmpty
                    ? CachedNetworkImage(
                        imageUrl: aacgImageUrl(cover),
                        fit: BoxFit.cover,
                        memCacheWidth: 336, // 112dp×3x：列表小图按显示尺寸解码
                        placeholder: (_, __) =>
                            Container(color: Theme.of(context).dividerColor),
                        errorWidget: (_, __, ___) => Container(
                          color: Theme.of(context).dividerColor,
                          child: const Icon(Icons.image_outlined, size: 24),
                        ),
                      )
                    : Container(
                        color: Theme.of(context).dividerColor,
                        child: const Icon(Icons.image_outlined, size: 24),
                      ),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    article.title.isNotEmpty ? article.title : '（无标题）',
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                        fontSize: 14,
                        fontWeight: FontWeight.w600,
                        height: 1.3),
                  ),
                  if (article.summary.isNotEmpty) ...[
                    const SizedBox(height: 5),
                    Text(
                      article.summary,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                          fontSize: 12,
                          color: Theme.of(context).hintColor,
                          height: 1.3),
                    ),
                  ],
                  if (date.isNotEmpty) ...[
                    const SizedBox(height: 5),
                    Text(date,
                        style: TextStyle(
                            fontSize: 11, color: Theme.of(context).hintColor)),
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
