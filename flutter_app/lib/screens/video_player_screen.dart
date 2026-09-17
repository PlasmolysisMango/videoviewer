import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../api/models.dart';
import '../services/history.dart';
import '../services/logger.dart';
import '../services/player_engine.dart';
import '../widgets/player_control_bar.dart';
import '../widgets/player_gestures.dart';

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

  @override
  void initState() {
    super.initState();
    _streams = VideoStream.sortStreams(widget.streams);
    _initController();
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
    setState(() {
      _position = e.position;
      _duration = e.duration;
      _playing = e.isPlaying;
    });
  }

  @override
  void dispose() {
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
    // 退出时恢复竖屏允许 + 显示状态栏
    SystemChrome.setPreferredOrientations([
      DeviceOrientation.portraitUp,
      DeviceOrientation.landscapeLeft,
      DeviceOrientation.landscapeRight,
    ]);
    SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge);
    _controller?.onTick = null;
    _controller?.dispose();
    super.dispose();
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

  void _toggleControls() => setState(() => _controlsVisible = !_controlsVisible);

  void _seekRelative(double seconds) {
    final c = _controller;
    if (c == null) return;
    final target =
        c.position + Duration(milliseconds: (seconds * 1000).round());
    final clamped = Duration(
      milliseconds: target.inMilliseconds.clamp(0, c.duration.inMilliseconds),
    );
    c.seek(clamped);
  }

  void _seekTo(Duration d) => _controller?.seek(d);

  void _setRate(double r) {
    setState(() => _rate = r);
    _controller?.setRate(r);
  }

  void _setVolume(double v) {
    setState(() => _volume = v);
    _controller?.setVolume(v);
  }

  /// 切换全屏：横屏全屏 + 隐藏状态栏
  void _toggleFullscreen() {
    setState(() {
      _isFullscreen = !_isFullscreen;
      // 切换全屏时确保控制 UI 可见，方便用户退出全屏
      _controlsVisible = true;
    });
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
            Positioned.fill(
              child: _buildVideoArea(c, initialized),
            ),
            // 亮度遮罩
            Positioned.fill(
              child: IgnorePointer(
                child: Container(
                  color: Colors.black
                      .withOpacity((1 - _brightness).clamp(0.0, 0.9)),
                ),
              ),
            ),
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
                ),
              ),
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
            ),
        ],
      ),
    );
  }

  /// 视频画面区：根据是否全屏选择 AspectRatio 或 FittedBox 填满。
  Widget _buildVideoArea(PlayerEngine? c, bool initialized) {
    if (!initialized || c == null) return const SizedBox.shrink();
    if (_isFullscreen) {
      final size = c.videoSize;
      if (size.width > 0 && size.height > 0) {
        // 全屏：FittedBox cover 填满屏幕，保持比例裁切多余部分
        return FittedBox(
          fit: BoxFit.cover,
          child: SizedBox(
            width: size.width,
            height: size.height,
            child: c.buildView(),
          ),
        );
      }
      // 分辨率未知：直接铺满由引擎内部 contain 适配
      return SizedBox.expand(child: c.buildView());
    }
    // 普通模式：AspectRatio 保持比例
    return Center(
      child: AspectRatio(
        aspectRatio: c.aspectRatio,
        child: c.buildView(),
      ),
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
