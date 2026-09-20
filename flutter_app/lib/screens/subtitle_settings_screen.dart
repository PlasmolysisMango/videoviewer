import 'package:flutter/material.dart';

import '../services/subtitle_service.dart';

/// 字幕设置页：显示开关、字号、时间轴偏移。
/// 所有修改即时生效（播放器监听服务通知）并自动保存到本地。
class SubtitleSettingsScreen extends StatefulWidget {
  const SubtitleSettingsScreen({super.key});

  @override
  State<SubtitleSettingsScreen> createState() => _SubtitleSettingsScreenState();
}

class _SubtitleSettingsScreenState extends State<SubtitleSettingsScreen> {
  final SubtitleService _svc = SubtitleService.instance;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('字幕设置')),
      body: ListView(
        children: [
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 8, 16, 0),
            child: Text(
              '修改后自动保存，播放中即时生效',
              style: TextStyle(fontSize: 12, color: Colors.grey),
            ),
          ),
          SwitchListTile(
            secondary: const Icon(Icons.closed_caption),
            title: const Text('显示字幕'),
            subtitle: const Text('自动加载中文（找不到时按英文/日文回退）'),
            value: _svc.enabled,
            onChanged: (v) => setState(() => _svc.setEnabled(v)),
          ),
          const Divider(),
          // 字号
          ListTile(
            leading: const Icon(Icons.format_size),
            title: const Text('字号'),
            trailing: Text('${_svc.fontSize.round()}'),
          ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Slider(
              min: 12,
              max: 32,
              divisions: 10,
              label: '${_svc.fontSize.round()}',
              value: _svc.fontSize,
              onChanged: (v) => setState(() => _svc.setFontSize(v)),
            ),
          ),
          // 时间轴偏移
          ListTile(
            leading: const Icon(Icons.schedule),
            title: const Text('时间轴偏移'),
            subtitle: const Text('字幕比声音慢就调负值，快就调正值'),
            trailing: Text(
              _offsetLabel(_svc.offsetMs),
              style: const TextStyle(fontWeight: FontWeight.bold),
            ),
          ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                IconButton.outlined(
                  tooltip: '提前 0.5 秒',
                  icon: const Icon(Icons.remove),
                  onPressed: () =>
                      setState(() => _svc.setOffsetMs(_svc.offsetMs - 500)),
                ),
                const SizedBox(width: 12),
                Text(
                  _offsetLabel(_svc.offsetMs),
                  style: const TextStyle(
                      fontSize: 18, fontWeight: FontWeight.bold),
                ),
                const SizedBox(width: 12),
                IconButton.outlined(
                  tooltip: '延后 0.5 秒',
                  icon: const Icon(Icons.add),
                  onPressed: () =>
                      setState(() => _svc.setOffsetMs(_svc.offsetMs + 500)),
                ),
                const SizedBox(width: 12),
                TextButton(
                  onPressed: _svc.offsetMs == 0
                      ? null
                      : () => setState(() => _svc.resetOffset()),
                  child: const Text('重置'),
                ),
              ],
            ),
          ),
          const Divider(),
          // 样式预览
          const Padding(
            padding: EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            child: Text('预览', style: TextStyle(fontWeight: FontWeight.bold)),
          ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Container(
              width: double.infinity,
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
              decoration: BoxDecoration(
                color: Colors.black,
                borderRadius: BorderRadius.circular(8),
              ),
              child: Text(
                '这是字幕样式预览\nSubtitle preview',
                textAlign: TextAlign.center,
                style: TextStyle(
                  color: Colors.white,
                  fontSize: _svc.fontSize,
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
    );
  }

  static String _offsetLabel(int ms) {
    if (ms == 0) return '0s';
    final s = ms / 1000;
    return s > 0 ? '+${s.toStringAsFixed(1)}s' : '${s.toStringAsFixed(1)}s';
  }
}
