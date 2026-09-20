import 'dart:async';

import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart' show Clipboard, ClipboardData;
import 'package:provider/provider.dart';
import '../api/client.dart';
import '../api/models.dart';
import '../providers/user_state_provider.dart';
import '../services/backend_launcher.dart';
import '../services/data_cache.dart';
import '../services/history.dart';
import '../services/image_url.dart';
import '../services/logger.dart';
import '../services/subtitle_service.dart';
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
  // JavDB 用户评论：按需分页拉取（首屏只拉一页，滚动加载更多，无上限）。
  // hotly 用服务端热度序（各页全局有序，追加即保序）；
  // latest 对累计数据本地重排（加载越多越准）。
  List<Map<String, dynamic>> _reviews = [];
  List<Map<String, dynamic>> _reviewsRaw = [];
  int _reviewTotal = 0; // 已加载条数
  int _serverReviewTotal = 0; // 服务端报告的真实总数（0 = 未知）
  int _reviewPage = 0;
  bool _hasMoreReviews = false;
  bool _reviewsLoadingMore = false;
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
  // 所有源都探测不到可用变体（或解析全部失败）时为 true，展示“无播放源”。
  bool _noSource = false;
  // 探测/解析的世代计数：切源或重新加载后作废旧异步结果，防止竞态回写。
  int _probeGeneration = 0;
  int _resolveGeneration = 0;
  String _selectedSource = '';
  String? _streamError; // 片源解析错误信息
  bool _resolving = false; // 点击播放后按需解析中的加载态（按钮图标）
  bool _isLoading = true;
  String? _error;
  int _galleryPage = 0;
  // 字幕预加载：进入详情即后台拉取，播放时已就绪（服务内去重/缓存）。
  LoadedSubtitle? _subtitle;
  bool _subtitleLoading = false;

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
    _preloadSubtitle();
  }

  /// 预加载字幕（详情页展示"已加载"提示，播放器直接复用同一份缓存）。
  void _preloadSubtitle([String? number]) {
    final code = (number ?? widget.movieNumber).trim();
    if (code.isEmpty || _subtitle != null) return;
    setState(() => _subtitleLoading = true);
    SubtitleService.instance.load(code).then((ls) {
      if (!mounted) return;
      setState(() {
        _subtitle = ls;
        _subtitleLoading = false;
      });
    });
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
    // 先查缓存（{movie, magnets} 原始 JSON）：命中即秒出，后台再拉
    // 最新数据刷新（stale-while-revalidate）；未命中走正常加载。
    try {
      final cached = await DataCache.instance
          .read('movie.${widget.movieId}', maxAge: const Duration(hours: 6));
      if (cached is Map && cached['movie'] is Map) {
        final result = (cached).cast<String, dynamic>();
        if (!mounted) return;
        setState(() => _applyMovieData(result));
        _loadMovieFresh();
        return;
      }
    } catch (_) {}
    await _loadMovieFresh();
  }

  /// 从网络拉最新详情并写缓存；已有缓存内容时失败静默。
  Future<void> _loadMovieFresh() async {
    try {
      AppLogger.info('Loading movie: ${widget.movieId}');
      final result = await _client.getMovie(widget.movieId);
      unawaited(DataCache.instance
          .write('movie.${widget.movieId}', result));
      if (!mounted) return;
      setState(() => _applyMovieData(result));
    } catch (e) {
      if (mounted && _movieData == null) {
        setState(() {
          _error = e.toString();
          _isLoading = false;
        });
      }
      AppLogger.error('Failed to load movie', e);
    }
  }

  /// 应用详情数据（缓存/网络同一路径）：解析磁链、更新历史、预取画廊图。
  void _applyMovieData(Map<String, dynamic> result) {
    final magnetsList = <Magnet>[];

    // Handle both single magnet (Map) and list of magnets
    final magnetsData = result['magnets'];
    if (magnetsData != null) {
      if (magnetsData is List) {
        magnetsList.addAll(magnetsData
            .map((m) => Magnet.fromJson((m as Map).cast<String, dynamic>())));
      } else if (magnetsData is Map) {
        magnetsList
            .add(Magnet.fromJson(magnetsData.cast<String, dynamic>()));
      }
    }

    _movieData = result['movie'] as Map<String, dynamic>?;
    _magnets = magnetsList;
    _isLoading = false;
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
    // movieNumber 为空进入时，用详情返回的番号补拉一次字幕。
    final num = (m?['number'] as String?)?.trim() ?? '';
    if (num.isNotEmpty && num != widget.movieNumber.trim()) {
      _preloadSubtitle(num);
    }
    _precacheGallery();
  }

  /// 画廊全部图 URL（封面在前，预览大图随后）。
  List<String> _galleryUrls() {
    final m = _movieData;
    if (m == null) return const [];
    final urls = <String>[];
    final cover = m['cover_url'] as String?;
    if (cover != null && cover.isNotEmpty) urls.add(cover);
    final previews =
        (m['preview_images'] as List<dynamic>?)?.cast<String>() ??
            const <String>[];
    urls.addAll(previews.where((u) => u.isNotEmpty));
    return urls;
  }

  /// 预取画廊前几张图（cached_network_image 落盘，二次进入零等待）。
  void _precacheGallery() {
    if (!mounted) return;
    final urls = _galleryUrls();
    for (final u in urls.take(4)) {
      precacheImage(
          CachedNetworkImageProvider(resolveImageUrl(u)), context);
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
        // 先展示占位变体，随后用搜索接口逐源探测真实变体并刷新按钮
        _availableVariants = _defaultVariants;
        _noSource = false;
        _streamError = null;
      });
      AppLogger.info('AV data loaded, m3u8: ${_avData?['m3u8']}');
      // 提前探测变体：不阻塞详情页，结果回来即更新
      _probeVariants();
    } catch (e) {
      AppLogger.error('AV data not available', e);
    }
  }

  /// 逐源探测可用变体：当前源返回空/失败即换下一源（missav → jable →
  /// hohoj），全部无结果时进入“无播放源”状态。探测不阻塞详情页。
  Future<void> _probeVariants() async {
    final number = widget.movieNumber;
    if (number.isEmpty) return;
    final gen = ++_probeGeneration;
    final srcAtStart = _selectedSource;
    // 探测顺序：当前选中源优先，其余按 _availableSources 顺序
    final order = _sourceOrder(startWith: srcAtStart);
    for (final source in order) {
      Map<String, dynamic> result;
      try {
        result = await _client.avProbe(number, source: source);
      } catch (e) {
        AppLogger.info('Probe $number on $source failed: $e');
        continue; // 探测失败视为此源不可用，换下一源
      }
      if (gen != _probeGeneration || !mounted) return; // 已被新探测/切源作废
      final kinds = _kindsFromProbe(result);
      if (kinds.isEmpty) continue; // 此源无此片，换下一源
      setState(() {
        if (_selectedSource != source) _selectedSource = source;
        _availableVariants = kinds;
        if (!kinds.contains(_selectedVariant)) {
          _selectedVariant = kinds.first;
        }
        _noSource = false;
      });
      AppLogger.info('Probed variants for $number on $source: $kinds');
      return;
    }
    if (gen != _probeGeneration || !mounted) return;
    setState(() => _noSource = true); // 所有源都没有可用变体
    AppLogger.info('No playable source for $number');
  }

  /// 源尝试顺序：startWith（当前源）优先，其余按 _availableSources 顺序。
  List<String> _sourceOrder({String? startWith}) {
    final cur = startWith ?? '';
    return [
      if (cur.isNotEmpty) cur,
      ..._availableSources.where((s) => s != cur),
    ];
  }

  /// 从 probe 响应提取可用变体（按 无码→中字→原片 排序）。
  static List<String> _kindsFromProbe(Map<String, dynamic> result) {
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
    return kinds;
  }

  /// 按需拉取指定源+变体的播放流（惰性加载），结果存入 _streamsByVariant。
  /// 失败只记 _streamError（由调用方决定是否级联下一源/提示）。
  Future<void> _resolveVariantOn(String source, String variant) async {
    try {
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
      AppLogger.info('Resolved $variant on $source: ${sorted.length} streams');
    } catch (e) {
      setState(() => _streamError = e.toString());
      AppLogger.error('Resolve variant $variant on $source failed', e);
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
      _noSource = false;
      _streamError = null;
      _probeGeneration++; // 作废旧探测循环，避免旧源结果回写
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
  /// 所有源都探测/解析失败时，不再展示占位变体（避免误以为有可用变体），
  /// 改为「无播放源」提示。
  Widget _buildVariantSelector() {
    if (_noSource) {
      return Container(
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
            Icon(Icons.videocam_off_outlined,
                size: 18, color: Theme.of(context).colorScheme.error),
            const SizedBox(width: 6),
            Text('无播放源',
                style: TextStyle(
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                    color: Theme.of(context).colorScheme.error)),
          ],
        ),
      );
    }
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
    // 惰性加载：如果该变体的流尚未解析，先逐源级联解析（当前源优先，
    // 失败/无流自动换下一源）；全部失败再试 avDetail 带回的直连 m3u8，
    // 仍无流则提示无播放源。
    var streams = List<VideoStream>.from(
        _streamsByVariant[_selectedVariant] ?? const <VideoStream>[]);
    if (streams.isEmpty) {
      setState(() => _resolving = true);
      try {
        final gen = ++_resolveGeneration;
        for (final source in _sourceOrder(startWith: _selectedSource)) {
          await _resolveVariantOn(source, _selectedVariant);
          if (gen != _resolveGeneration || !mounted) return;
          streams = List<VideoStream>.from(
              _streamsByVariant[_selectedVariant] ?? const <VideoStream>[]);
          if (streams.isNotEmpty) {
            // 级联换源成功时让片源选择器跟随实际生效的源
            if (_selectedSource != source) {
              setState(() => _selectedSource = source);
            }
            break;
          }
        }
        if (streams.isEmpty) {
          final m3u8Url = _avData?['m3u8'] as String?;
          if (m3u8Url != null && m3u8Url.isNotEmpty) {
            streams.add(VideoStream(url: m3u8Url));
          }
        }
        if (streams.isEmpty && mounted) {
          setState(() => _noSource = true);
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(
                content: Text(_streamError != null
                    ? '所有视频源均无可用播放流（最后错误: $_streamError）'
                    : '所有视频源均无可用播放流')),
          );
          return;
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

  /// 收藏按钮：♥收藏 = JavDB "想看"标记（与登录账号双向同步，本地缓存）。
  Widget _buildFavButton(BuildContext context) {
    final userState = context.watch<UserStateProvider>();
    final movie = _movieData ?? {'id': widget.movieId, 'number': widget.movieNumber};
    final fav = userState.isWantWatch(widget.movieId);
    return IconButton(
      icon: Icon(
        fav ? Icons.favorite : Icons.favorite_border,
        color: fav ? Colors.redAccent : null,
      ),
      tooltip: fav ? '取消想看' : '想看',
      onPressed: () => _toggleMark(context, movie, kMarkWantWatch),
    );
  }

  /// 看过按钮：JavDB "看过"标记（与登录账号双向同步）。
  Widget _buildWatchedButton(BuildContext context) {
    final userState = context.watch<UserStateProvider>();
    final movie = _movieData ?? {'id': widget.movieId, 'number': widget.movieNumber};
    final watched = userState.isWatched(widget.movieId);
    return IconButton(
      icon: Icon(
        watched ? Icons.check_circle : Icons.check_circle_outline,
        color: watched ? Colors.green : null,
      ),
      tooltip: watched ? '取消看过' : '看过',
      onPressed: () => _toggleMark(context, movie, kMarkWatched),
    );
  }

  Future<void> _toggleMark(
      BuildContext context, Map<String, dynamic> movie, String status) async {
    final label = status == kMarkWantWatch ? '想看' : '看过';
    try {
      final result =
          await context.read<UserStateProvider>().toggleMark(movie, status);
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(result == null ? '已取消$label' : '已标记$label'),
        duration: const Duration(seconds: 1),
      ));
    } catch (e) {
      if (!context.mounted) return;
      final needLogin = e.toString().contains('login required');
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(needLogin ? '需要登录 JavDB 账号' : '操作失败: $e'),
        backgroundColor: needLogin ? Colors.orange : Colors.red,
      ));
    }
  }

  /// 清单按钮：展示我的清单及该影片的在列状态。
  /// 移动端 API 仅支持从清单移除（无加入端点），未在列的清单仅供查看。
  Widget _buildListButton(BuildContext context) {
    return IconButton(
      icon: const Icon(Icons.playlist_add),
      tooltip: '清单',
      onPressed: _movieData == null ? null : () => _showListDialog(context),
    );
  }

  Future<void> _showListDialog(BuildContext context) async {
    final userState = context.read<UserStateProvider>();
    // 带 movieId 拉取，拿到每个清单的 has_movie 状态。
    await userState.refreshLists(movieId: widget.movieId);
    if (!context.mounted) return;
    final lists = userState.lists;

    await showModalBottomSheet<void>(
      context: context,
      builder: (sheetContext) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              title: Text('影片清单',
                  style: Theme.of(sheetContext).textTheme.titleMedium),
              trailing: IconButton(
                icon: const Icon(Icons.close),
                onPressed: () => Navigator.of(sheetContext).pop(),
              ),
            ),
            const Divider(height: 1),
            if (lists.isEmpty)
              const Padding(
                padding: EdgeInsets.all(24),
                child: Text('暂无清单（需登录）'),
              )
            else
              Flexible(
                child: ListView.builder(
                  shrinkWrap: true,
                  itemCount: lists.length,
                  itemBuilder: (_, i) {
                    final list = lists[i];
                    final id = (list['id'] as String?) ?? '';
                    final name = (list['name'] as String?) ?? '';
                    final inList = list['has_movie'] == true;
                    return ListTile(
                      leading: Icon(
                        inList ? Icons.playlist_add_check : Icons.playlist_play,
                        color: inList ? Colors.green : null,
                      ),
                      title: Text(name),
                      subtitle: Text(inList ? '已在清单（点按移除）' : '不在清单'),
                      onTap: inList
                          ? () async {
                              try {
                                await userState.removeMovieFromList(
                                    id, name, widget.movieId);
                                if (sheetContext.mounted) {
                                  Navigator.of(sheetContext).pop();
                                  ScaffoldMessenger.of(context).showSnackBar(
                                    SnackBar(
                                        content: Text('已从「$name」移除'),
                                        duration:
                                            const Duration(seconds: 1)),
                                  );
                                }
                              } catch (e) {
                                if (sheetContext.mounted) {
                                  ScaffoldMessenger.of(sheetContext)
                                      .showSnackBar(SnackBar(
                                    content: Text('移除失败：$e'),
                                    backgroundColor: Colors.red,
                                  ));
                                }
                              }
                            }
                          : null,
                    );
                  },
                ),
              ),
            const Divider(height: 1),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
              child: Text(
                '移动端接口暂不支持从 App 加入清单，可在 JavDB 客户端添加后再此管理',
                style: TextStyle(
                    fontSize: 12, color: Theme.of(sheetContext).hintColor),
              ),
            ),
          ],
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(_movieData?['number'] as String? ?? '电影详情'),
        actions: [
          if (_movieData != null) ...[
            _buildFavButton(context),
            _buildWatchedButton(context),
            _buildListButton(context),
          ],
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
            onPageChanged: (i) {
              setState(() => _galleryPage = i);
              // 翻页时预取后两张，继续滑动即点即显
              for (var k = i + 1; k <= i + 2 && k < urls.length; k++) {
                precacheImage(
                    CachedNetworkImageProvider(resolveImageUrl(urls[k])),
                    context);
              }
            },
            itemBuilder: (context, i) {
              return GestureDetector(
                onTap: () => _openImageViewer(urls, i),
                child: Center(
                  child: CachedNetworkImage(
                    imageUrl: resolveImageUrl(urls[i]),
                    fit: BoxFit.contain,
                    placeholder: (_, __) => const Center(
                      child: SizedBox(
                          width: 22,
                          height: 22,
                          child:
                              CircularProgressIndicator(strokeWidth: 2)),
                    ),
                    errorWidget: (_, __, ___) =>
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
          // 字幕预加载状态提示：成功绿字、加载中灰字，失败不占位
          if (_subtitle != null || _subtitleLoading)
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: Row(
                children: [
                  Icon(
                    _subtitle != null
                        ? Icons.closed_caption
                        : Icons.hourglass_empty,
                    size: 15,
                    color: _subtitle != null ? Colors.green : Colors.grey,
                  ),
                  const SizedBox(width: 5),
                  Text(
                    _subtitle != null
                        ? '字幕已加载 · ${_subtitle!.langLabel} · 播放时自动显示'
                        : '字幕加载中…',
                    style: TextStyle(
                      fontSize: 12,
                      color: _subtitle != null ? Colors.green : Colors.grey,
                    ),
                  ),
                ],
              ),
            ),
          // 播放/下载按钮独占一行等宽展示；AV 流在后台解析，点击播放时如未就绪会现场补拉
          Row(
            children: [
              Expanded(
                child: FilledButton.icon(
                  onPressed: (_resolving || _noSource) ? null : _playVideo,
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

  /// JavDB 评论：与详情主内容并行拉取；首屏只拉第一页，其余按需加载。
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
        _reviews = _reviewsRaw = list;
        _reviewPage = 1;
        _reviewTotal = list.length;
        _serverReviewTotal = (result['total'] as num?)?.toInt() ?? 0;
        _hasMoreReviews = list.length >= 10;
        _reviewsLoading = false;
      });
    } catch (e) {
      AppLogger.warning('Failed to load reviews: $e');
      if (!mounted) return;
      setState(() => _reviewsLoading = false);
    }
  }

  /// 加载下一页评论并入累计列表（按 id 去重），随后按当前排序方式重排。
  Future<void> _loadMoreReviews() async {
    if (_reviewsLoadingMore || !_hasMoreReviews) return;
    setState(() => _reviewsLoadingMore = true);
    try {
      final result = await _client.getReviews(widget.movieId,
          sort: _reviewSort, page: _reviewPage + 1);
      final more = (result['reviews'] as List?)
              ?.whereType<Map<String, dynamic>>()
              .toList() ??
          const <Map<String, dynamic>>[];
      if (!mounted) return;
      setState(() {
        _reviewPage += 1;
        final seen = _reviewsRaw.map((r) => (r['id'] as String?) ?? '').toSet();
        final fresh = more
            .where((r) => !seen.contains((r['id'] as String?) ?? ''))
            .toList();
        _reviewsRaw = [..._reviewsRaw, ...fresh];
        _reviewTotal = _reviewsRaw.length;
        final srv = (result['total'] as num?)?.toInt() ?? 0;
        if (srv > 0) _serverReviewTotal = srv;
        _hasMoreReviews = fresh.isNotEmpty &&
            more.length >= 10 &&
            (_serverReviewTotal <= 0 || _reviewTotal < _serverReviewTotal);
        _applyReviewSortQuiet();
        _reviewsLoadingMore = false;
      });
    } catch (e) {
      AppLogger.warning('Failed to load more reviews: $e');
      if (!mounted) return;
      setState(() => _reviewsLoadingMore = false);
    }
  }

  /// 排序切换：hotly 恢复服务端热度序（_reviewsRaw 的累计顺序即热度序）；
  /// latest 按日期降序重排。评论全量已在服务端按热度全局排序，
  /// 追加页天然保序，无需重新请求。
  void _applyReviewSort() {
    setState(_applyReviewSortQuiet);
  }

  void _applyReviewSortQuiet() {
    if (_reviewSort == 'latest') {
      final list = [..._reviewsRaw];
      list.sort((a, b) {
        final da = a['date'] as String? ?? '';
        final db = b['date'] as String? ?? '';
        return db.compareTo(da);
      });
      _reviews = list;
    } else {
      _reviews = List.of(_reviewsRaw);
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
            Text(_serverReviewTotal > 0
                    ? '评论 ($_serverReviewTotal)'
                    : (_reviewTotal > 0 ? '评论 ($_reviewTotal)' : '评论'),
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
                _applyReviewSort(); // 累计数据本地重排，无需重拉
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
        else ...[
          ..._reviews.map(_buildReviewCard),
          if (_hasMoreReviews)
            Center(
              child: TextButton.icon(
                onPressed: _reviewsLoadingMore ? null : _loadMoreReviews,
                icon: _reviewsLoadingMore
                    ? const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(strokeWidth: 2))
                    : const Icon(Icons.expand_more, size: 18),
                label: Text(_reviewsLoadingMore
                    ? '加载中...'
                    : '加载更多评论 ($_reviewTotal${_serverReviewTotal > 0 ? '/$_serverReviewTotal' : ''})'),
              ),
            )
          else if (_reviews.length >= 10)
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: Center(
                child: Text('已加载全部 $_reviewTotal 条评论',
                    style: TextStyle(
                        fontSize: 12,
                        color: Theme.of(context).colorScheme.outline)),
              ),
            ),
        ],
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
              child: CachedNetworkImage(
                imageUrl: resolveImageUrl(widget.urls[i]),
                fit: BoxFit.contain,
                placeholder: (_, __) => const Center(
                    child: CircularProgressIndicator(strokeWidth: 2)),
                errorWidget: (_, __, ___) => const Icon(Icons.broken_image,
                    size: 80, color: Colors.white38),
              ),
            ),
          );
        },
      ),
    );
  }
}
