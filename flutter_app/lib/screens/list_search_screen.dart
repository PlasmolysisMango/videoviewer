import 'package:flutter/material.dart';

import '../api/client.dart';
import '../services/backend_launcher.dart';
import '../services/logger.dart';
import 'list_detail_screen.dart';

/// 影单（合集）搜索页：关键词搜索 JavDB 社区影单，
/// 点进影单查看内容并可订阅。
class ListSearchScreen extends StatefulWidget {
  const ListSearchScreen({super.key, this.initialQuery = ''});

  final String initialQuery;

  @override
  State<ListSearchScreen> createState() => _ListSearchScreenState();
}

class _ListSearchScreenState extends State<ListSearchScreen> {
  late final JavDBClient _client;
  late final TextEditingController _searchController;
  List<Map<String, dynamic>> _lists = [];
  bool _loading = false;
  String? _error;
  // 分页：后端每页固定 20 条，底部"加载更多"逐页追加以展示全部结果。
  int _page = 1;
  bool _hasMore = false;
  bool _loadingMore = false;

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    _searchController = TextEditingController(text: widget.initialQuery);
    if (widget.initialQuery.isNotEmpty) _search();
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  Future<void> _search() async {
    final q = _searchController.text.trim();
    if (q.isEmpty) return;
    setState(() {
      _loading = true;
      _error = null;
      _page = 1;
    });
    try {
      final lists = await _client.searchLists(q);
      if (!mounted) return;
      setState(() {
        _lists = lists;
        _loading = false;
        // 整页说明大概率还有下一页。
        _hasMore = lists.length >= 20;
      });
      AppLogger.info('List search "$q": ${lists.length} lists');
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _loading = false;
      });
      AppLogger.error('List search failed', e);
    }
  }

  Future<void> _loadMore() async {
    if (_loadingMore || !_hasMore) return;
    final q = _searchController.text.trim();
    if (q.isEmpty) return;
    setState(() => _loadingMore = true);
    try {
      final lists = await _client.searchLists(q, page: _page + 1);
      if (!mounted) return;
      setState(() {
        _lists.addAll(lists);
        _page += 1;
        _hasMore = lists.length >= 20;
        _loadingMore = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _loadingMore = false);
      AppLogger.error('List search load more failed', e);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('搜索合集')),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
            child: TextField(
              controller: _searchController,
              textInputAction: TextInputAction.search,
              onSubmitted: (_) => _search(),
              decoration: InputDecoration(
                hintText: '搜索影单 / 合集关键词',
                prefixIcon: const Icon(Icons.search),
                suffixIcon: IconButton(
                  icon: const Icon(Icons.arrow_forward),
                  onPressed: _search,
                ),
                border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(14)),
                isDense: true,
              ),
            ),
          ),
          Expanded(
            child: _loading
                ? const Center(child: CircularProgressIndicator())
                : _error != null
                    ? Center(
                        child: Text('搜索失败: $_error',
                            textAlign: TextAlign.center))
                    : _lists.isEmpty
                        ? Center(
                            child: Text('无结果',
                                style: TextStyle(color: Theme.of(context).hintColor)))
                        : ListView.separated(
                            padding: const EdgeInsets.fromLTRB(16, 4, 16, 16),
                            itemCount:
                                _lists.length + (_hasMore ? 1 : 0),
                            separatorBuilder: (_, __) => const SizedBox(height: 8),
                            itemBuilder: (context, i) {
                              if (i >= _lists.length) {
                                // 尾部加载更多（整页结果时展示）。
                                return Padding(
                                  padding:
                                      const EdgeInsets.symmetric(vertical: 6),
                                  child: OutlinedButton.icon(
                                    onPressed:
                                        _loadingMore ? null : _loadMore,
                                    icon: _loadingMore
                                        ? const SizedBox(
                                            width: 16,
                                            height: 16,
                                            child: CircularProgressIndicator(
                                                strokeWidth: 2))
                                        : const Icon(Icons.expand_more),
                                    label: Text(_loadingMore
                                        ? '加载中...'
                                        : '加载更多'),
                                  ),
                                );
                              }
                              final l = _lists[i];
                              return _buildListCard(context, l);
                            },
                          ),
          ),
        ],
      ),
    );
  }

  Widget _buildListCard(BuildContext context, Map<String, dynamic> l) {
    return InkWell(
      borderRadius: BorderRadius.circular(14),
      onTap: () async {
        await Navigator.push(
          context,
          MaterialPageRoute(
            builder: (_) => ListDetailScreen(
              listId: l['id'] as String,
              listName: (l['name'] as String?) ?? '',
              // 带上搜索卡片上的影片数，订阅后首页不显示 0 部。
              moviesCount: (l['movies_count'] as num?)?.toInt() ?? 0,
            ),
          ),
        );
      },
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        decoration: BoxDecoration(
          color: Theme.of(context).colorScheme.surfaceContainerHighest,
          borderRadius: BorderRadius.circular(14),
        ),
        child: Row(
          children: [
            Container(
              width: 40,
              height: 40,
              decoration: BoxDecoration(
                color: const Color(0xFFFFD54F).withOpacity(0.15),
                borderRadius: BorderRadius.circular(12),
              ),
              child: const Icon(Icons.collections_bookmark,
                  color: Color(0xFFFFD54F), size: 22),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Text(
                (l['name'] as String?) ?? '',
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(
                    fontSize: 15, fontWeight: FontWeight.w600),
              ),
            ),
            const SizedBox(width: 8),
            Text('${l['movies_count'] ?? 0} 部',
                style: TextStyle(
                    fontSize: 12, color: Theme.of(context).hintColor)),
            Icon(Icons.chevron_right, color: Theme.of(context).hintColor),
          ],
        ),
      ),
    );
  }
}
