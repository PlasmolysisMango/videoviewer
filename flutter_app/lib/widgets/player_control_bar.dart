import 'package:flutter/material.dart';

/// 播放器通用底部控制条：播放/暂停 + 进度条 + 时间 + 清晰度/倍速入口。
class PlayerControlBar extends StatelessWidget {
  final bool playing;
  final VoidCallback onPlayPause;
  final Duration position;
  final Duration duration;
  final ValueChanged<Duration> onSeekTo;
  final List<String> qualityLabels;
  final int currentQuality;
  final ValueChanged<int> onQualityChanged;
  final double rate;
  final ValueChanged<double> onRateChanged;

  static const _rates = [0.5, 0.75, 1.0, 1.25, 1.5, 2.0];

  const PlayerControlBar({
    super.key,
    required this.playing,
    required this.onPlayPause,
    required this.position,
    required this.duration,
    required this.onSeekTo,
    required this.qualityLabels,
    required this.currentQuality,
    required this.onQualityChanged,
    required this.rate,
    required this.onRateChanged,
  });

  String _fmt(Duration d) {
    final m = d.inMinutes.remainder(60).toString().padLeft(2, '0');
    final s = d.inSeconds.remainder(60).toString().padLeft(2, '0');
    final h = d.inHours;
    return h > 0 ? '$h:$m:$s' : '$m:$s';
  }

  @override
  Widget build(BuildContext context) {
    final maxMs = duration.inMilliseconds.toDouble();
    final posMs =
        position.inMilliseconds.clamp(0, duration.inMilliseconds).toDouble();

    return Container(
      color: Colors.black54,
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      child: SafeArea(
        top: false,
        child: Row(
          children: [
            IconButton(
              icon: Icon(playing ? Icons.pause : Icons.play_arrow,
                  color: Colors.white),
              onPressed: onPlayPause,
            ),
            Text(
              '${_fmt(position)} / ${_fmt(duration)}',
              style: const TextStyle(color: Colors.white, fontSize: 12),
            ),
            Expanded(
              child: Slider(
                value: maxMs > 0 ? posMs : 0,
                max: maxMs > 0 ? maxMs : 1,
                onChanged: maxMs > 0
                    ? (v) => onSeekTo(Duration(milliseconds: v.round()))
                    : null,
              ),
            ),
            PopupMenuButton<double>(
              tooltip: '倍速',
              initialValue: rate,
              onSelected: onRateChanged,
              itemBuilder: (_) => _rates
                  .map((r) => PopupMenuItem(
                        value: r,
                        child: Row(
                          children: [
                            if (r == rate)
                              const Icon(Icons.check, size: 18)
                            else
                              const SizedBox(width: 18),
                            const SizedBox(width: 8),
                            Text('${r}x'),
                          ],
                        ),
                      ))
                  .toList(),
              child: Padding(
                padding:
                    const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                child: Text(
                  '${rate}x',
                  style: const TextStyle(color: Colors.white, fontSize: 13),
                ),
              ),
            ),
            if (qualityLabels.length > 1)
              PopupMenuButton<int>(
                tooltip: '清晰度',
                initialValue: currentQuality,
                onSelected: onQualityChanged,
                itemBuilder: (_) => List.generate(qualityLabels.length, (i) {
                  return PopupMenuItem(
                    value: i,
                    child: Row(
                      children: [
                        if (i == currentQuality)
                          const Icon(Icons.check, size: 18)
                        else
                          const SizedBox(width: 18),
                        const SizedBox(width: 8),
                        Text(qualityLabels[i]),
                      ],
                    ),
                  );
                }).toList(),
                child: Padding(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                  child: Text(
                    qualityLabels[currentQuality],
                    style: const TextStyle(color: Colors.white, fontSize: 13),
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}
