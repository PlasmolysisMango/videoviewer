import 'dart:async';
import 'dart:math';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../api/models.dart';
import '../providers/user_state_provider.dart';
import '../services/history.dart';
import '../services/logger.dart';
import '../services/player_engine.dart';
import '../services/subtitle_service.dart';
import '../widgets/player_control_bar.dart';
import '../widgets/player_gestures.dart';
import '../widgets/subtitle_panel.dart';

/// 播放默认设置（app 启动时预读，播放页同步读取）：
/// 默认清晰度上限（px），0 = 不限（保持排序默认）。
class PlayerDefaults {
  static int maxHeight = 0;

  static Future<void> load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      maxHeight = prefs.getInt('default_max_height') ?? 0;
    } catch (_) {}
  }
}

/// 原生端 HLS 播放器（video_player / ExoPlayer 原生支持 HLS，可带 Referer 直连）。
/// 支持清晰度切换（重初始化并保留进度）、倍速、手势（音量/亮度/进度）、横屏全屏。
class VideoPlayerScreen extends StatefulWidget {
  final List<VideoStream> streams;
  final String title;

  /// 影片元信息：用于观影历史进度记录（空则不记录）。
  final String movieId;
  final String movieNumber;
  final String cover;

  const VideoPlayerScreen({
    super.key,
    required this.streams,
    required this.title,
    this.movieId = '',
    this.movieNumber = '',
    this.cover = '',
  });

  @override
  State<VideoPlayerScreen> createState() => _VideoPlayerScreenState();
}

class _VideoPlayerScreenState extends State<VideoPlayerScreen> {
  late List<VideoStream> _streams;
  int _current = 0;
  PlayerEngine? _controller;
  Duration? _seekPending;

  bool _playing = false;
  Duration _position = Duration.zero;
  Duration _duration = Duration.zero;
  double _volume = 1.0;
  double _brightness = 1.0;
  double _rate = 1.0;
  bool _loading = true;
  String? _error;
  bool _isFullscreen = false;
  bool _controlsVisible = true; // 单击视频区切换控制 UI（AppBar/控制栏）
  bool _subtitlePanelOpen = false; // 字幕设置右侧侧边栏（画面仍可见可对照调整）

  // tick 节流：ExoPlayer/mpv 的事件频率远高于进度条需要，全页 setState
  // 过频会加重渲染压力（配合引擎层 view 实例缓存，共同消除播放中闪黑）。
  DateTime _lastTickAt = DateTime.fromMillisecondsSinceEpoch(0);

  // 字幕：详情页/播放器都会触发加载（服务内去重），就绪后 overlay 渲染。
  LoadedSubtitle? _subtitle;

  // 画面实例代际：seek/切全屏后自增，重建 TextureLayer 强制重新取帧。
  // ExoPlayer seek 后视频轨重新输出，但长期复用的 TextureLayer 在
  // 部分设备上不重新合成（黑帧滞留、声音正常）；换 Key 重挂子树
  // 让新 TextureLayer 拿到 seek 后的帧。平时 key 不变，零重建。
  int _viewEpoch = 0;
  Timer? _nudgeDebounce;

  void _nudgeView() {
    if (!mounted) return;
    setState(() => _viewEpoch++);
  }

  /// 连续手势 seek 防抖：滑动中频繁 seek 只在停顿后重建一次。
  void _scheduleNudge() {
    _nudgeDebounce?.cancel();
    _nudgeDebounce = Timer(const Duration(milliseconds: 300), () {
      _nudgeView();
    });
  }

  // 自动标看过：进入时缓存 provider（dispose 阶段不能再读 context）。
  UserStateProvider? _userState;

  @override
  void initState() {
    super.initState();
    _userState = context.read<UserStateProvider>();
    _streams = VideoStream.sortStreams(widget.streams);
    _current = _pickDefaultStream(_streams);
    _initController();
    SubtitleService.instance.addListener(_onSubtitleChanged);
    if (widget.movieNumber.isNotEmpty) {
      SubtitleService.instance.load(widget.movieNumber).then((ls) {
        if (mounted && ls != null) _onSubtitleChanged();
      });
    }
  }

  void _onSubtitleChanged() {
    if (!mounted || widget.movieNumber.isEmpty) return;
    // load() 命中会话缓存时同步返回；设置变化时也借此触发重建。
    SubtitleService.instance.load(widget.movieNumber).then((ls) {
      if (mounted) setState(() => _subtitle = ls);
    });
  }

  /// 默认清晰度选择：设置"不限"时返回 0（保持排序默认——变体优先、
  /// 同变体高清晰度）；设置了上限则在不超过上限的片源里取最高的，
  /// 全部超上限时取最低档（最接近上限）。
  int _pickDefaultStream(List<VideoStream> streams) {
    final maxH = PlayerDefaults.maxHeight;
    if (maxH <= 0 || streams.isEmpty) return 0;
    var best = -1, bestH = -1;
    var lowest = 0, lowestH = 1 << 30;
    for (var i = 0; i < streams.length; i++) {
      final h = streams[i].qualityHeight ?? 0;
      if (h < lowestH) {
        lowestH = h;
        lowest = i;
      }
      if (h > 0 && h <= maxH && h > bestH) {
        bestH = h;
        best = i;
      }
    }
    return best >= 0 ? best : lowest;
  }

  Future<void> _initController() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    final old = _controller;
    _controller = null;

    final stream = _streams[_current];
    final headers = <String, String>{};
    if (stream.referer != null && stream.referer!.isNotEmpty) {
      headers['Referer'] = stream.referer!;
    }

    try {
      AppLogger.info('Initializing video: ${stream.url}');
      final c = await createPlayerEngine(url: stream.url, headers: headers);
      if (!mounted) {
        await c.dispose();
        await old?.dispose();
        return;
      }
      _controller = c;
      c.onTick = _onPlayerTick;
      await old?.dispose();
      await c.setVolume(_volume);
      await c.setRate(_rate);
      setState(() => _loading = false);
      final seek = _seekPending;
      if (seek != null) {
        await c.seek(seek);
        _seekPending = null;
      }
      await c.play();
    } catch (e) {
      AppLogger.error('Failed to initialize video', e);
      await old?.dispose();
      if (mounted) {
        setState(() {
          _error = e.toString();
          _loading = false;
        });
      }
    }
  }

  void _onPlayerTick() {
    final e = _controller;
    if (e == null || !mounted) return;
    // 节流到 ~10Hz：进度条足够平滑，避免高频全页重建。
    final now = DateTime.now();
    if (now.difference(_lastTickAt) < const Duration(milliseconds: 100)) {
      return;
    }
    _lastTickAt = now;
    setState(() {
      _position = e.position;
      _duration = e.duration;
      _playing = e.isPlaying;
    });
  }

  @override
  void dispose() {
    _nudgeDebounce?.cancel();
    // 观影历史：记录本次播放进度（秒），超过阈值标记为"看过"。
    if (widget.movieId.isNotEmpty) {
      HistoryService.recordProgress(
        id: widget.movieId,
        number: widget.movieNumber,
        title: widget.title,
        cover: widget.cover,
        seconds: _controller?.position.inSeconds ?? 0,
      );
    }
    // 设置开启时，播完（进度 ≥90%）自动加入 JavDB "看过"。
    // controller 即将销毁，先同步取出进度再异步判断。
    if (widget.movieId.isNotEmpty && _userState != null) {
      _autoMarkWatched(
        posSeconds: _controller?.position.inSeconds ?? 0,
        durSeconds: _controller?.duration.inSeconds ?? 0,
      );
    }
    // 退出时恢复竖屏允许 + 显示状态栏
    SystemChrome.setPreferredOrientations([
      DeviceOrientation.portraitUp,
      DeviceOrientation.landscapeLeft,
      DeviceOrientation.landscapeRight,
    ]);
    SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge);
    _controller?.onTick = null;
    _controller?.dispose();
    SubtitleService.instance.removeListener(_onSubtitleChanged);
    super.dispose();
  }

  /// 播放结束后自动加入"看过"（设置页开关控制；进度不足 90% 不标记）。
  Future<void> _autoMarkWatched(
      {required int posSeconds, required int durSeconds}) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      if (!(prefs.getBool('auto_mark_watched') ?? false)) return;
      if (durSeconds <= 0 || posSeconds < durSeconds * 0.9) return;
      await _userState!.markWatchedQuietly({
        'id': widget.movieId,
        'number': widget.movieNumber,
        'title': widget.title,
        'cover_url': widget.cover,
      });
    } catch (e) {
      AppLogger.warning('Auto mark watched failed: $e');
    }
  }

  // —— 控制动作 ——

  void _switchQuality(int idx) {
    if (idx == _current || idx < 0 || idx >= _streams.length) return;
    _seekPending = _controller?.position;
    setState(() => _current = idx);
    _initController();
  }

  void _togglePlay() {
    final c = _controller;
    if (c == null) return;
    c.isPlaying ? c.pause() : c.play();
  }

  void _toggleControls() => setState(() {
        _controlsVisible = !_controlsVisible;
        // 收起控制栏时同步收起字幕侧边栏
        if (!_controlsVisible) _subtitlePanelOpen = false;
      });

  void _seekRelative(double seconds) {
    final c = _controller;
    if (c == null) return;
    final target =
        c.position + Duration(milliseconds: (seconds * 1000).round());
    final clamped = Duration(
      milliseconds: target.inMilliseconds.clamp(0, c.duration.inMilliseconds),
    );
    c.seek(clamped);
    _scheduleNudge();
  }

  Future<void> _seekTo(Duration d) async {
    final c = _controller;
    if (c == null) return;
    await c.seek(d);
    // 点击进度条跳转后立即重建画面层（见 _viewEpoch 注释）。
    _nudgeView();
  }

  void _setRate(double r) {
    setState(() => _rate = r);
    _controller?.setRate(r);
  }

  void _setVolume(double v) {
    setState(() => _volume = v);
    _controller?.setVolume(v);
  }

  /// 字幕设置：播放画面右侧滑入侧边栏，左侧画面仍可见，调整实时对照。
  void _toggleSubtitlePanel() {
    setState(() => _subtitlePanelOpen = !_subtitlePanelOpen);
  }

  /// 字幕侧边栏（两个布局分支共用）：关闭时滑出屏幕右侧。
  Widget _buildSubtitleSidePanel() {
    return AnimatedPositioned(
      duration: const Duration(milliseconds: 220),
      curve: Curves.easeOutCubic,
      right: _subtitlePanelOpen ? 0 : -270,
      top: 0,
      bottom: 0,
      width: 260,
      child: SubtitleSidePanel(
        movieNumber: widget.movieNumber,
        currentSubtitleLabel: _subtitle == null
            ? '未加载'
            : '${_subtitle!.langLabel} · ${_subtitle!.source}',
        onClose: () => setState(() => _subtitlePanelOpen = false),
      ),
    );
  }

  /// 字幕 overlay：白字黑边 + 半透明底，随播放进度/偏移/字号实时变化。
  /// 放在亮度遮罩与手势层之下，IgnorePointer 不拦截任何手势。
  Widget _buildSubtitleOverlay() {
    final ls = _subtitle;
    final svc = SubtitleService.instance;
    if (ls == null || !svc.enabled) return const SizedBox.shrink();
    // 偏移语义：正值 = 字幕延后显示，故查找时把进度往回拨。
    final pos = _position - Duration(milliseconds: svc.offsetMs);
    final cue = SubtitleService.cueAt(ls.cues, pos);
    if (cue == null) return const SizedBox.shrink();
    return Positioned(
      left: 24,
      right: 24,
      bottom: _isFullscreen ? 76 : 10,
      child: IgnorePointer(
        child: Center(
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
            decoration: BoxDecoration(
              color: Colors.black.withValues(alpha: 0.45),
              borderRadius: BorderRadius.circular(6),
            ),
            child: Text(
              cue.text,
              textAlign: TextAlign.center,
              style: TextStyle(
                color: Color(svc.fontColor),
                fontSize: svc.fontSize,
                height: 1.35,
                shadows: const [
                  Shadow(offset: Offset(1, 1), blurRadius: 2),
                  Shadow(offset: Offset(-1, 1), blurRadius: 2),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  /// 切换全屏：横屏全屏 + 隐藏状态栏。
  /// 旋转导致 Flutter surface 尺寸交换；暂停-恢复促使 ExoPlayer 立即
  /// 重新输出一帧，避免旋转后 Texture 层停留黑帧。
  /// 画面 widget 的父链在普通/全屏两分支同构（见 _buildVideoArea），
  /// 同一实例被直接复用而非重建——reparent/unmount 也是黑帧来源。
  void _toggleFullscreen() {
    setState(() {
      _isFullscreen = !_isFullscreen;
      // 切换全屏时确保控制 UI 可见，方便用户退出全屏
      _controlsVisible = true;
    });
    final c = _controller;
    if (c != null && c.isPlaying) {
      unawaited(c.pause().then((_) => c.play()));
    }
    // 旋转重排同样可能让 TextureLayer 停留黑帧，换 Key 重建画面层。
    _nudgeView();
    if (_isFullscreen) {
      SystemChrome.setPreferredOrientations([
        DeviceOrientation.landscapeLeft,
        DeviceOrientation.landscapeRight,
      ]);
      SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersiveSticky);
    } else {
      SystemChrome.setPreferredOrientations([
        DeviceOrientation.portraitUp,
        DeviceOrientation.landscapeLeft,
        DeviceOrientation.landscapeRight,
      ]);
      SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge);
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = _controller;
    final initialized = c != null && c.initialized && _error == null;

    // 全屏模式：无 AppBar，视频填满屏幕，控制栏浮层
    if (_isFullscreen) {
      return Scaffold(
        backgroundColor: Colors.black,
        body: Stack(
          children: [
            _buildVideoArea(c, initialized),
            // 亮度遮罩
            Positioned.fill(
              child: IgnorePointer(
                child: Container(
                  color: Colors.black
                      .withOpacity((1 - _brightness).clamp(0.0, 0.9)),
                ),
              ),
            ),
            _buildSubtitleOverlay(),
            // 手势层
            Positioned.fill(
              child: PlayerGestureOverlay(
                playing: _playing,
                onSingleTap: _toggleControls,
                onDoubleTap: _togglePlay,
                volume: _volume,
                brightness: _brightness,
                onVolumeChanged: _setVolume,
                onBrightnessChanged: (v) => setState(() => _brightness = v),
                onSeek: _seekRelative,
                child: const SizedBox.expand(),
              ),
            ),
            // 底部控制栏（浮层）
            if (_controlsVisible)
              Positioned(
                left: 0,
                right: 0,
                bottom: 0,
                child: PlayerControlBar(
                  playing: _playing,
                  onPlayPause: _togglePlay,
                  position: _position,
                  duration: _duration,
                  onSeekTo: _seekTo,
                  qualityLabels: _streams.map((s) => s.label).toList(),
                  currentQuality: _current,
                  onQualityChanged: _switchQuality,
                  rate: _rate,
                  onRateChanged: _setRate,
                  isFullscreen: _isFullscreen,
                  onToggleFullscreen: _toggleFullscreen,
                  onOpenSubtitleSettings: _toggleSubtitlePanel,
                  subtitleOn: _subtitle != null && SubtitleService.instance.enabled,
                ),
              ),
            // 字幕设置侧边栏（关闭时滑出屏幕右侧）
            _buildSubtitleSidePanel(),
          ],
        ),
      );
    }

    // 普通模式：AppBar + 视频 + 底部控制栏
    return Scaffold(
      backgroundColor: Colors.black,
      // 黑色背景必须显式白色前景，否则默认 onSurface 深色图标/标题看不清
      appBar: _controlsVisible
          ? AppBar(
              title: Text(widget.title, style: const TextStyle(fontSize: 16)),
              backgroundColor: Colors.black,
              foregroundColor: Colors.white,
            )
          : null,
      body: Column(
        children: [
          Expanded(
            child: Stack(
              alignment: Alignment.center,
              children: [
                _buildVideoArea(c, initialized),
                if (_loading && !initialized)
                  const CircularProgressIndicator()
                else if (_error != null)
                  _buildErrorView(),
                // 亮度遮罩
                Positioned.fill(
                  child: IgnorePointer(
                    child: Container(
                      color: Colors.black
                          .withOpacity((1 - _brightness).clamp(0.0, 0.9)),
                    ),
                  ),
                ),
                _buildSubtitleOverlay(),
                // 手势层
                Positioned.fill(
                  child: PlayerGestureOverlay(
                    playing: _playing,
                    onSingleTap: _toggleControls,
                    onDoubleTap: _togglePlay,
                    volume: _volume,
                    brightness: _brightness,
                    onVolumeChanged: _setVolume,
                    onBrightnessChanged: (v) => setState(() => _brightness = v),
                    onSeek: _seekRelative,
                    child: const SizedBox.expand(),
                  ),
                ),
                // 字幕设置侧边栏（关闭时滑出屏幕右侧）
                _buildSubtitleSidePanel(),
              ],
            ),
          ),
          if (_controlsVisible)
            PlayerControlBar(
              playing: _playing,
              onPlayPause: _togglePlay,
              position: _position,
              duration: _duration,
              onSeekTo: _seekTo,
              qualityLabels: _streams.map((s) => s.label).toList(),
              currentQuality: _current,
              onQualityChanged: _switchQuality,
              rate: _rate,
              onRateChanged: _setRate,
              isFullscreen: _isFullscreen,
              onToggleFullscreen: _toggleFullscreen,
              onOpenSubtitleSettings: _toggleSubtitlePanel,
              subtitleOn: _subtitle != null && SubtitleService.instance.enabled,
            ),
        ],
      ),
    );
  }

  /// 视频画面区：普通/全屏共用同一棵 widget 结构（Positioned.fill >
  /// ClipRect > OverflowBox > 画面实例），仅尺寸计算不同：普通 contain
  /// 完整显示留黑边，全屏 cover 铺满裁切。画面 widget 的布局尺寸始终
  /// 等于实际显示尺寸——不用 FittedBox/Transform 对 Texture 层做 GPU
  /// 缩放（部分设备旋转后黑屏）；各层 slot 同构让切全屏时同一实例
  /// 直接复用（reparent/unmount 同样是黑帧来源）。
  Widget _buildVideoArea(PlayerEngine? c, bool initialized) {
    if (!initialized || c == null) {
      return const Positioned.fill(child: SizedBox.shrink());
    }
    return Positioned.fill(
      child: LayoutBuilder(builder: (context, box) {
        final maxW = box.maxWidth;
        final maxH = box.maxHeight;
        final ar = c.aspectRatio; // 宽/高，未知时引擎默认 16/9
        double w, h;
        if (!maxW.isFinite || !maxH.isFinite || ar <= 0) {
          w = maxW.isFinite ? maxW : 16;
          h = maxH.isFinite ? maxH : 9;
        } else if (_isFullscreen) {
          w = max(maxW, maxH * ar);
          h = w / ar;
        } else {
          w = min(maxW, maxH * ar);
          h = w / ar;
        }
        return ClipRect(
          child: OverflowBox(
            alignment: Alignment.center,
            minWidth: w,
            maxWidth: w,
            minHeight: h,
            maxHeight: h,
            child: KeyedSubtree(
              key: ValueKey('view$_viewEpoch'),
              child: c.buildView(),
            ),
          ),
        );
      }),
    );
  }

  Widget _buildErrorView() {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        const Icon(Icons.error_outline, size: 64, color: Colors.red),
        const SizedBox(height: 16),
        Text('视频加载失败: $_error'),
        const SizedBox(height: 16),
        ElevatedButton(
          onPressed: _initController,
          child: const Text('重试'),
        ),
      ],
    );
  }
}
