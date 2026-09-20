import 'package:flutter/material.dart';

import '../services/subtitle_service.dart';

/// 播放页字幕叠加设置面板：从播放器弹出（BottomSheet 悬浮于画面之上），
/// 开关 / 字号 / 颜色 / 时间轴偏移全部就地可视化调整、实时生效并自动保存。
Future<void> showSubtitlePanel(BuildContext context) {
  return showModalBottomSheet<void>(
    context: context,
    showDragHandle: true,
    isScrollControlled: true,
    builder: (_) => const SubtitlePanel(),
  );
}

/// 字幕设置面板内容：监听 SubtitleService，任何调整立即反映到播放画面。
class SubtitlePanel extends StatelessWidget {
  const SubtitlePanel({super.key});

  /// 常用字幕色板（白/黄/青/绿/粉/橙）。
  static const _colors = <int, String>{
    0xFFFFFFFF: '白色',
    0xFFFFEB3B: '黄色',
    0xFF4DD0E1: '青色',
    0xFF69F0AE: '绿色',
    0xFFFF80AB: '粉色',
    0xFFFFAB40: '橙色',
  };

  static String _offsetLabel(int ms) {
    if (ms == 0) return '0s';
    final s = ms / 1000;
    return s > 0 ? '+${s.toStringAsFixed(1)}s' : '${s.toStringAsFixed(1)}s';
  }

  @override
  Widget build(BuildContext context) {
    final svc = SubtitleService.instance;
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(8, 0, 8, 12),
        child: ListenableBuilder(
          listenable: svc,
          builder: (context, _) => Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // —— 显示开关 ——
              SwitchListTile(
                secondary: const Icon(Icons.closed_caption),
                title: const Text('显示字幕'),
                value: svc.enabled,
                onChanged: svc.setEnabled,
              ),
              // —— 字号 ——
              ListTile(
                leading: const Icon(Icons.format_size),
                title: const Text('字号'),
                trailing: Text('${svc.fontSize.round()}'),
              ),
              Slider(
                min: 12,
                max: 32,
                divisions: 10,
                label: '${svc.fontSize.round()}',
                value: svc.fontSize,
                onChanged: svc.setFontSize,
              ),
              // —— 颜色 ——
              ListTile(
                leading: const Icon(Icons.palette_outlined),
                title: const Text('颜色'),
                trailing: Text(
                  _colors[svc.fontColor] ?? '自定义',
                  style: TextStyle(
                    fontSize: 13,
                    color: Color(svc.fontColor),
                  ),
                ),
              ),
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16),
                child: Row(
                  children: [
                    for (final e in _colors.entries)
                      Padding(
                        padding: const EdgeInsets.only(right: 12),
                        child: InkWell(
                          borderRadius: BorderRadius.circular(20),
                          onTap: () => svc.setFontColor(e.key),
                          child: Container(
                            padding: const EdgeInsets.all(3),
                            decoration: BoxDecoration(
                              shape: BoxShape.circle,
                              border: Border.all(
                                width: 2,
                                color: svc.fontColor == e.key
                                    ? Theme.of(context).colorScheme.primary
                                    : Colors.transparent,
                              ),
                            ),
                            child: CircleAvatar(
                              radius: 13,
                              backgroundColor: Color(e.key),
                              child: svc.fontColor == e.key
                                  ? const Icon(Icons.check,
                                      size: 15, color: Colors.black87)
                                  : null,
                            ),
                          ),
                        ),
                      ),
                  ],
                ),
              ),
              // —— 时间轴偏移 ——
              ListTile(
                leading: const Icon(Icons.schedule),
                title: const Text('时间轴偏移'),
                subtitle: const Text('字幕比声音慢调负值，快调正值'),
                trailing: Text(
                  _offsetLabel(svc.offsetMs),
                  style: const TextStyle(fontWeight: FontWeight.bold),
                ),
              ),
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 8),
                child: Row(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    OutlinedButton(
                      onPressed: () => svc.setOffsetMs(svc.offsetMs - 500),
                      child: const Text('-0.5s'),
                    ),
                    Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 6),
                      child: OutlinedButton(
                        onPressed: () => svc.setOffsetMs(svc.offsetMs - 100),
                        child: const Text('-0.1s'),
                      ),
                    ),
                    Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 6),
                      child: OutlinedButton(
                        onPressed: () => svc.setOffsetMs(svc.offsetMs + 100),
                        child: const Text('+0.1s'),
                      ),
                    ),
                    Padding(
                      padding: const EdgeInsets.only(left: 6),
                      child: OutlinedButton(
                        onPressed: () => svc.setOffsetMs(svc.offsetMs + 500),
                        child: const Text('+0.5s'),
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 8),
              Center(
                child: TextButton(
                  onPressed:
                      svc.offsetMs == 0 ? null : () => svc.resetOffset(),
                  child: const Text('重置偏移'),
                ),
              ),
              // —— 实时预览 ——
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16),
                child: Container(
                  width: double.infinity,
                  padding:
                      const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
                  decoration: BoxDecoration(
                    color: Colors.black,
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: Text(
                    '字幕样式实时预览',
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
            ],
          ),
        ),
      ),
    );
  }
}
