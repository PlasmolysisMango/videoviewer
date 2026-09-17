import 'package:flutter/material.dart';

import '../services/history.dart';
import '../services/image_url.dart';
import 'movie_detail_screen.dart';

/// 观影历史：进入过详情页的影片按最近浏览倒序，状态分
/// 浏览过 / 观看中 / 看过（播放满 [kWatchedThresholdSeconds] 秒）。
class HistoryScreen extends StatefulWidget {
  const HistoryScreen({super.key});

  @override
  State<HistoryScreen> createState() => _HistoryScreenState();
}

class _HistoryScreenState extends State<HistoryScreen> {
  List<HistoryEntry> _entries = [];
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _reload();
  }

  Future<void> _reload() async {
    final entries = await HistoryService.list();
    if (!mounted) return;
    setState(() {
      _entries = entries;
      _loading = false;
    });
  }

  Future<void> _confirmClear() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('清空历史'),
        content: const Text('确定清空全部观影历史？此操作不可恢复。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('清空'),
          ),
        ],
      ),
    );
    if (ok == true) {
      await HistoryService.clear();
      _reload();
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('历史记录'),
        actions: [
          if (_entries.isNotEmpty)
            IconButton(
              icon: const Icon(Icons.delete_sweep_outlined),
              tooltip: '清空历史',
              onPressed: _confirmClear,
            ),
        ],
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : _entries.isEmpty
              ? const Center(child: Text('暂无历史记录'))
              : RefreshIndicator(
                  onRefresh: _reload,
                  child: ListView.separated(
                    itemCount: _entries.length,
                    separatorBuilder: (_, __) => const Divider(height: 1),
                    itemBuilder: (context, i) {
                      final e = _entries[i];
                      return _buildTile(context, e);
                    },
                  ),
                ),
    );
  }

  Widget _buildTile(BuildContext context, HistoryEntry e) {
    return ListTile(
      leading: ClipRRect(
        borderRadius: BorderRadius.circular(6),
        child: e.cover.isNotEmpty
            ? Image.network(
                resolveImageUrl(e.cover),
                width: 48,
                height: 64,
                fit: BoxFit.cover,
                errorBuilder: (_, __, ___) => Container(
                  width: 48,
                  height: 64,
                  color: Theme.of(context).dividerColor,
                  child: const Icon(Icons.movie, size: 22),
                ),
              )
            : Container(
                width: 48,
                height: 64,
                color: Theme.of(context).dividerColor,
                child: const Icon(Icons.movie, size: 22),
              ),
      ),
      title: Text(
        e.number.isNotEmpty ? e.number : '(加载中)',
        style: const TextStyle(fontWeight: FontWeight.w600),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Text(
        e.title.isNotEmpty ? e.title : ' ',
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      trailing: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          _stateBadge(context, e.state),
          const SizedBox(height: 4),
          Text(_formatTime(e.viewedAt),
              style: TextStyle(
                  fontSize: 11, color: Theme.of(context).hintColor)),
        ],
      ),
      onTap: () => Navigator.push(
        context,
        MaterialPageRoute(
          builder: (_) => MovieDetailScreen(
            movieId: e.id,
            movieNumber: e.number,
          ),
        ),
      ),
    );
  }

  Widget _stateBadge(BuildContext context, String state) {
    final (label, color) = switch (state) {
      ('watched') => ('看过', Colors.green),
      ('watching') => ('观看中', Colors.orange),
      _ => ('浏览过', Theme.of(context).hintColor),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withOpacity(0.12),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Text(label,
          style: TextStyle(fontSize: 11, color: color)),
    );
  }

  String _formatTime(int epochMs) {
    final t = DateTime.fromMillisecondsSinceEpoch(epochMs);
    final now = DateTime.now();
    String two(int n) => n.toString().padLeft(2, '0');
    final hm = '${two(t.hour)}:${two(t.minute)}';
    if (t.year == now.year && t.month == now.month && t.day == now.day) {
      return hm;
    }
    return '${t.year}-${two(t.month)}-${two(t.day)} $hm';
  }
}
