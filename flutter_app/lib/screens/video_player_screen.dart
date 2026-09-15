import 'package:flutter/material.dart';
import 'package:video_player/video_player.dart';

import '../api/models.dart';
import '../services/logger.dart';
import '../widgets/player_control_bar.dart';
import '../widgets/player_gestures.dart';

/// 原生端 HLS 播放器（video_player / ExoPlayer 原生支持 HLS，可带 Referer 直连）。
/// 支持清晰度切换（重初始化并保留进度）、倍速、手势（音量/亮度/进度）。
class VideoPlayerScreen extends StatefulWidget {
  final List<VideoStream> streams;
  final String title;

  const VideoPlayerScreen({
    super.key,
    required this.streams,
    required this.title,
  });

  @override
  State<VideoPlayerScreen> createState() => _VideoPlayerScreenState();
}

class _VideoPlayerScreenState extends State<VideoPlayerScreen> {
  late List<VideoStream> _streams;
  int _current = 0;
  VideoPlayerController? _controller;
  Duration? _seekPending;

  bool _playing = false;
  Duration _position = Duration.zero;
  Duration _duration = Duration.zero;
  double _volume = 1.0;
  double _brightness = 1.0;
  double _rate = 1.0;
  bool _loading = true;
  String? _error;

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
    final c = VideoPlayerController.networkUrl(
      Uri.parse(stream.url),
      httpHeaders: headers,
    );
    _controller = c;
    await old?.dispose();

    try {
      AppLogger.info('Initializing video: ${stream.url}');
      await c.initialize();
      if (!mounted) return;
      c.setVolume(_volume);
      c.setPlaybackSpeed(_rate);
      c.addListener(_onPlayerTick);
      setState(() => _loading = false);
      final seek = _seekPending;
      if (seek != null) {
        await c.seekTo(seek);
        _seekPending = null;
      }
      c.play();
    } catch (e) {
      AppLogger.error('Failed to initialize video', e);
      if (mounted) {
        setState(() {
          _error = e.toString();
          _loading = false;
        });
      }
    }
  }

  void _onPlayerTick() {
    final v = _controller?.value;
    if (v == null || !mounted) return;
    setState(() {
      _position = v.position;
      _duration = v.duration;
      _playing = v.isPlaying;
    });
  }

  @override
  void dispose() {
    _controller?.removeListener(_onPlayerTick);
    _controller?.dispose();
    super.dispose();
  }

  // —— 控制动作 ——

  void _switchQuality(int idx) {
    if (idx == _current || idx < 0 || idx >= _streams.length) return;
    _seekPending = _controller?.value.position;
    _controller?.removeListener(_onPlayerTick);
    setState(() => _current = idx);
    _initController();
  }

  void _togglePlay() {
    final c = _controller;
    if (c == null) return;
    c.value.isPlaying ? c.pause() : c.play();
  }

  void _seekRelative(double seconds) {
    final c = _controller;
    if (c == null) return;
    final target =
        c.value.position + Duration(milliseconds: (seconds * 1000).round());
    final clamped = Duration(
      milliseconds:
          target.inMilliseconds.clamp(0, c.value.duration.inMilliseconds),
    );
    c.seekTo(clamped);
  }

  void _seekTo(Duration d) => _controller?.seekTo(d);

  void _setRate(double r) {
    setState(() => _rate = r);
    _controller?.setPlaybackSpeed(r);
  }

  void _setVolume(double v) {
    setState(() => _volume = v);
    _controller?.setVolume(v);
  }

  @override
  Widget build(BuildContext context) {
    final c = _controller;
    final initialized = c != null && c.value.isInitialized && _error == null;
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        title: Text(widget.title, style: const TextStyle(fontSize: 16)),
        backgroundColor: Colors.black,
      ),
      body: Column(
        children: [
          Expanded(
            child: Stack(
              alignment: Alignment.center,
              children: [
                if (initialized && c != null)
                  Center(
                    child: AspectRatio(
                      aspectRatio: c.value.aspectRatio,
                      child: VideoPlayer(c),
                    ),
                  )
                else if (_loading)
                  const CircularProgressIndicator()
                else if (_error != null)
                  Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const Icon(Icons.error_outline,
                          size: 64, color: Colors.red),
                      const SizedBox(height: 16),
                      Text('视频加载失败: $_error'),
                      const SizedBox(height: 16),
                      ElevatedButton(
                        onPressed: _initController,
                        child: const Text('重试'),
                      ),
                    ],
                  ),
                // 亮度遮罩（右半屏上滑变亮、下滑变暗）
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
          ),
        ],
      ),
    );
  }
}
