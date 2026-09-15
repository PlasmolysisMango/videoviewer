import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart' show Clipboard, ClipboardData;
import '../api/client.dart';
import '../api/models.dart';
import '../services/backend_launcher.dart';
import '../services/image_url.dart';
import '../services/logger.dart';
import '../widgets/common_ui.dart';
import 'hls_player.dart';
import 'search_screen.dart';
import 'video_player_screen.dart';

class MovieDetailScreen extends StatefulWidget {
  final String movieId;
  final String movieNumber;

  const MovieDetailScreen({
    super.key,
    required this.movieId,
    required this.movieNumber,
  });

  @override
  State<MovieDetailScreen> createState() => _MovieDetailScreenState();
}

class _MovieDetailScreenState extends State<MovieDetailScreen> {
  late final JavDBClient _client;
  Map<String, dynamic>? _movieData;
  List<Magnet> _magnets = [];
  Map<String, dynamic>? _avData;
  Map<String, List<VideoStream>> _streamsByVariant = {};
  List<String> _availableVariants = [];
  String _selectedVariant = 'normal';
  // 片源选择（missav / jable / hohoj）
  List<String> _availableSources = [];
  String _selectedSource = '';
  bool _isLoading = true;
  String? _error;
  int _galleryPage = 0;

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    _loadMovie();
    _loadAvData();
    _loadSources();
  }

  /// 从后端拉取已注册的 AV 数据源列表（missav/jable/hohoj）。
  Future<void> _loadSources() async {
    try {
      final sources = await _client.avSources();
      if (!mounted) return;
      setState(() {
        _availableSources = sources;
        // 默认选中第一个（优先级最高）
        if (_selectedSource.isEmpty && sources.isNotEmpty) {
          _selectedSource = sources.first;
        }
      });
      AppLogger.info('Available AV sources: $sources');
    } catch (e) {
      // 回退：硬编码默认值
      AppLogger.warning('Failed to load AV sources, using defaults: $e');
      if (!mounted) return;
      setState(() {
        _availableSources = ['missav', 'jable', 'hohoj'];
        if (_selectedSource.isEmpty) _selectedSource = 'missav';
      });
    }
  }

  Future<void> _loadMovie() async {
    try {
      AppLogger.info('Loading movie: ${widget.movieId}');
      final result = await _client.getMovie(widget.movieId);
      final magnetsList = <Magnet>[];

      // Handle both single magnet (Map) and list of magnets
      final magnetsData = result['magnets'];
      if (magnetsData != null) {
        if (magnetsData is List) {
          magnetsList.addAll(magnetsData
              .map((m) => Magnet.fromJson(m as Map<String, dynamic>)));
        } else if (magnetsData is Map) {
          magnetsList.add(Magnet.fromJson(magnetsData as Map<String, dynamic>));
        }
      }

      setState(() {
        _movieData = result['movie'] as Map<String, dynamic>?;
        _magnets = magnetsList;
        _isLoading = false;
      });
      AppLogger.info(
          'Movie loaded: ${_movieData?['number']}, magnets: ${_magnets.length}');
    } catch (e) {
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
      AppLogger.error('Failed to load movie', e);
    }
  }

  Future<void> _loadAvData() async {
    try {
      AppLogger.info('Loading AV data for: ${widget.movieNumber}');
      final result = await _client.avDetail(widget.movieNumber);
      setState(() {
        _avData = result['video'] as Map<String, dynamic>?;
      });
      AppLogger.info('AV data loaded, m3u8: ${_avData?['m3u8']}');

      // 拉取多路流（不同清晰度）供播放器切换
      _loadStreams();
    } catch (e) {
      AppLogger.warning('AV data not available: $e');
    }
  }

  /// 拉取全部可选片源（无码/中字/普通 × 各清晰度），按变体优先级排序。
  /// 传入 _selectedSource 指定用户选择的数据源（missav/jable/hohoj）。
  Future<void> _loadStreams() async {
    try {
      final source = _selectedSource.isNotEmpty ? _selectedSource : null;
      final result = await _client.avResolve(widget.movieNumber, source: source);
      final rawList = (result['streams'] as List?)
              ?.map((s) => VideoStream.fromJson(s as Map<String, dynamic>))
              .toList() ??
          const <VideoStream>[];
      _applyStreams(VideoStream.sortStreams(rawList));
      AppLogger.info('Loaded ${rawList.length} streams from source=$_selectedSource');
    } catch (e) {
      AppLogger.warning('Streams not available: $e');
    }
  }

  /// 按片源变体分组（MissAV 里无码/中字/普通是不同的视频），
  /// 默认选中按 无码 > 中字 > 普通 的优先级。
  void _applyStreams(List<VideoStream> sorted) {
    final groups = <String, List<VideoStream>>{};
    for (final s in sorted) {
      final key = s.uncensored == true
          ? 'uncensored'
          : (s.cnsub == true ? 'cnsub' : 'normal');
      groups.putIfAbsent(key, () => []).add(s);
    }
    const order = ['uncensored', 'cnsub', 'normal'];
    setState(() {
      _streamsByVariant = groups;
      _availableVariants = order.where(groups.containsKey).toList();
      if (!_availableVariants.contains(_selectedVariant)) {
        _selectedVariant =
            _availableVariants.isNotEmpty ? _availableVariants.first : 'normal';
      }
    });
  }

  /// 片源变体显示名（MissAV 里无码/中字/原片是不同的视频）。
  static const _variantLabels = {
    'uncensored': '无码',
    'cnsub': '中字',
    'normal': '原片',
  };

  /// 片源站点显示名。
  static const _sourceLabels = {
    'missav': 'MissAV',
    'jable': 'Jable',
    'hohoj': 'HohoJ',
  };

  /// 切换片源站点时重新解析流。
  void _onSourceChanged(String source) {
    if (source == _selectedSource) return;
    setState(() {
      _selectedSource = source;
      // 清空旧的流，等待新源解析
      _streamsByVariant = {};
      _availableVariants = [];
      _selectedVariant = 'normal';
    });
    _loadStreams();
  }

  /// 片源站点下拉选择器，始终展示在播放按钮行前方。
  Widget _buildSourceSelector() {
    if (_availableSources.isEmpty) return const SizedBox.shrink();
    return PopupMenuButton<String>(
      tooltip: '选择片源站点',
      onSelected: _onSourceChanged,
      itemBuilder: (context) => [
        for (final s in _availableSources)
          CheckedPopupMenuItem<String>(
            value: s,
            checked: s == _selectedSource,
            child: Text(_sourceLabels[s] ?? s),
          ),
      ],
      child: Container(
        height: 48,
        padding: const EdgeInsets.symmetric(horizontal: 14),
        decoration: BoxDecoration(
          border: Border.all(
              color: Theme.of(context).colorScheme.outlineVariant),
          borderRadius: BorderRadius.circular(14),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              '片源',
              style: TextStyle(
                  fontSize: 13, color: Theme.of(context).hintColor),
            ),
            const SizedBox(width: 6),
            Text(
              _sourceLabels[_selectedSource] ?? _selectedSource,
              style: const TextStyle(fontWeight: FontWeight.w600),
            ),
            const SizedBox(width: 4),
            const Icon(Icons.expand_more, size: 18),
          ],
        ),
      ),
    );
  }

  /// 播放按钮旁的变体下拉：存在多个变体时展示，单一片源时隐藏。
  Widget _buildVariantSelector() {
    if (_availableVariants.length < 2) return const SizedBox.shrink();
    return PopupMenuButton<String>(
      tooltip: '选择变体',
      onSelected: (v) => setState(() => _selectedVariant = v),
      itemBuilder: (context) => [
        for (final v in _availableVariants)
          CheckedPopupMenuItem<String>(
            value: v,
            checked: v == _selectedVariant,
            child: Text(
              '${_variantLabels[v] ?? v}'
              '${(_streamsByVariant[v]?.length ?? 0) > 1 ? ' · ${_streamsByVariant[v]!.length}档清晰度' : ''}',
            ),
          ),
      ],
      child: Container(
        height: 48,
        padding: const EdgeInsets.symmetric(horizontal: 14),
        decoration: BoxDecoration(
          border: Border.all(
              color: Theme.of(context).colorScheme.outlineVariant),
          borderRadius: BorderRadius.circular(14),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              '变体',
              style: TextStyle(
                  fontSize: 13, color: Theme.of(context).hintColor),
            ),
            const SizedBox(width: 6),
            Text(
              _variantLabels[_selectedVariant] ?? _selectedVariant,
              style: const TextStyle(fontWeight: FontWeight.w600),
            ),
            const SizedBox(width: 4),
            const Icon(Icons.expand_more, size: 18),
          ],
        ),
      ),
    );
  }

  Future<void> _playVideo() async {
    // 播放当前选中的片源（组内可切清晰度）；后台解析尚未完成时现场补拉，
    // 最后回退 avData 单路 m3u8
    var streams = List<VideoStream>.from(
        _streamsByVariant[_selectedVariant] ?? const <VideoStream>[]);
    if (streams.isEmpty) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('正在解析视频源...')),
        );
      }
      await _loadStreams();
      streams = List<VideoStream>.from(
          _streamsByVariant[_selectedVariant] ?? const <VideoStream>[]);
      if (streams.isEmpty) {
        final m3u8Url = _avData?['m3u8'] as String?;
        if (m3u8Url == null || m3u8Url.isEmpty) {
          if (mounted) {
            ScaffoldMessenger.of(context).showSnackBar(
              const SnackBar(content: Text('视频源不可用')),
            );
          }
          return;
        }
        streams.add(VideoStream(url: m3u8Url));
      }
    }

    final title = _movieData?['title'] as String? ?? 'Video';
    final Widget screen;
    if (kIsWeb) {
      // 浏览器无法播 HLS 且无法伪造 Referer：播放器内部走后端 HLS 代理 + hls.js
      screen = HlsPlayerScreen(streams: streams, title: title);
    } else {
      // 原生播放器（ExoPlayer）原生支持 HLS，可带 Referer 头直连
      screen = VideoPlayerScreen(streams: streams, title: title);
    }

    AppLogger.info(
        'Playing: ${streams.first.url}, streams: ${streams.length}, web: $kIsWeb');
    Navigator.push(
      context,
      MaterialPageRoute(builder: (context) => screen),
    );
  }

  /// 提取 Detail 中的链接名称（series/maker/publisher 为单个 Link 对象）。
  List<String> _linkNames(dynamic v) {
    if (v is Map) {
      final n = v['name'];
      return (n is String && n.isNotEmpty) ? [n] : const [];
    }
    return (v as List?)
            ?.whereType<Map>()
            .map((e) => e['name'])
            .whereType<String>()
            .where((n) => n.isNotEmpty)
            .toList() ??
        const <String>[];
  }

  void _openSearch(String query) {
    Navigator.push(
      context,
      MaterialPageRoute(
          builder: (context) => SearchScreen(initialQuery: query)),
    );
  }

  /// 可点击标签区：点击任一项跳搜索页查找相关影片。
  /// 演员区：chip 点击进演员专题页（无 id 时回退关键词搜索）。
  Widget _buildActorSection(dynamic credits) {
    final actors = <Actor>[];
    if (credits is List) {
      for (final a in credits) {
        if (a is Map) {
          final name = a['name'];
          if (name is String && name.isNotEmpty) {
            actors.add(Actor(
              id: (a['id'] as String?) ?? '',
              name: name,
            ));
          }
        }
      }
    }
    if (actors.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(bottom: 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text('演员',
              style: TextStyle(fontSize: 16, fontWeight: FontWeight.bold)),
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: actors
                .map((a) => ActionChip(
                      label: Text(a.name,
                          style: TextStyle(
                              fontSize: 13,
                              color: Theme.of(context).colorScheme.primary)),
                      side: BorderSide(color: Theme.of(context).dividerColor),
                      onPressed: () {
                        if (a.id.isNotEmpty) {
                          pushActorScreen(context, a);
                        } else {
                          _openSearch(a.name);
                        }
                      },
                    ))
                .toList(),
          ),
        ],
      ),
    );
  }

  /// 可点击标签区：点击任一项跳搜索页查找相关影片。
  Widget _buildChipSection(String title, List<String> names) {
    if (names.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(bottom: 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(title,
              style:
                  const TextStyle(fontSize: 16, fontWeight: FontWeight.bold)),
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: names
                .map((n) => ActionChip(
                      label: Text(n,
                          style: TextStyle(
                              fontSize: 13,
                              color: Theme.of(context).colorScheme.primary)),
                      side: BorderSide(color: Theme.of(context).dividerColor),
                      onPressed: () => _openSearch(n),
                    ))
                .toList(),
          ),
        ],
      ),
    );
  }

  void _downloadVideo() {
    if (_avData == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('视频源解析中，请稍后')),
      );
      return;
    }
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('下载功能开发中...')),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(_movieData?['number'] as String? ?? '电影详情'),
      ),
      body: _isLoading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? ErrorRetryView(error: _error!, onRetry: _loadMovie)
              : _movieData == null
                  ? const Center(child: Text('未找到电影'))
                  : _buildContent(),
    );
  }

  /// 画廊：封面在前，JavDB 详情页预览大图随后，左右滑动切换。
  Widget _buildGallery(Map<String, dynamic> movie) {
    final urls = <String>[];
    final cover = movie['cover_url'] as String?;
    if (cover != null && cover.isNotEmpty) urls.add(cover);
    final previews =
        (movie['preview_images'] as List<dynamic>?)?.cast<String>() ??
            const <String>[];
    urls.addAll(previews.where((u) => u.isNotEmpty));
    if (urls.isEmpty) return const SizedBox.shrink();

    return Column(
      children: [
        SizedBox(
          height: 300,
          child: PageView.builder(
            itemCount: urls.length,
            onPageChanged: (i) => setState(() => _galleryPage = i),
            itemBuilder: (context, i) {
              return Center(
                child: Image.network(
                  resolveImageUrl(urls[i]),
                  fit: BoxFit.contain,
                  errorBuilder: (_, __, ___) =>
                      const Icon(Icons.movie, size: 100),
                ),
              );
            },
          ),
        ),
        if (urls.length > 1)
          Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: List.generate(urls.length, (i) {
              return Container(
                width: 8,
                height: 8,
                margin: const EdgeInsets.symmetric(horizontal: 3, vertical: 8),
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  color: i == _galleryPage
                      ? Theme.of(context).colorScheme.primary
                      : Theme.of(context).hintColor.withOpacity(0.3),
                ),
              );
            }),
          ),
      ],
    );
  }

  Widget _buildContent() {
    final movie = _movieData!;
    return SingleChildScrollView(
      padding: const EdgeInsets.all(16.0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // 图片画廊（封面 + 预览图，可滑动切换）
          _buildGallery(movie),
          const SizedBox(height: 16),
          // Title + 评分徽章
          Row(
            children: [
              Expanded(
                child: Text(
                  movie['number'] as String? ?? '',
                  style: const TextStyle(
                      fontSize: 24, fontWeight: FontWeight.bold),
                ),
              ),
              if (movie['score'] != null)
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                  decoration: BoxDecoration(
                    color: Colors.orange,
                    borderRadius: BorderRadius.circular(12),
                  ),
                  child: Text(
                    '★ ${movie['score']}',
                    style: const TextStyle(
                        color: Colors.white,
                        fontWeight: FontWeight.w700,
                        fontSize: 13),
                  ),
                ),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            movie['title'] as String? ?? '',
            style: TextStyle(
                fontSize: 15, color: Theme.of(context).hintColor, height: 1.4),
          ),
          const SizedBox(height: 16),
          // Info 卡片
          Container(
            width: double.infinity,
            decoration: cardDecoration(context),
            padding: const EdgeInsets.all(14),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                _buildInfoRow('发行日期', movie['release_date'] as String?),
                _buildInfoRow(
                    '时长',
                    movie['duration'] != null
                        ? '${movie['duration']} 分钟'
                        : null),
              ],
            ),
          ),
          const SizedBox(height: 16),
          // 演员/类型/系列等可点击标签：演员进专题页，其余跳搜索
          _buildActorSection(movie['actor_credits']),
          _buildChipSection('类型标签', _linkNames(movie['tags'])),
          _buildChipSection('系列 / 片商 / 发行商', [
            ..._linkNames(movie['series']),
            ..._linkNames(movie['maker']),
            ..._linkNames(movie['publisher']),
          ]),
          const SizedBox(height: 24),
          // 播放/下载按钮默认展示；AV 流在后台解析，点击播放时如未就绪会现场补拉
          // 片源站点选择（missav/jable/hohoj）+ MissAV 变体选择（无码/中字/原片）
          Row(
            children: [
              _buildSourceSelector(),
              const SizedBox(width: 8),
              _buildVariantSelector(),
              const SizedBox(width: 12),
              Expanded(
                child: FilledButton.icon(
                  onPressed: _playVideo,
                  icon: const Icon(Icons.play_arrow),
                  label: const Text('播放'),
                  style: FilledButton.styleFrom(
                    padding: const EdgeInsets.symmetric(vertical: 14),
                    shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(14)),
                  ),
                ),
              ),
              const SizedBox(width: 16),
              Expanded(
                child: OutlinedButton.icon(
                  onPressed: _downloadVideo,
                  icon: const Icon(Icons.download),
                  label: const Text('下载'),
                  style: OutlinedButton.styleFrom(
                    padding: const EdgeInsets.symmetric(vertical: 14),
                    shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(14)),
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 24),
          // Magnets
          const Text(
            '磁链',
            style: TextStyle(fontSize: 16, fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 8),
          if (_magnets.isEmpty)
            Text('暂无磁链', style: TextStyle(color: Theme.of(context).hintColor))
          else
            ..._magnets.map((magnet) => Container(
                  margin: const EdgeInsets.only(bottom: 10),
                  decoration: cardDecoration(context),
                  child: ListTile(
                    shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(14)),
                    title: Text(
                      magnet.name,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                          fontSize: 14, fontWeight: FontWeight.w600),
                    ),
                    subtitle: Row(
                      children: [
                        if (magnet.sizeText != null)
                          Text('大小: ${magnet.sizeText}  ',
                              style: TextStyle(
                                  fontSize: 12,
                                  color: Theme.of(context).hintColor)),
                        if (magnet.cnsub == true)
                          Text('中字  ',
                              style: TextStyle(
                                  fontSize: 12,
                                  color: Colors.green.shade600,
                                  fontWeight: FontWeight.w600)),
                        if (magnet.hd == true)
                          const Text('HD  ',
                              style: TextStyle(
                                  fontSize: 12,
                                  color: Colors.blue,
                                  fontWeight: FontWeight.w600)),
                      ],
                    ),
                    trailing: IconButton(
                      icon: const Icon(Icons.copy),
                      tooltip: '复制磁力链接',
                      onPressed: magnet.magnetUrl == null
                          ? null
                          : () async {
                              await Clipboard.setData(
                                  ClipboardData(text: magnet.magnetUrl!));
                              if (mounted) {
                                ScaffoldMessenger.of(context).showSnackBar(
                                  const SnackBar(content: Text('磁力链接已复制')),
                                );
                              }
                            },
                    ),
                  ),
                )),
        ],
      ),
    );
  }

  Widget _buildInfoRow(String label, String? value) {
    if (value == null) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 80,
            child: Text(
              label,
              style: TextStyle(color: Theme.of(context).hintColor),
            ),
          ),
          Expanded(
              child: Text(value,
                  style: const TextStyle(fontWeight: FontWeight.w500))),
        ],
      ),
    );
  }
}
