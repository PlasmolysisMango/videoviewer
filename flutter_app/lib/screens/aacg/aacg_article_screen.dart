import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';

import '../../api/aacg_models.dart';
import '../../api/client.dart';
import '../../api/models.dart';
import '../../services/backend_launcher.dart';
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

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final detail = await _client.aacgArticle(widget.url);
      if (!mounted) return;
      setState(() {
        _detail = detail;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _loading = false;
      });
      AppLogger.error('Failed to load aacg article', e);
    }
  }

  /// 播放复用既有播放器：原生（ExoPlayer）直连 HLS，Web 走后端代理 + hls.js。
  /// movieId 留空：aacg 内容不写观影历史、不载字幕。
  void _play(AacgVideoLink video) {
    final stream = VideoStream(url: video.url, source: 'aacg');
    final title = _detail?.article.title ?? widget.title;
    final cover = _detail?.article.coverUrl ?? '';
    final Widget screen = kIsWeb
        ? HlsPlayerScreen(streams: [stream], title: title)
        : VideoPlayerScreen(streams: [stream], title: title, cover: cover);
    AppLogger.info('Playing aacg video (type: ${video.type})');
    Navigator.push(context, MaterialPageRoute(builder: (context) => screen));
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
    return ListView(
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
              style:
                  TextStyle(fontSize: 12, color: Theme.of(context).hintColor)),
        ],
        for (var i = 0; i < detail.videos.length; i++)
          Padding(
            padding: const EdgeInsets.only(top: 12),
            child: Align(
              alignment: Alignment.centerLeft,
              child: FilledButton.icon(
                onPressed: () => _play(detail.videos[i]),
                icon: const Icon(Icons.play_arrow),
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
