import 'package:flutter/material.dart';

import '../api/client.dart';
import '../services/backend_launcher.dart';
import '../services/logger.dart';
import '../services/subtitle_service.dart';

/// 字幕手动选择面板：列出各字幕源（subtitlecat / avsubtitles）该番号的
/// 全部条目，中文优先排序；点击下载并应用（写入同一缓存，播放器实时生效）。
/// 入口：详情页"未找到字幕"提示（auto 自动加载失败/无结果时的手动兜底）。
Future<void> showSubtitlePicker(BuildContext context, String code) {
  return showModalBottomSheet(
    context: context,
    showDragHandle: true,
    isScrollControlled: true,
    builder: (_) => _SubtitlePickerSheet(code: code),
  );
}

/// 语言展示优先级：中文 → 英文 → 日文 → 其他。
int _langRank(String lang) {
  final l = lang.toLowerCase();
  if (l.startsWith('zh')) return 0;
  if (l.startsWith('en')) return 1;
  if (l == 'ja' || l == 'jp') return 2;
  return 3;
}

class _SubtitlePickerSheet extends StatefulWidget {
  final String code;

  const _SubtitlePickerSheet({required this.code});

  @override
  State<_SubtitlePickerSheet> createState() => _SubtitlePickerSheetState();
}

class _SubtitlePickerSheetState extends State<_SubtitlePickerSheet> {
  late final JavDBClient _client;
  List<Map<String, dynamic>> _items = [];
  bool _loading = true;
  String? _error;
  String? _applyingRef; // 正在下载应用的条目

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    _search();
  }

  Future<void> _search() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final rep = await _client.searchSubtitles(widget.code);
      if (!mounted) return;
      final items = (rep['items'] as List?)
              ?.map((e) => e as Map<String, dynamic>)
              .toList() ??
          const <Map<String, dynamic>>[];
      // 中文优先（后端已排序，这里再保证一次稳定顺序）。
      items.sort((a, b) => _langRank((a['lang'] as String?) ?? '')
          .compareTo(_langRank((b['lang'] as String?) ?? '')));
      setState(() {
        _items = items;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _loading = false;
      });
      AppLogger.error('Subtitle picker search failed', e);
    }
  }

  Future<void> _apply(Map<String, dynamic> item) async {
    final ref = (item['ref'] as String?) ?? '';
    if (ref.isEmpty || _applyingRef != null) return;
    setState(() => _applyingRef = ref);
    final ls = await SubtitleService.instance.applyManual(
      widget.code,
      (item['source'] as String?) ?? '',
      ref,
      (item['lang'] as String?) ?? '',
    );
    if (!mounted) return;
    setState(() => _applyingRef = null);
    if (ls != null) {
      Navigator.pop(context);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('已应用 ${ls.langLabel}字幕（${ls.source}）')),
      );
    } else {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
            content: Text('下载失败，请换一条或稍后再试'),
            backgroundColor: Colors.red),
      );
    }
  }

  String _langLabel(String lang) {
    switch (lang.toLowerCase()) {
      case 'zh':
        return '中文';
      case 'zh-cn':
        return '中简';
      case 'zh-tw':
      case 'zh-hk':
        return '中繁';
      case 'en':
        return '英';
      case 'ja':
      case 'jp':
        return '日';
      case 'multi':
        return '多语';
      default:
        return lang.toUpperCase();
    }
  }

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: SizedBox(
        height: MediaQuery.of(context).size.height * 0.65,
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
              child: Row(
                children: [
                  Expanded(
                    child: Text('选择字幕 · ${widget.code}',
                        style: const TextStyle(
                            fontSize: 16, fontWeight: FontWeight.w700)),
                  ),
                  IconButton(
                    icon: const Icon(Icons.refresh, size: 20),
                    tooltip: '重新搜索',
                    onPressed: _loading ? null : _search,
                  ),
                ],
              ),
            ),
            const Divider(height: 1),
            Expanded(
              child: _loading
                  ? const Center(child: CircularProgressIndicator())
                  : _error != null
                      ? Center(
                          child: Column(
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              Text('搜索失败: $_error',
                                  textAlign: TextAlign.center,
                                  style: TextStyle(
                                      fontSize: 12,
                                      color:
                                          Theme.of(context).colorScheme.error)),
                              const SizedBox(height: 10),
                              OutlinedButton(
                                  onPressed: _search,
                                  child: const Text('重试')),
                            ],
                          ),
                        )
                      : _items.isEmpty
                          ? Center(
                              child: Text('两个字幕源都没有 ${widget.code} 的条目',
                                  style: TextStyle(
                                      color: Theme.of(context).hintColor)))
                          : ListView.separated(
                              padding: const EdgeInsets.fromLTRB(12, 8, 12, 12),
                              itemCount: _items.length,
                              separatorBuilder: (_, __) =>
                                  const SizedBox(height: 6),
                              itemBuilder: (context, i) {
                                final it = _items[i];
                                final ref = (it['ref'] as String?) ?? '';
                                final lang = (it['lang'] as String?) ?? '';
                                final title =
                                    (it['title'] as String?) ?? ref;
                                final source = (it['source'] as String?) ?? '';
                                final size = (it['size'] as String?) ?? '';
                                final downloads =
                                    (it['downloads'] as num?)?.toInt() ?? 0;
                                final applying = _applyingRef == ref;
                                return ListTile(
                                  dense: true,
                                  onTap:
                                      applying ? null : () => _apply(it),
                                  leading: Container(
                                    width: 44,
                                    height: 24,
                                    alignment: Alignment.center,
                                    decoration: BoxDecoration(
                                      color: Theme.of(context)
                                          .colorScheme
                                          .primary
                                          .withValues(alpha: 0.12),
                                      borderRadius: BorderRadius.circular(6),
                                    ),
                                    child: Text(
                                      _langLabel(lang),
                                      style: TextStyle(
                                          fontSize: 11,
                                          color: Theme.of(context)
                                              .colorScheme
                                              .primary),
                                    ),
                                  ),
                                  title: Text(title,
                                      maxLines: 1,
                                      overflow: TextOverflow.ellipsis,
                                      style: const TextStyle(fontSize: 13)),
                                  subtitle: Text(
                                    [
                                      source,
                                      if (size.isNotEmpty) size,
                                      if (downloads > 0) '$downloads 次下载',
                                    ].join(' · '),
                                    style: TextStyle(
                                        fontSize: 11,
                                        color: Theme.of(context).hintColor),
                                  ),
                                  trailing: applying
                                      ? const SizedBox(
                                          width: 16,
                                          height: 16,
                                          child: CircularProgressIndicator(
                                              strokeWidth: 2))
                                      : const Icon(Icons.download,
                                          size: 20),
                                );
                              },
                            ),
            ),
          ],
        ),
      ),
    );
  }
}
