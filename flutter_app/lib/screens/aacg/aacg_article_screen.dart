import 'dart:async';

import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';

import '../../api/aacg_models.dart';
import '../../api/client.dart';
import '../../api/models.dart';
import '../../services/backend_launcher.dart';
import '../../services/data_cache.dart';
import '../../services/image_url.dart';
import '../../services/logger.dart';
import '../../widgets/common_ui.dart';
import '../hls_player.dart';
import '../video_player_screen.dart';

/// 打开 AACG 文章详情页（信息流条目点击入口）。
void pushAacgArticle(BuildContext context, AacgArticle article) {
  Navigator.push(
    context,
    MaterialPageRoute(
      builder: (context) =>
          AacgArticleScreen(url: article.url, title: article.title),
    ),
  );
}

/// AACG 文章详情：封面 + 标题 + 正文 + 视频播放（复用既有播放器）。
class AacgArticleScreen extends StatefulWidget {
  final String url;
  final String title;

  const AacgArticleScreen({super.key, required this.url, this.title = ''});

  @override
  State<AacgArticleScreen> createState() => _AacgArticleScreenState();
}

class _AacgArticleScreenState extends State<AacgArticleScreen> {
  late final JavDBClient _client;
  AacgArticleDetail? _detail;
  bool _loading = true;
  String? _error;

  /// 正在探测选源的视频下标；非空时全部播放按钮禁用（目标按钮显示转圈）。
  int? _probingIndex;

  static const _cacheMaxAge = Duration(hours: 6);

  /// key 取路径而非完整 URL：镜像域名可能轮换，路径才是稳定标识。
  String get _cacheKey =>
      'aacg.article.v1.${Uri.tryParse(widget.url)?.path ?? widget.url}';

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    // 有缓存：只展示缓存，进入不重新拉取（下拉刷新才刷新）；
    // 无缓存/缓存过期：正常加载。
    _restoreCache().then((hit) {
      if (!hit && mounted) _load();
    });
  }

  /// 恢复上次成功拉取的详情快照；返回是否命中。
  Future<bool> _restoreCache() async {
    try {
      final data =
          await DataCache.instance.read(_cacheKey, maxAge: _cacheMaxAge);
      if (data is! Map || !mounted) return false;
      final detail = AacgArticleDetail.fromJson(data.cast<String, dynamic>());
      if (detail.article.title.isEmpty &&
          detail.content.isEmpty &&
          detail.videos.isEmpty) {
        return false;
      }
      setState(() {
        _detail = detail;
        _loading = false;
      });
      return true;
    } catch (e) {
      AppLogger.warning('Restore aacg article cache failed: $e');
      return false;
    }
  }

  /// 拉取详情并写缓存。showLoading=false（下拉刷新）时不闪加载态，
  /// 已有内容时失败静默保留旧内容。
  Future<void> _load({bool showLoading = true}) async {
    if (showLoading) {
      setState(() {
        _loading = true;
        _error = null;
      });
    } else {
      setState(() => _error = null);
    }
    try {
      final detail = await _client.aacgArticle(widget.url);
      if (!mounted) return;
      unawaited(DataCache.instance.write(_cacheKey, detail.toJson()));
      setState(() {
        _detail = detail;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      if (_detail != null) {
        AppLogger.warning('Refresh aacg article failed: $e');
        return;
      }
      setState(() {
        _error = e.toString();
        _loading = false;
      });
      AppLogger.error('Failed to load aacg article', e);
    }
  }

  /// 播放前动态探测选源：候选并行交给后端做两阶段验活（播放列表 + 分片级
  /// 深度验证，与播放同链路）。深度通过（key/首分片在当前网络可取）的源首项
  /// 直接播放，其余经本机 HLS 代理作为备用源传入播放器（初始化失败时自动
  /// 切换）；仅播放列表可用的候选殿后。全部候选都只有播放列表可用时（分片在
  /// 当前网络不可达，如菠萝啤类帖子），快速提示并保留「仍要尝试」，避免播放器
  /// 内逐个源白等后报 Source error。全失败自动刷新文章（auth_key 短时效）重试
  /// 一次，仍失败给出提示。旧后端无 sources 时退化为单候选。movieId 留空：
  /// aacg 内容不写观影历史、不载字幕。
  Future<void> _play(int index) async {
    final detail = _detail;
    if (detail == null || index < 0 || index >= detail.videos.length) return;
    setState(() => _probingIndex = index);
    try {
      var probe = await _client.aacgProbe(_candidates(detail.videos[index]));
      if (probe.urls.isEmpty) {
        final refreshed = await _client.aacgArticle(widget.url);
        if (!mounted) return;
        unawaited(DataCache.instance.write(_cacheKey, refreshed.toJson()));
        setState(() => _detail = refreshed);
        if (index < refreshed.videos.length) {
          probe = await _client.aacgProbe(_candidates(refreshed.videos[index]));
        }
      }
      if (!mounted) return;
      final usable = probe.urls;
      final playable = probe.playable;
      if (usable.isEmpty) {
        _showSnack('片源暂不可用，请稍后重试');
        return;
      }
      if (playable.isEmpty) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content:
              const Text('未找到可直连播放的片源（分片在当前网络不可达），可开启全局代理后重试'),
          duration: const Duration(seconds: 6),
          action: SnackBarAction(
            label: '仍要尝试',
            onPressed: () => _openPlayer(index, usable),
          ),
        ));
        return;
      }
      _openPlayer(index, <String>[
        ...playable,
        ...usable.where((u) => !playable.contains(u)),
      ]);
    } catch (e) {
      AppLogger.error('Failed to prepare aacg video', e);
      if (!mounted) return;
      _showSnack('播放准备失败，请检查服务是否运行');
    } finally {
      if (mounted) setState(() => _probingIndex = null);
    }
  }

  /// 打开播放页：首项为播放源、其余为备用源（初始化失败自动切换）。
  /// urls 均为上游地址，原生播放器统一经本机 HLS 代理包装。
  void _openPlayer(int index, List<String> urls) {
    if (!mounted || urls.isEmpty) return;
    final stream =
        VideoStream(url: _proxyStreamUrl(urls.first), source: 'aacg');
    final title = _detail?.article.title ?? widget.title;
    final cover = _detail?.article.coverUrl ?? '';
    final Widget screen = kIsWeb
        ? HlsPlayerScreen(streams: [stream], title: title)
        : VideoPlayerScreen(
            streams: [stream],
            title: title,
            cover: cover,
            fallbackUrls: urls.skip(1).map(_proxyStreamUrl).toList(),
          );
    final videos = _detail?.videos ?? const <AacgVideoLink>[];
    if (index < videos.length) {
      AppLogger.info('Playing aacg video (type: ${videos[index].type})');
    }
    Navigator.push(context, MaterialPageRoute(builder: (context) => screen));
  }

  List<String> _candidates(AacgVideoLink video) =>
      video.sources.isNotEmpty ? video.sources : <String>[video.url];

  /// 原生播放器统一经本机 HLS 代理取流（站点 CDN 域名对手机侧直连不可达，
  /// 后端链路才对它们可用）；Web 端 HlsPlayerScreen 自带代理包装，无需处理。
  /// 路径必须以 .m3u8 结尾：ExoPlayer(media3) 按 URL 后缀识别 HLS，无后缀会
  /// 被当作渐进媒体解析而报 Source error（见 server 端 playlist.m3u8 路由）。
  String _proxyStreamUrl(String url) {
    if (kIsWeb) return url;
    final query = Uri(queryParameters: {'u': url}).query;
    return '${BackendLauncher.baseUrl}/api/hls/playlist.m3u8?$query';
  }

  void _showSnack(String message) {
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          widget.title.isNotEmpty ? widget.title : '文章详情',
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? ErrorRetryView(error: _error!, onRetry: _load)
              : _buildDetail(context, _detail!),
    );
  }

  Widget _buildDetail(BuildContext context, AacgArticleDetail detail) {
    final article = detail.article;
    final cover = article.coverUrl;
    final date = article.publishedAt.split('T').first;
    return RefreshIndicator(
      onRefresh: () => _load(showLoading: false),
      child: ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
        children: [
          ClipRRect(
            borderRadius: BorderRadius.circular(12),
            child: AspectRatio(
              aspectRatio: 16 / 9,
              child: cover.isNotEmpty
                  ? CachedNetworkImage(
                      imageUrl: aacgImageUrl(cover),
                      fit: BoxFit.cover,
                      placeholder: (_, __) =>
                          Container(color: Theme.of(context).dividerColor),
                      errorWidget: (_, __, ___) => Container(
                        color: Theme.of(context).dividerColor,
                        child: const Icon(Icons.image_outlined, size: 40),
                      ),
                    )
                  : Container(
                      color: Theme.of(context).dividerColor,
                      child: const Icon(Icons.image_outlined, size: 40),
                    ),
            ),
          ),
          const SizedBox(height: 14),
          Text(
            article.title.isNotEmpty ? article.title : '（无标题）',
            style: const TextStyle(
                fontSize: 18, fontWeight: FontWeight.w700, height: 1.35),
          ),
          if (date.isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(date,
                style: TextStyle(
                    fontSize: 12, color: Theme.of(context).hintColor)),
          ],
          for (var i = 0; i < detail.videos.length; i++)
            Padding(
              padding: const EdgeInsets.only(top: 12),
              child: Align(
                alignment: Alignment.centerLeft,
                child: FilledButton.icon(
                  onPressed: _probingIndex != null ? null : () => _play(i),
                  icon: _probingIndex == i
                      ? const SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.play_arrow),
                  label: Text(detail.videos.length == 1
                      ? '播放视频'
                      : '播放视频 ${i + 1}'),
                ),
              ),
            ),
          if (detail.content.isNotEmpty) ...[
            const SizedBox(height: 18),
            const Divider(height: 1),
            const SizedBox(height: 14),
            ..._contentWidgets(detail),
          ],
          if (detail.previous != null || detail.next != null) ...[
            const SizedBox(height: 18),
            const Divider(height: 1),
            const SizedBox(height: 4),
            if (detail.previous != null)
              _AacgNavRow(
                label: '上一篇',
                icon: Icons.chevron_left,
                article: detail.previous!,
              ),
            if (detail.next != null)
              _AacgNavRow(
                label: '下一篇',
                icon: Icons.chevron_right,
                article: detail.next!,
              ),
          ],
        ],
      ),
    );
  }

  /// 正文区块：连续非图片段合并为一个富文本（段间按同一 Text 排版），
  /// 图片段独立成块；parts 为空时退化为纯文本（兼容旧后端）。
  List<Widget> _contentWidgets(AacgArticleDetail detail) {
    final parts = detail.contentParts;
    if (parts.isEmpty) {
      return [
        _AacgContentText(
          content: detail.content,
          parts: parts,
          start: 0,
          end: 0,
        ),
      ];
    }
    final widgets = <Widget>[];
    var i = 0;
    while (i < parts.length) {
      if (parts[i].imageUrl.isNotEmpty) {
        widgets.add(_AacgContentImage(url: parts[i].imageUrl));
        i++;
        continue;
      }
      var j = i;
      while (j < parts.length && parts[j].imageUrl.isEmpty) {
        j++;
      }
      widgets.add(_AacgContentText(
        content: detail.content,
        parts: parts,
        start: i,
        end: j,
      ));
      i = j;
    }
    return widgets;
  }
}

/// 正文富文本：链接段（跳转其他文章）主题色 + 下划线，点击进对应文章详情。
/// 仅渲染 parts[start, end) 范围，连续文本可与其他范围分开排版。
class _AacgContentText extends StatefulWidget {
  final String content;
  final List<AacgContentPart> parts;
  final int start;
  final int end;

  const _AacgContentText({
    required this.content,
    required this.parts,
    required this.start,
    required this.end,
  });

  @override
  State<_AacgContentText> createState() => _AacgContentTextState();
}

class _AacgContentTextState extends State<_AacgContentText> {
  final List<TapGestureRecognizer> _recognizers = [];
  late TextSpan _span;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _span = _buildSpan();
  }

  @override
  void didUpdateWidget(covariant _AacgContentText oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.content != widget.content ||
        oldWidget.start != widget.start ||
        oldWidget.end != widget.end ||
        !identical(oldWidget.parts, widget.parts)) {
      _span = _buildSpan();
    }
  }

  @override
  void dispose() {
    _disposeRecognizers();
    super.dispose();
  }

  void _disposeRecognizers() {
    for (final recognizer in _recognizers) {
      recognizer.dispose();
    }
    _recognizers.clear();
  }

  TextSpan _buildSpan() {
    _disposeRecognizers();
    const baseStyle = TextStyle(fontSize: 14, height: 1.6);
    if (widget.end <= widget.start) {
      return TextSpan(text: widget.content, style: baseStyle);
    }
    final primary = Theme.of(context).colorScheme.primary;
    final linkStyle = TextStyle(
      color: primary,
      decoration: TextDecoration.underline,
      decorationColor: primary,
    );
    final spans = <InlineSpan>[];
    for (var i = widget.start; i < widget.end; i++) {
      final part = widget.parts[i];
      if (part.url.isEmpty) {
        spans.add(TextSpan(text: part.text));
        continue;
      }
      final recognizer = TapGestureRecognizer()
        ..onTap = () {
          if (!mounted) return;
          pushAacgArticle(
              context, AacgArticle(title: part.text, url: part.url));
        };
      _recognizers.add(recognizer);
      spans.add(
          TextSpan(text: part.text, style: linkStyle, recognizer: recognizer));
    }
    return TextSpan(style: baseStyle, children: spans);
  }

  @override
  Widget build(BuildContext context) {
    return Text.rich(_span);
  }
}

/// 正文图片块：走后端解密代理（站点图片为混淆密文），撑满栏宽等比显示。
class _AacgContentImage extends StatelessWidget {
  final String url;

  const _AacgContentImage({required this.url});

  @override
  Widget build(BuildContext context) {
    final placeholderColor = Theme.of(context).dividerColor;
    Widget fallback() => Container(
          height: 180,
          color: placeholderColor,
          child: const Icon(Icons.image_outlined, size: 40),
        );
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(10),
        child: CachedNetworkImage(
          imageUrl: aacgImageUrl(url),
          width: double.infinity,
          fit: BoxFit.fitWidth,
          placeholder: (_, __) =>
              Container(height: 180, color: placeholderColor),
          errorWidget: (_, __, ___) => fallback(),
        ),
      ),
    );
  }
}

/// 页尾上一篇/下一篇跳转行（与正文内嵌的合集链接分离）。
class _AacgNavRow extends StatelessWidget {
  final String label;
  final IconData icon;
  final AacgArticle article;

  const _AacgNavRow({
    required this.label,
    required this.icon,
    required this.article,
  });

  @override
  Widget build(BuildContext context) {
    final hintColor = Theme.of(context).hintColor;
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: () => pushAacgArticle(context, article),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 10, horizontal: 4),
        child: Row(
          children: [
            Icon(icon, size: 18, color: hintColor),
            const SizedBox(width: 4),
            Text('$label：', style: TextStyle(fontSize: 13, color: hintColor)),
            Expanded(
              child: Text(
                article.title,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(fontSize: 13),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
