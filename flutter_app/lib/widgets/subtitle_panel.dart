import 'package:flutter/material.dart';

import '../services/subtitle_service.dart';
import 'subtitle_picker.dart';

/// 播放页字幕设置侧边栏：从画面右侧滑入的窄面板，打开时画面左侧仍可见，
/// 字号/偏移等调整直接对照播放画面实时生效（无需预览块）。
/// 内容紧凑（小控件字号）：显示开关 / 当前字幕与切换入口 / 字号 / 时间轴偏移。
class SubtitleSidePanel extends StatelessWidget {
  final String movieNumber;
  final String currentSubtitleLabel;
  final VoidCallback onClose;

  const SubtitleSidePanel({
    super.key,
    required this.movieNumber,
    required this.currentSubtitleLabel,
    required this.onClose,
  });

  static String _offsetLabel(int ms) {
    if (ms == 0) return '0s';
    final s = ms / 1000;
    return s > 0 ? '+${s.toStringAsFixed(1)}s' : '${s.toStringAsFixed(1)}s';
  }

  @override
  Widget build(BuildContext context) {
    final svc = SubtitleService.instance;
    return Material(
      color: Theme.of(context).cardColor,
      elevation: 8,
      child: ListenableBuilder(
        listenable: svc,
        builder: (context, _) => SafeArea(
          left: false,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // —— 标题栏 ——
              Padding(
                padding: const EdgeInsets.fromLTRB(12, 6, 4, 0),
                child: Row(
                  children: [
                    const Icon(Icons.closed_caption, size: 18),
                    const SizedBox(width: 6),
                    const Expanded(
                      child: Text('字幕设置',
                          style: TextStyle(
                              fontSize: 14, fontWeight: FontWeight.w700)),
                    ),
                    IconButton(
                      icon: const Icon(Icons.close, size: 18),
                      tooltip: '关闭',
                      onPressed: onClose,
                    ),
                  ],
                ),
              ),
              const Divider(height: 1),
              Expanded(
                child: ListView(
                  padding: const EdgeInsets.fromLTRB(12, 4, 12, 12),
                  children: [
                    // —— 当前字幕 / 切换入口 ——
                    ListTile(
                      dense: true,
                      contentPadding: EdgeInsets.zero,
                      leading: const Icon(Icons.swap_horiz, size: 20),
                      title: Text(
                        '当前：$currentSubtitleLabel',
                        style: const TextStyle(fontSize: 13),
                      ),
                      subtitle: const Text('点击切换候选字幕',
                          style: TextStyle(fontSize: 11)),
                      trailing: const Icon(Icons.chevron_right, size: 18),
                      onTap: movieNumber.isEmpty
                          ? null
                          : () => showSubtitlePicker(context, movieNumber),
                    ),
                    const Divider(height: 1),
                    // —— 显示开关 ——
                    SwitchListTile(
                      dense: true,
                      contentPadding: EdgeInsets.zero,
                      secondary: const Icon(Icons.visibility_outlined,
                          size: 20),
                      title: const Text('显示字幕',
                          style: TextStyle(fontSize: 13)),
                      value: svc.enabled,
                      onChanged: svc.setEnabled,
                    ),
                    // —— 字号 ——
                    Row(
                      children: [
                        const Icon(Icons.format_size, size: 20),
                        const SizedBox(width: 12),
                        const Text('字号', style: TextStyle(fontSize: 13)),
                        Expanded(
                          child: Slider(
                            min: 12,
                            max: 32,
                            divisions: 10,
                            label: '${svc.fontSize.round()}',
                            value: svc.fontSize,
                            onChanged: svc.setFontSize,
                          ),
                        ),
                        Text('${svc.fontSize.round()}',
                            style: const TextStyle(
                                fontSize: 12,
                                fontWeight: FontWeight.bold)),
                      ],
                    ),
                    const Divider(height: 1),
                    // —— 时间轴偏移 ——
                    Padding(
                      padding: const EdgeInsets.symmetric(vertical: 4),
                      child: Row(
                        children: [
                          const Icon(Icons.schedule, size: 20),
                          const SizedBox(width: 12),
                          const Text('时间轴偏移',
                              style: TextStyle(fontSize: 13)),
                          const Spacer(),
                          Text(_offsetLabel(svc.offsetMs),
                              style: const TextStyle(
                                  fontSize: 12,
                                  fontWeight: FontWeight.bold)),
                        ],
                      ),
                    ),
                    Text('字幕比声音慢调负值，快调正值',
                        style: TextStyle(
                            fontSize: 11,
                            color: Theme.of(context).hintColor)),
                    const SizedBox(height: 4),
                    Row(
                      mainAxisAlignment: MainAxisAlignment.spaceEvenly,
                      children: [
                        OutlinedButton(
                          style: OutlinedButton.styleFrom(
                              padding: const EdgeInsets.symmetric(
                                  horizontal: 8, vertical: 2),
                              minimumSize: const Size(0, 30)),
                          onPressed: () =>
                              svc.setOffsetMs(svc.offsetMs - 500),
                          child: const Text('-0.5s',
                              style: TextStyle(fontSize: 11)),
                        ),
                        OutlinedButton(
                          style: OutlinedButton.styleFrom(
                              padding: const EdgeInsets.symmetric(
                                  horizontal: 8, vertical: 2),
                              minimumSize: const Size(0, 30)),
                          onPressed: () =>
                              svc.setOffsetMs(svc.offsetMs - 100),
                          child: const Text('-0.1s',
                              style: TextStyle(fontSize: 11)),
                        ),
                        OutlinedButton(
                          style: OutlinedButton.styleFrom(
                              padding: const EdgeInsets.symmetric(
                                  horizontal: 8, vertical: 2),
                              minimumSize: const Size(0, 30)),
                          onPressed: () =>
                              svc.setOffsetMs(svc.offsetMs + 100),
                          child: const Text('+0.1s',
                              style: TextStyle(fontSize: 11)),
                        ),
                        OutlinedButton(
                          style: OutlinedButton.styleFrom(
                              padding: const EdgeInsets.symmetric(
                                  horizontal: 8, vertical: 2),
                              minimumSize: const Size(0, 30)),
                          onPressed: () =>
                              svc.setOffsetMs(svc.offsetMs + 500),
                          child: const Text('+0.5s',
                              style: TextStyle(fontSize: 11)),
                        ),
                      ],
                    ),
                    const SizedBox(height: 4),
                    Center(
                      child: TextButton(
                        style: TextButton.styleFrom(
                            textStyle: const TextStyle(fontSize: 11)),
                        onPressed: svc.offsetMs == 0
                            ? null
                            : () => svc.resetOffset(),
                        child: const Text('重置偏移'),
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
