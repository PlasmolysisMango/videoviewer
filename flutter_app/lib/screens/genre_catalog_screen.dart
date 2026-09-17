import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/client.dart';
import '../providers/subscription_provider.dart';
import '../services/backend_launcher.dart';
import '../services/logger.dart';
import 'genre_screen.dart';

/// 题材大类页：左侧分组导航栏（角色/主題/服裝…）+ 右侧题材卡片网格，
/// 两种格式明显区分；点击题材卡进入题材影片页，右上角书签订阅到首页。
/// tag 分组来自 mobile API（匿名可拉），浏览具体题材影片需要网页版 Cookie。
class GenreCatalogScreen extends StatefulWidget {
  const GenreCatalogScreen({super.key});

  @override
  State<GenreCatalogScreen> createState() => _GenreCatalogScreenState();
}

/// 订阅标记统一用低饱和灰，不做醒目强调。
const _subMarkColor = Color(0xFF9AA3AD);

class _GenreCatalogScreenState extends State<GenreCatalogScreen> {
  late final JavDBClient _client;
  List<Map<String, dynamic>> _groups = [];
  int _groupIndex = 0;
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    context.read<SubscriptionProvider>().ensureLoaded();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final data = await _client.getTags();
      final groups = (data['tags'] as List?)
              ?.map((e) => e as Map<String, dynamic>)
              .where((g) =>
                  ((g['web_group_id'] as String?) ?? '').isNotEmpty &&
                  ((g['options'] as List?)?.isNotEmpty ?? false))
              .toList() ??
          const <Map<String, dynamic>>[];
      if (!mounted) return;
      setState(() {
        _groups = groups;
        _groupIndex = 0;
        _loading = false;
      });
      AppLogger.info('Genre catalog loaded: ${groups.length} groups');
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _loading = false;
      });
      AppLogger.error('Failed to load genre groups', e);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('题材')),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text('加载失败: $_error',
                          textAlign: TextAlign.center,
                          style: TextStyle(
                              color: Theme.of(context).colorScheme.error,
                              fontSize: 12)),
                      const SizedBox(height: 12),
                      OutlinedButton(onPressed: _load, child: const Text('重试')),
                    ],
                  ),
                )
              : Row(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    _buildGroupRail(context),
                    const VerticalDivider(width: 1),
                    Expanded(child: _buildTagPanel(context)),
                  ],
                ),
    );
  }

  // ------------------------------------------------------------- 左侧分组栏

  /// 分组导航栏：竖排列表，选中项主色高亮 + 左侧指示条，与右侧卡片网格
  /// 形成完全不同的视觉层级。
  Widget _buildGroupRail(BuildContext context) {
    final index = _groupIndex < _groups.length ? _groupIndex : 0;
    final primary = Theme.of(context).colorScheme.primary;
    return SizedBox(
      width: 104,
      child: Container(
        color: Theme.of(context).colorScheme.brightness == Brightness.dark
            ? const Color(0xFF171A20)
            : const Color(0xFFF7F8FA),
        child: ListView.builder(
          itemCount: _groups.length,
          padding: const EdgeInsets.symmetric(vertical: 8),
          itemBuilder: (context, i) {
            final selected = i == index;
            return InkWell(
              onTap: () => setState(() => _groupIndex = i),
              child: Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 12, vertical: 14),
                decoration: BoxDecoration(
                  color: selected ? primary.withOpacity(0.08) : null,
                  border: Border(
                    left: BorderSide(
                      width: 3,
                      color: selected ? primary : Colors.transparent,
                    ),
                  ),
                ),
                child: Text(
                  (_groups[i]['name'] as String?) ?? '',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    fontSize: 13,
                    fontWeight:
                        selected ? FontWeight.w700 : FontWeight.w400,
                    color: selected ? primary : null,
                  ),
                ),
              ),
            );
          },
        ),
      ),
    );
  }

  // ------------------------------------------------------------- 右侧题材区

  /// 题材卡片网格：圆角卡片流式排布，右上角灰色书签标记订阅状态，
  /// 点卡片进题材页，点书签切换订阅。
  Widget _buildTagPanel(BuildContext context) {
    final subs = context.watch<SubscriptionProvider>();
    final index = _groupIndex < _groups.length ? _groupIndex : 0;
    final group = _groups[index];
    final groupId = (group['web_group_id'] as String?) ?? '';
    final options = (group['options'] as List?) ?? const [];
    if (options.isEmpty) {
      return Center(
        child: Text('暂无数据',
            style: TextStyle(color: Theme.of(context).hintColor)),
      );
    }
    return GridView.builder(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.all(12),
      gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
        maxCrossAxisExtent: 168,
        mainAxisSpacing: 10,
        crossAxisSpacing: 10,
        childAspectRatio: 3.0,
      ),
      itemCount: options.length,
      itemBuilder: (context, i) {
        final o = options[i] as Map<String, dynamic>;
        final tagId = (o['id'] as String?) ?? '';
        final name = (o['name'] as String?) ?? '';
        final subscribed = subs.isSubscribed(kSubGenre, tagId);
        return _buildTagCard(context, subs, groupId, tagId, name,
            subscribed: subscribed);
      },
    );
  }

  Widget _buildTagCard(
    BuildContext context,
    SubscriptionProvider subs,
    String groupId,
    String tagId,
    String name, {
    required bool subscribed,
  }) {
    return InkWell(
      borderRadius: BorderRadius.circular(12),
      onTap: () => Navigator.push(
        context,
        MaterialPageRoute(
          builder: (_) =>
              GenreScreen(groupId: groupId, tagId: tagId, title: name),
        ),
      ),
      child: Container(
        decoration: BoxDecoration(
          color: Theme.of(context).cardColor,
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: Theme.of(context).dividerColor),
        ),
        child: Stack(
          children: [
            Center(
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 10),
                child: Text(name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(fontSize: 13)),
              ),
            ),
            Positioned(
              top: 0,
              right: 0,
              child: InkWell(
                borderRadius: const BorderRadius.only(
                  topRight: Radius.circular(12),
                  bottomLeft: Radius.circular(12),
                ),
                onTap: () => _toggleSubscribe(subs, groupId, tagId, name,
                    subscribed: subscribed),
                child: Padding(
                  padding: const EdgeInsets.all(5),
                  child: Icon(
                    subscribed
                        ? Icons.bookmark_added
                        : Icons.bookmark_add_outlined,
                    size: 16,
                    color: subscribed
                        ? _subMarkColor
                        : Theme.of(context).hintColor,
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _toggleSubscribe(SubscriptionProvider subs, String groupId,
      String tagId, String name,
      {required bool subscribed}) async {
    try {
      if (subscribed) {
        await subs.unsubscribe(kSubGenre, tagId);
        if (mounted) _toast('已取消订阅');
      } else {
        await subs.subscribeGenre(groupId, tagId, name);
        if (mounted) _toast('已订阅，可在首页查看');
      }
    } catch (e) {
      AppLogger.error('Failed to toggle genre subscription', e);
      if (mounted) _toast('操作失败: $e', error: true);
    }
  }

  void _toast(String msg, {bool error = false}) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(msg), backgroundColor: error ? Colors.red : null),
    );
  }
}
