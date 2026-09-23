import 'dart:async';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';
import 'package:video_player/video_player.dart';

import 'data_saver.dart';

/// 播放引擎抽象：屏蔽 video_player 与 media_kit 两套后端差异，
/// 供 VideoPlayerScreen 的手势/控制条 UI 统一驱动。
abstract class PlayerEngine {
  /// UI 刷新回调（进度/播放状态变化时触发）。
  void Function()? onTick;

  bool get initialized;
  Duration get position;
  Duration get duration;
  bool get isPlaying;

  /// 视频宽高比（宽/高）；未知时返回 16/9。
  double get aspectRatio;

  /// 原始视频尺寸；未知时返回 Size.zero。
  Size get videoSize;

  Future<void> play();
  Future<void> pause();
  Future<void> seek(Duration d);
  Future<void> setVolume(double v); // 0.0 - 1.0
  Future<void> setRate(double r);
  Widget buildView();
  Future<void> dispose();
}

/// 按平台创建播放引擎。
/// Windows：video_player 无原生实现（创建即抛 UnimplementedError init），
/// 改用 media_kit（libmpv，完整支持 HLS 与自定义 header）。
/// Android/iOS：video_player（ExoPlayer 原生 HLS，可带 Referer 直连）。
Future<PlayerEngine> createPlayerEngine({
  required String url,
  Map<String, String> headers = const {},
}) async {
  if (!kIsWeb && Platform.isWindows) {
    return MediaKitEngine.open(url, headers: headers);
  }
  return VideoPlayerEngine.open(url, headers: headers);
}

/// video_player 实现（Android/iOS）。
class VideoPlayerEngine implements PlayerEngine {
  VideoPlayerEngine._(this._c) {
    _c.addListener(_tick);
  }

  static Future<PlayerEngine> open(String url,
      {Map<String, String> headers = const {}}) async {
    // 把移动网络加载限制设置同步给原生（Android 侧 ExoPlayer 缓冲/限速策略），
    // 保证本次播放与设置页一致；非 Android 平台为安全空操作。
    await DataSaver.syncToNative();
    final c = VideoPlayerController.networkUrl(
      Uri.parse(url),
      httpHeaders: headers,
    );
    await c.initialize();
    return VideoPlayerEngine._(c);
  }

  final VideoPlayerController _c;

  @override
  void Function()? onTick;

  void _tick() => onTick?.call();

  @override
  bool get initialized => _c.value.isInitialized;

  @override
  Duration get position => _c.value.position;

  @override
  Duration get duration => _c.value.duration;

  @override
  bool get isPlaying => _c.value.isPlaying;

  @override
  double get aspectRatio {
    // video_player 内置 getter 在 size 未知时返回 1.0（正方形），照它布局
    // 会把非方形视频拉伸变形（切换源/元数据未就绪时可见）；未知时按
    // 16/9 兜底更接近绝大多数片源，size 就绪后自动恢复真实比例。
    final s = _c.value.size;
    if (s.width > 0 && s.height > 0) {
      return s.width / s.height;
    }
    return 16 / 9;
  }

  @override
  Size get videoSize => _c.value.size;

  @override
  Future<void> play() => _c.play();

  @override
  Future<void> pause() => _c.pause();

  @override
  Future<void> seek(Duration d) => _c.seekTo(d);

  @override
  Future<void> setVolume(double v) => _c.setVolume(v);

  @override
  Future<void> setRate(double r) => _c.setPlaybackSpeed(r);

  @override
  Widget buildView() {
    // 每次返回新 widget 实例：重建成本可忽略（key 稳定时 Element 复用，
    // 零 remount），但保持画面层可被外部重建驱动（配合播放页的
    // KeyedSubtree epoch 机制，seek/周期自愈时换 Key 强制取新帧）。
    return VideoPlayer(_c);
  }

  @override
  Future<void> dispose() {
    _c.removeListener(_tick);
    return _c.dispose();
  }
}

/// media_kit（libmpv）实现（Windows 桌面端）。
class MediaKitEngine implements PlayerEngine {
  MediaKitEngine._(Player player) : _player = player {
    _subs = [
      _player.stream.position.listen((p) {
        _position = p;
        onTick?.call();
      }),
      _player.stream.duration.listen((d) {
        _duration = d;
        onTick?.call();
      }),
      _player.stream.playing.listen((v) {
        _playing = v;
        onTick?.call();
      }),
      _player.stream.videoParams.listen((_) => onTick?.call()),
    ];
    _controller = VideoController(_player);
  }

  static Future<PlayerEngine> open(String url,
      {Map<String, String> headers = const {}}) async {
    MediaKit.ensureInitialized();
    final p = Player();
    await p.open(Media(url, httpHeaders: headers));
    return MediaKitEngine._(p);
  }

  final Player _player;
  late final VideoController _controller;

  List<StreamSubscription<dynamic>> _subs = const [];
  Duration _position = Duration.zero;
  Duration _duration = Duration.zero;
  bool _playing = false;

  @override
  void Function()? onTick;

  @override
  bool get initialized => true; // open() 返回后即就绪，缓冲由 libmpv 处理

  @override
  Duration get position => _position;

  @override
  Duration get duration => _duration;

  @override
  bool get isPlaying => _playing;

  @override
  double get aspectRatio {
    final size = videoSize;
    if (size.width > 0 && size.height > 0) {
      return size.width / size.height;
    }
    return 16 / 9;
  }

  @override
  Size get videoSize {
    // VideoParams：w/h 为源尺寸，dw/dh 为显示尺寸（优先）
    final p = _player.state.videoParams;
    final w = (p.dw ?? p.w ?? 0).toDouble();
    final h = (p.dh ?? p.h ?? 0).toDouble();
    if (w > 0 && h > 0) return Size(w, h);
    return Size.zero;
  }

  @override
  Future<void> play() => _player.play();

  @override
  Future<void> pause() => _player.pause();

  @override
  Future<void> seek(Duration d) => _player.seek(d);

  @override
  Future<void> setVolume(double v) => _player.setVolume(v * 100);

  @override
  Future<void> setRate(double r) => _player.setRate(r);

  @override
  Widget buildView() {
    // 同 VideoPlayerEngine：每次新建实例，配合播放页 epoch 机制。
    return Video(
      controller: _controller,
      controls: NoVideoControls,
      fit: BoxFit.contain,
    );
  }

  @override
  Future<void> dispose() async {
    for (final s in _subs) {
      await s.cancel();
    }
    _subs = const [];
    await _player.dispose();
  }
}
