import 'dart:async';

import 'package:flutter/material.dart';

/// 播放器通用手势层（跨平台）：
/// - 单击：回调 onSingleTap（页面用它切换控制 UI 显隐）
/// - 双击：任意位置回调 onDoubleTap（页面用它播放/暂停），中央提示当前动作
/// - 水平拖动：快进/快退（1px ≈ 0.25s，拖动结束提交 onSeek）
/// - 左半屏垂直拖动：音量（0..1）
/// - 右半屏垂直拖动：亮度（0..1；播放器侧用黑色遮罩呈现，真实屏幕亮度需平台插件）
/// 操作时中央显示指示器，停手自动淡出。
class PlayerGestureOverlay extends StatefulWidget {
  final Widget child;
  final double volume;
  final double brightness;
  final bool playing;
  final ValueChanged<double> onVolumeChanged;
  final ValueChanged<double> onBrightnessChanged;
  final ValueChanged<double> onSeek;
  final VoidCallback? onSingleTap;
  final VoidCallback? onDoubleTap;

  const PlayerGestureOverlay({
    super.key,
    required this.child,
    required this.volume,
    required this.brightness,
    required this.playing,
    required this.onVolumeChanged,
    required this.onBrightnessChanged,
    required this.onSeek,
    this.onSingleTap,
    this.onDoubleTap,
  });

  @override
  State<PlayerGestureOverlay> createState() => _PlayerGestureOverlayState();
}

class _PlayerGestureOverlayState extends State<PlayerGestureOverlay> {
  bool _isVolumeSide = true;
  double _dragStartValue = 0;
  double _accumVertical = 0; // 垂直拖动累积量
  double _accumSeconds = 0;
  bool _horizontal = false;

  IconData? _hintIcon;
  String? _hintText;
  Timer? _hintTimer;

  void _showHint(IconData icon, String text) {
    _hintTimer?.cancel();
    setState(() {
      _hintIcon = icon;
      _hintText = text;
    });
    _hintTimer = Timer(const Duration(milliseconds: 800), () {
      if (mounted) {
        setState(() {
          _hintIcon = null;
          _hintText = null;
        });
      }
    });
  }

  @override
  void dispose() {
    _hintTimer?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        final width = constraints.maxWidth;
        return GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: widget.onSingleTap,
          onDoubleTap: () {
            widget.onDoubleTap?.call();
            // 提示按双击前的状态反向展示：播放中 → 「暂停」
            _showHint(
                widget.playing ? Icons.pause : Icons.play_arrow,
                widget.playing ? '暂停' : '播放');
          },
          onVerticalDragStart: (d) {
            _horizontal = false;
            _isVolumeSide = d.localPosition.dx < width / 2;
            _dragStartValue = _isVolumeSide ? widget.volume : widget.brightness;
            _accumVertical = 0;
          },
          onVerticalDragUpdate: (d) {
            if (_horizontal) return;
            _accumVertical += -d.delta.dy / 300;
            final v = (_dragStartValue + _accumVertical).clamp(0.0, 1.0);
            if (_isVolumeSide) {
              widget.onVolumeChanged(v);
              _showHint(Icons.volume_up, '音量 ${(v * 100).round()}%');
            } else {
              widget.onBrightnessChanged(v);
              _showHint(Icons.brightness_6, '亮度 ${(v * 100).round()}%');
            }
          },
          onHorizontalDragStart: (d) {
            _horizontal = true;
            _accumSeconds = 0;
          },
          onHorizontalDragUpdate: (d) {
            _accumSeconds += d.delta.dx * 0.25;
            _showHint(
              _accumSeconds >= 0 ? Icons.fast_forward : Icons.fast_rewind,
              _accumSeconds >= 0
                  ? '+${_accumSeconds.round()}s'
                  : '${_accumSeconds.round()}s',
            );
          },
          onHorizontalDragEnd: (_) {
            if (_accumSeconds.abs() > 0.5) {
              widget.onSeek(_accumSeconds);
            }
            _accumSeconds = 0;
          },
          child: Stack(
            children: [
              widget.child,
              if (_hintText != null)
                IgnorePointer(
                  child: Center(
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 20, vertical: 12),
                      decoration: BoxDecoration(
                        color: Colors.black54,
                        borderRadius: BorderRadius.circular(32),
                      ),
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Icon(_hintIcon, color: Colors.white, size: 36),
                          const SizedBox(height: 4),
                          Text(
                            _hintText!,
                            style: const TextStyle(
                                color: Colors.white, fontSize: 14),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
            ],
          ),
        );
      },
    );
  }
}
