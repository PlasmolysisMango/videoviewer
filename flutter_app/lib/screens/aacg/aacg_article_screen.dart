import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
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
          Text(detail.content,
              style: const TextStyle(fontSize: 14, height: 1.6)),
        ],
      ],
    );
  }
}
