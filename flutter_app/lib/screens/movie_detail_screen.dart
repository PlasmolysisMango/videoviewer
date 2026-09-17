import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart' show Clipboard, ClipboardData;
import 'package:provider/provider.dart';
import '../api/client.dart';
import '../api/models.dart';
import '../providers/subscription_provider.dart';
import '../services/backend_launcher.dart';
import '../services/history.dart';
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
  // 相似推荐：[{"movie": {...}, "reason": "..."}]，异步拉取失败静默
  List<Map<String, dynamic>> _similar = [];
  // JavDB 用户评论：失败静默，排序切换后重拉
  List<Map<String, dynamic>> _reviews = [];
  int _reviewTotal = 0;
  String _reviewSort = 'hotly';
  bool _reviewsLoading = false;
  Map<String, dynamic>? _avData;
  // 变体探测失败时的占位列表；正常以 avProbe（MissAV 搜索接口）结果为准。
  static const _defaultVariants = ['uncensored', 'cnsub', 'normal'];

  Map<String, List<VideoStream>> _streamsByVariant = {};
  List<String> _availableVariants = _defaultVariants;
  String _selectedVariant = 'uncensored';
  // 片源选择（missav / jable / hohoj）
  List<String> _availableSources = [];
  String _selectedSource = '';
  String? _streamError; // 片源解析错误信息
  bool _resolving = false; // 点击播放后按需解析中的加载态（按钮图标）
  bool _isLoading = true;
  String? _error;
  int _galleryPage = 0;

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    _loadMovie();
    _loadSimilar();
    _loadReviews();
    _loadAvData();
    _loadSources();
    // 进入详情即计入观影历史（浏览过）；播放页再更新观看进度。
    HistoryService.recordView(
      id: widget.movieId,
      number: widget.movieNumber,
      title: '',
      cover: '',
    );
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
      // 元信息就绪，补全历史条目（番号/标题/封面，进度合并）。
      final m = _movieData;
      if (m != null) {
        HistoryService.recordView(
          id: widget.movieId,
          number: (m['number'] as String?) ?? '',
          title: (m['title'] as String?) ?? '',
          cover: (m['cover_url'] as String?) ?? '',
        );
      }
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

  /// 相似推荐：与详情主内容并行拉取；失败静默，仅隐藏区块。
  Future<void> _loadSimilar() async {
    try {
      final result = await _client.getSimilarMovies(widget.movieId);
      final list = (result['similar'] as List?)
              ?.whereType<Map<String, dynamic>>()
              .toList() ??
          const <Map<String, dynamic>>[];
      if (!mounted) return;
      setState(() => _similar = list);
      AppLogger.info('Similar movies loaded: ${list.length}');
    } catch (e) {
      AppLogger.warning('Failed to load similar movies: $e');
    }
  }

  Future<void> _loadAvData() async {
    try {
      final source = _selectedSource.isNotEmpty ? _selectedSource : null;
      AppLogger.info('Loading AV data for: ${widget.movieNumber}, source=$source');
      final result = await _client.avDetail(widget.movieNumber, source: source);
      setState(() {
        _avData = result['video'] as Map<String, dynamic>?;
        // 先展示占位变体，随后用搜索接口探测真实变体并刷新按钮
        _availableVariants = _defaultVariants;
        _streamError = null;
      });
      AppLogger.info('AV data loaded, m3u8: ${_avData?['m3u8']}');
      // 提前探测变体：不阻塞详情页，结果回来即更新
      _probeVariants();
    } catch (e) {
      AppLogger.error('AV data not available', e);
    }
  }

  /// 进入详情页即探测可用变体：一次搜索请求拿到该番号的全部变体页面，
  /// 没有「无码」就不展示无码按钮，按 无码 → 中字 → 原片 排序。
  /// 探测失败静默保留占位符，不影响播放（点击播放仍按需解析）。
  Future<void> _probeVariants() async {
    final number = widget.movieNumber;
    if (number.isEmpty) return;
    final srcAtStart = _selectedSource;
    try {
      final source = srcAtStart.isNotEmpty ? srcAtStart : null;
      final result = await _client.avProbe(number, source: source);
      // 探测期间用户切换了片源：结果属于旧源，丢弃（切源会重新探测）
      if (_selectedSource != srcAtStart || !mounted) return;
      const order = ['uncensored', 'cnsub', 'normal'];
      final kinds = <String>[];
      for (final v in (result['variants'] as List? ?? const [])) {
        final m = v as Map<String, dynamic>;
        final kind = (m['kind'] as String?) ?? '';
        final available = (m['available'] as bool?) ?? true;
        if (!available || !order.contains(kind) || kinds.contains(kind)) {
          continue;
        }
        kinds.add(kind);
      }
      kinds.sort((a, b) => order.indexOf(a).compareTo(order.indexOf(b)));
      if (kinds.isEmpty) return;
      setState(() {
        _availableVariants = kinds;
        // 占位选中的变体不存在时，改选探测到的第一个
        if (!kinds.contains(_selectedVariant)) {
          _selectedVariant = kinds.first;
        }
      });
      AppLogger.info('Probed variants for $number: $kinds');
    } catch (e) {
      AppLogger.info('Probe variants failed, keep placeholders: $e');
    }
  }

  /// 按需拉取指定变体的播放流（惰性加载）。
  /// 仅在用户点击播放时调用，避免进入详情页后立即发起网络请求。
  Future<void> _resolveVariant(String variant) async {
    try {
      final source = _selectedSource.isNotEmpty ? _selectedSource : null;
      final result = await _client.avResolve(
        widget.movieNumber,
        source: source,
        variant: variant,
      );
      final rawList = (result['streams'] as List?)
              ?.map((s) => VideoStream.fromJson(s as Map<String, dynamic>))
              .toList() ??
          const <VideoStream>[];
      final sorted = VideoStream.sortStreams(rawList);
      setState(() {
        _streamsByVariant[variant] = sorted;
        _streamError = null;
      });
      AppLogger.info('Resolved $variant: ${sorted.length} streams');
    } catch (e) {
      final msg = e.toString();
      setState(() => _streamError = msg);
      AppLogger.error('Resolve variant $variant failed', e);
    }
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

  /// 切换片源站点时重置变体状态。
  void _onSourceChanged(String source) {
    if (source == _selectedSource) return;
    setState(() {
      _selectedSource = source;
      // 清空旧的流与变体，恢复占位符；_loadAvData 会重新探测真实变体
      _streamsByVariant = {};
      _availableVariants = _defaultVariants;
      _selectedVariant = 'uncensored';
      _streamError = null;
    });
    _loadAvData();
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

  /// 播放按钮旁的变体下拉：单变体也展示（下拉里只有一项，如「原片」），
  /// 让用户明确该片源的可用变体，而不是下拉消失让人误以为探测失效。
  Widget _buildVariantSelector() {
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
    // 惰性加载：如果该变体的流尚未解析，先按需拉取。
    // 解析中不弹任何提示（避免遮挡画面），仅在播放按钮上显示加载图标；
    // 失败时才用 SnackBar 提示。
    var streams = List<VideoStream>.from(
        _streamsByVariant[_selectedVariant] ?? const <VideoStream>[]);
    if (streams.isEmpty) {
      setState(() => _resolving = true);
      try {
        await _resolveVariant(_selectedVariant);
        streams = List<VideoStream>.from(
            _streamsByVariant[_selectedVariant] ?? const <VideoStream>[]);
        if (streams.isEmpty) {
          final m3u8Url = _avData?['m3u8'] as String?;
          if (m3u8Url == null || m3u8Url.isEmpty) {
            if (mounted) {
              ScaffoldMessenger.of(context).showSnackBar(
                SnackBar(
                    content: Text(_streamError != null
                        ? '视频源不可用: $_streamError'
                        : '视频源不可用')),
              );
            }
            return;
          }
          streams.add(VideoStream(url: m3u8Url));
        }
      } finally {
        if (mounted) setState(() => _resolving = false);
      }
    }

    final title = _movieData?['title'] as String? ?? 'Video';
    final Widget screen;
    if (kIsWeb) {
      // 浏览器无法播 HLS 且无法伪造 Referer：播放器内部走后端 HLS 代理 + hls.js
      screen = HlsPlayerScreen(streams: streams, title: title);
    } else {
      // 原生播放器（ExoPlayer）原生支持 HLS，可带 Referer 头直连
      screen = VideoPlayerScreen(
        streams: streams,
        title: title,
        movieId: widget.movieId,
        movieNumber: _movieData?['number'] as String? ?? '',
        cover: _movieData?['cover_url'] as String? ?? '',
      );
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

  /// 收藏按钮：加入收藏夹（复用订阅存储 kind=movie，与订阅相互独立）。
  Widget _buildFavButton(BuildContext context) {
    final provider = context.watch<SubscriptionProvider>();
    final number =
        _movieData?['number'] as String? ?? widget.movieNumber;
    final cover = _movieData?['cover_url'] as String?;
    final fav = provider.isSubscribed(kSubMovie, widget.movieId);
    return IconButton(
      icon: Icon(
        fav ? Icons.favorite : Icons.favorite_border,
        color: fav ? Colors.redAccent : null,
      ),
      tooltip: fav ? '取消收藏' : '收藏',
      onPressed: () async {
        try {
          if (fav) {
            await provider.unsubscribe(kSubMovie, widget.movieId);
          } else {
            await provider.favoriteMovie(widget.movieId, number, cover: cover);
          }
          if (context.mounted) {
            ScaffoldMessenger.of(context).showSnackBar(SnackBar(
              content: Text(fav ? '已取消收藏' : '已加入收藏'),
              duration: const Duration(seconds: 1),
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
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(_movieData?['number'] as String? ?? '电影详情'),
        actions: [
          if (_movieData != null) _buildFavButton(context),
        ],
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

  /// 画廊：封面在前，预览大图随后，左右滑动切换；点按进入全屏查看器。
  Widget _buildGallery(Map<String, dynamic> movie) {
    final urls = <String>[];
    final cover = movie['cover_url'] as String?;
    if (cover != null && cover.isNotEmpty) urls.add(cover);
    final previews =
        (movie['preview_images'] as List<dynamic>?)?.cast<String>() ??
            const <String>[];
    urls.addAll(previews.where((u) => u.isNotEmpty));
    if (urls.isEmpty) return const SizedBox.shrink();

    return Stack(
      children: [
        SizedBox(
          height: 300,
          child: PageView.builder(
            itemCount: urls.length,
            onPageChanged: (i) => setState(() => _galleryPage = i),
            itemBuilder: (context, i) {
              return GestureDetector(
                onTap: () => _openImageViewer(urls, i),
                child: Center(
                  child: Image.network(
                    resolveImageUrl(urls[i]),
                    fit: BoxFit.contain,
                    errorBuilder: (_, __, ___) =>
                        const Icon(Icons.movie, size: 100),
                  ),
                ),
              );
            },
          ),
        ),
        // 页码指示器：固定右上角，不随图片数量变长。
        if (urls.length > 1)
          Positioned(
            right: 12,
            bottom: 12,
            child: Container(
              padding:
                  const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
              decoration: BoxDecoration(
                color: Colors.black45,
                borderRadius: BorderRadius.circular(12),
              ),
              child: Text(
                '${_galleryPage + 1} / ${urls.length}',
                style: const TextStyle(color: Colors.white, fontSize: 12),
              ),
            ),
          ),
      ],
    );
  }

  void _openImageViewer(List<String> urls, int initial) {
    Navigator.push(
      context,
      MaterialPageRoute(
        builder: (_) => _ImageViewerScreen(urls: urls, initial: initial),
      ),
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
                child: SelectableText(
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
          SelectableText(
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
          // 片源/变体选择器单独一行：与按钮同排时手机窄屏会把按钮挤压到不可用
          Row(
            children: [
              Flexible(child: _buildSourceSelector()),
              const SizedBox(width: 8),
              Flexible(child: _buildVariantSelector()),
            ],
          ),
          const SizedBox(height: 10),
          // 播放/下载按钮独占一行等宽展示；AV 流在后台解析，点击播放时如未就绪会现场补拉
          Row(
            children: [
              Expanded(
                child: FilledButton.icon(
                  onPressed: _resolving ? null : _playVideo,
                  icon: _resolving
                      ? const SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.play_arrow),
                  label: const Text('播放'),
                  style: FilledButton.styleFrom(
                    padding: const EdgeInsets.symmetric(vertical: 14),
                    shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(14)),
                  ),
                ),
              ),
              const SizedBox(width: 12),
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
          const SizedBox(height: 24),
          // 相似推荐（同女演员 / 同系列 / 同题材，后端聚合打分）
          _buildSimilarSection(),
          const SizedBox(height: 24),
          // JavDB 用户评论（最热/最新排序）
          _buildReviewsSection(),
        ],
      ),
    );
  }

  /// 相似推荐区块：横向海报卡，右上角小徽章标注推荐理由；空结果整块隐藏。
  Widget _buildSimilarSection() {
    if (_similar.isEmpty) return const SizedBox.shrink();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Text('相似推荐',
            style: TextStyle(fontSize: 16, fontWeight: FontWeight.bold)),
        const SizedBox(height: 8),
        SizedBox(
          height: 216,
          child: ListView.separated(
            scrollDirection: Axis.horizontal,
            itemCount: _similar.length,
            separatorBuilder: (_, __) => const SizedBox(width: 10),
            itemBuilder: (context, i) {
              final item = _similar[i];
              final movie = Movie.fromJson(
                  item['movie'] as Map<String, dynamic>? ?? const {});
              final reason = item['reason'] as String? ?? '';
              return SizedBox(
                width: 112,
                child: Stack(
                  children: [
                    Positioned.fill(child: MovieGridCard(movie: movie)),
                    if (reason.isNotEmpty)
                      Positioned(
                        top: 6,
                        right: 6,
                        child: Tooltip(
                          message: reason,
                          child: ConstrainedBox(
                            constraints: const BoxConstraints(maxWidth: 92),
                            child: Container(
                              padding: const EdgeInsets.symmetric(
                                  horizontal: 5, vertical: 2),
                              decoration: BoxDecoration(
                                color: Colors.black54,
                                borderRadius: BorderRadius.circular(6),
                              ),
                              child: Text(
                                reason,
                                maxLines: 2,
                                overflow: TextOverflow.ellipsis,
                                style: const TextStyle(
                                    color: Colors.white, fontSize: 9),
                              ),
                            ),
                          ),
                        ),
                      ),
                  ],
                ),
              );
            },
          ),
        ),
      ],
    );
  }

  /// JavDB 评论：与详情主内容并行拉取；失败或无评论整块隐藏。
  Future<void> _loadReviews() async {
    setState(() => _reviewsLoading = true);
    try {
      final result =
          await _client.getReviews(widget.movieId, sort: _reviewSort);
      final list = (result['reviews'] as List?)
              ?.whereType<Map<String, dynamic>>()
              .toList() ??
          const <Map<String, dynamic>>[];
      if (!mounted) return;
      setState(() {
        _reviews = list;
        // app API 的 total 不可靠（可能为 0），取两者较大值保底展示数量
        _reviewTotal =
            ((result['total'] as num?)?.toInt() ?? 0) > list.length
                ? (result['total'] as num).toInt()
                : list.length;
        _reviewsLoading = false;
      });
    } catch (e) {
      AppLogger.warning('Failed to load reviews: $e');
      if (!mounted) return;
      setState(() => _reviewsLoading = false);
    }
  }

  /// 评论区块：标题 + 最热/最新切换 + 评论卡片列；无数据整块隐藏。
  Widget _buildReviewsSection() {
    if (_reviews.isEmpty && !_reviewsLoading) return const SizedBox.shrink();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Text(_reviewTotal > 0 ? '评论 ($_reviewTotal)' : '评论',
                style: const TextStyle(
                    fontSize: 16, fontWeight: FontWeight.bold)),
            const Spacer(),
            SegmentedButton<String>(
              segments: const [
                ButtonSegment(value: 'hotly', label: Text('最热')),
                ButtonSegment(value: 'latest', label: Text('最新')),
              ],
              selected: {_reviewSort},
              showSelectedIcon: false,
              style: const ButtonStyle(
                visualDensity: VisualDensity.compact,
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
              onSelectionChanged: (sel) {
                if (sel.first == _reviewSort) return;
                setState(() {
                  _reviewSort = sel.first;
                });
                _loadReviews();
              },
            ),
          ],
        ),
        const SizedBox(height: 8),
        if (_reviewsLoading)
          const Center(
            child: Padding(
              padding: EdgeInsets.all(12),
              child: SizedBox(
                  width: 22,
                  height: 22,
                  child: CircularProgressIndicator(strokeWidth: 2)),
            ),
          )
        else
          ..._reviews.map(_buildReviewCard),
      ],
    );
  }

  /// 单条评论卡：作者/评分/内容/日期与点赞数。
  Widget _buildReviewCard(Map<String, dynamic> review) {
    final author = review['author'] as String? ?? '匿名';
    final content = review['content'] as String? ?? '';
    final date = review['date'] as String? ?? '';
    final rating = (review['rating'] as num?)?.toDouble() ?? 0;
    final likes = (review['likes'] as num?)?.toInt() ?? 0;
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(bottom: 10),
      decoration: cardDecoration(context),
      padding: const EdgeInsets.all(12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              CircleAvatar(
                radius: 12,
                backgroundColor: Theme.of(context)
                    .colorScheme
                    .primary
                    .withOpacity(0.15),
                child: Text(
                    author.isNotEmpty ? author.substring(0, 1) : '?',
                    style: const TextStyle(fontSize: 11)),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(author,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                        fontWeight: FontWeight.w600, fontSize: 13)),
              ),
              if (rating > 0)
                Container(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 8, vertical: 3),
                  decoration: BoxDecoration(
                    color: Colors.orange,
                    borderRadius: BorderRadius.circular(10),
                  ),
                  child: Text(
                    '★ ${rating.toStringAsFixed(1)}',
                    style: const TextStyle(
                        color: Colors.white,
                        fontSize: 11,
                        fontWeight: FontWeight.w700),
                  ),
                ),
            ],
          ),
          if (content.isNotEmpty) ...[
            const SizedBox(height: 8),
            SelectableText(content,
                style: const TextStyle(fontSize: 13, height: 1.5)),
          ],
          if (date.isNotEmpty || likes > 0)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Row(
                children: [
                  if (date.isNotEmpty)
                    Text(date,
                        style: TextStyle(
                            color: Theme.of(context).hintColor,
                            fontSize: 11)),
                  const Spacer(),
                  if (likes > 0)
                    Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Icon(Icons.thumb_up_alt_outlined,
                            size: 13, color: Theme.of(context).hintColor),
                        const SizedBox(width: 4),
                        Text('$likes',
                            style: TextStyle(
                                color: Theme.of(context).hintColor,
                                fontSize: 11)),
                      ],
                    ),
                ],
              ),
            ),
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
              child: SelectableText(value,
                  style: const TextStyle(fontWeight: FontWeight.w500))),
        ],
      ),
    );
  }
}

/// 全屏图片查看器：黑底 PageView 翻页，双指缩放拖动查看细节。
class _ImageViewerScreen extends StatefulWidget {
  final List<String> urls;
  final int initial;

  const _ImageViewerScreen({required this.urls, required this.initial});

  @override
  State<_ImageViewerScreen> createState() => _ImageViewerScreenState();
}

class _ImageViewerScreenState extends State<_ImageViewerScreen> {
  late int _page = widget.initial;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: Colors.black,
        foregroundColor: Colors.white,
        title: Text('${_page + 1} / ${widget.urls.length}'),
      ),
      body: PageView.builder(
        itemCount: widget.urls.length,
        controller: PageController(initialPage: widget.initial),
        onPageChanged: (i) => setState(() => _page = i),
        itemBuilder: (context, i) {
          return InteractiveViewer(
            maxScale: 5,
            child: Center(
              child: Image.network(
                resolveImageUrl(widget.urls[i]),
                fit: BoxFit.contain,
                errorBuilder: (_, __, ___) => const Icon(Icons.broken_image,
                    size: 80, color: Colors.white38),
              ),
            ),
          );
        },
      ),
    );
  }
}
