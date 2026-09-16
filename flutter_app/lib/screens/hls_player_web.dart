import 'dart:html' as html;
import 'dart:js' as js;
import 'dart:ui_web' as ui_web;

import 'package:flutter/material.dart';
import 'package:pointer_interceptor/pointer_interceptor.dart';

import '../api/models.dart';
import '../services/backend_launcher.dart';
import '../services/logger.dart';
import '../widgets/player_control_bar.dart';
import '../widgets/player_gestures.dart';

/// Web 端 HLS 播放器：hls.js + HtmlElementView。
///
/// Chrome 不原生支持 HLS，且浏览器无法伪造 Referer，
/// 因此流必须经后端 `/api/hls/playlist` 代理转发。
/// 支持清晰度切换（多路流重载并保留进度）、倍速、手势（音量/亮度/进度）。
class HlsPlayerScreen extends StatefulWidget {
  final List<VideoStream> streams;
  final String title;

  const HlsPlayerScreen({
    super.key,
    required this.streams,
    required this.title,
  });

  @override
  State<HlsPlayerScreen> createState() => _HlsPlayerScreenState();
}

class _HlsPlayerScreenState extends State<HlsPlayerScreen> {
  late List<VideoStream> _streams;
  int _current = 0;

  html.VideoElement? _video;
  js.JsObject? _hls;
  double? _seekPending;

  bool _playing = false;
  Duration _position = Duration.zero;
  Duration _duration = Duration.zero;
  double _volume = 1.0;
  double _brightness = 1.0;
  double _rate = 1.0;
  bool _loading = true;
  String? _error;
  int _netRetries = 0; // hls.js 网络类致命错误的自动重试次数
  bool _isFullscreen = false;
  final String _viewType =
      'hls-player-${DateTime.now().microsecondsSinceEpoch}';

  @override
  void initState() {
    super.initState();
    _streams = VideoStream.sortStreams(widget.streams);
    _initPlayer();
  }

  /// 把上游 m3u8 包装为后端 HLS 代理地址（后端带 Referer 拉取并改写分片）。
  String _proxyUrl(VideoStream s) {
    final params = <String, String>{'u': s.url};
    if (s.referer != null && s.referer!.isNotEmpty) {
      params['ref'] = s.referer!;
    }
    return '${BackendLauncher.baseUrl}/api/hls/playlist?'
        '${Uri(queryParameters: params).query}';
  }

  void _initPlayer() {
    final ctor = js.context['Hls'];
    if (ctor == null) {
      setState(() {
        _error = 'hls.js 未加载，请检查网络（CDN 脚本不可达）';
        _loading = false;
      });
      return;
    }

    if (_video == null) {
      final video = html.VideoElement()
        ..autoplay = true
        ..setAttribute('playsinline', 'true')
        ..style.width = '100%'
        ..style.height = '100%'
        ..style.backgroundColor = 'black';
      video.onTimeUpdate.listen((_) {
        if (!mounted) return;
        setState(() {
          _position =
              Duration(milliseconds: (video.currentTime * 1000).round());
          if (video.duration.isFinite) {
            _duration = Duration(milliseconds: (video.duration * 1000).round());
          }
        });
      });
      video.onPlay.listen((_) {
        if (mounted) setState(() => _playing = true);
      });
      video.onPause.listen((_) {
        if (mounted) setState(() => _playing = false);
      });
      video.onLoadedMetadata.listen((_) {
        video.volume = _volume;
        video.playbackRate = _rate;
        final seek = _seekPending;
        if (seek != null) {
          video.currentTime = seek;
          _seekPending = null;
        }
      });
      video.onError.listen((_) {
        final err = video.error;
        AppLogger.error('HLS video element error', err);
        if (mounted) {
          setState(() {
            _error = '视频加载失败: ${err?.message ?? "未知错误"}';
            _loading = false;
          });
        }
      });
      _video = video;
      ui_web.platformViewRegistry
          .registerViewFactory(_viewType, (int id) => video);
    }

    try {
      _hls?.callMethod('destroy');
    } catch (_) {}
    final hls = js.JsObject(ctor, []);
    _hls = hls;
    hls.callMethod('loadSource', [_proxyUrl(_streams[_current])]);
    hls.callMethod('attachMedia', [_video!]);

    final events = js.context['Hls']['Events'] as js.JsObject?;
    if (events != null) {
      hls.callMethod('on', [
        events['MANIFEST_PARSED'],
        js.allowInterop((dynamic _, dynamic __) {
          if (!mounted) return;
          setState(() => _loading = false);
          _video?.play();
        }),
      ]);
      hls.callMethod('on', [
        events['ERROR'],
        js.allowInterop((dynamic _, dynamic data) {
          try {
            final obj = data is js.JsObject
                ? data
                : js.JsObject.fromBrowserObject(data);
            if (obj['fatal'] != true) return;
            final type = '${obj['type'] ?? ''}';
            final details = '${obj['details'] ?? ''}';
            AppLogger.error('hls.js fatal: type=$type details=$details');
            // 官方推荐恢复路径：网络类错误重试拉取，媒体类错误重置解码器
            if (type == 'networkError' && _netRetries < 2) {
              _netRetries++;
              hls.callMethod('startLoad', []);
              return;
            }
            if (type == 'mediaError') {
              hls.callMethod('recoverMediaError', []);
              return;
            }
            if (mounted) {
              setState(() {
                _error = '流加载失败（$details），请返回重试';
                _loading = false;
              });
            }
          } catch (_) {}
        }),
      ]);
    }
  }

  @override
  void dispose() {
    try {
      _hls?.callMethod('destroy');
    } catch (_) {}
    _video?.remove();
    super.dispose();
  }

  // —— 控制动作 ——

  void _switchQuality(int idx) {
    if (idx == _current || idx < 0 || idx >= _streams.length) return;
    _seekPending = (_video?.currentTime ?? 0).toDouble();
    _netRetries = 0;
    setState(() {
      _current = idx;
      _loading = true;
      _error = null;
    });
    _initPlayer();
  }

  void _togglePlay() {
    final v = _video;
    if (v == null) return;
    v.paused ? v.play() : v.pause();
  }

  void _seekRelative(double seconds) {
    final v = _video;
    if (v == null) return;
    final target = (v.currentTime + seconds).clamp(0.0, v.duration);
    v.currentTime = target;
  }

  void _seekTo(Duration d) {
    if (_video != null) _video!.currentTime = d.inMilliseconds / 1000;
  }

  void _setRate(double r) {
    setState(() => _rate = r);
    if (_video != null) _video!.playbackRate = r;
  }

  void _setVolume(double v) {
    setState(() => _volume = v);
    if (_video != null) _video!.volume = v;
  }

  void _toggleFullscreen() {
    setState(() => _isFullscreen = !_isFullscreen);
    if (_isFullscreen) {
      html.document.documentElement?.requestFullscreen();
    } else {
      html.document.exitFullscreen();
    }
  }

  @override
  Widget build(BuildContext context) {
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
                Center(
                  child: AspectRatio(
                    aspectRatio: 16 / 9,
                    child: HtmlElementView(viewType: _viewType),
                  ),
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
                if (_loading) const CircularProgressIndicator(),
                if (_error != null)
                  PointerInterceptor(
                    child: Container(
                      color: Colors.black54,
                      padding: const EdgeInsets.all(16),
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(Icons.error_outline,
                              size: 64, color: Colors.red),
                          const SizedBox(height: 16),
                          Text(_error!, textAlign: TextAlign.center),
                          const SizedBox(height: 16),
                          ElevatedButton(
                            onPressed: () {
                              setState(() {
                                _error = null;
                                _loading = true;
                              });
                              _initPlayer();
                            },
                            child: const Text('重试'),
                          ),
                        ],
                      ),
                    ),
                  ),
                // 手势层：PointerInterceptor 让 platform view 之上的
                // Flutter 手势能收到指针事件
                Positioned.fill(
                  child: PointerInterceptor(
                    child: PlayerGestureOverlay(
                      volume: _volume,
                      brightness: _brightness,
                      onVolumeChanged: _setVolume,
                      onBrightnessChanged: (v) =>
                          setState(() => _brightness = v),
                      onSeek: _seekRelative,
                      child: const SizedBox.expand(),
                    ),
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
            isFullscreen: _isFullscreen,
            onToggleFullscreen: _toggleFullscreen,
          ),
        ],
      ),
    );
  }
}
