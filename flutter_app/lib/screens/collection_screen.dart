import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../providers/subscription_provider.dart';
import 'list_detail_screen.dart';
import 'list_search_screen.dart';

/// 合集页（目录）：与题材页同构的二级结构——
/// 一级展示"搜索社区合集"入口与已订阅合集列表，
/// 点击合集进入二级影单详情页（ListDetailScreen）。
/// TOP250 属于榜单（见榜单页），不再放在合集页。
class CollectionScreen extends StatelessWidget {
  const CollectionScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final subs = context.watch<SubscriptionProvider>().byKind(kSubCollection);
    return Scaffold(
      appBar: AppBar(
        title: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.collections_bookmark,
                color: Theme.of(context).colorScheme.primary),
            const SizedBox(width: 8),
            const Text('合集'),
          ],
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.search),
            tooltip: '搜索合集',
            onPressed: () => Navigator.push(context,
                MaterialPageRoute(builder: (_) => const ListSearchScreen())),
          ),
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: '刷新订阅',
            onPressed: () =>
                context.read<SubscriptionProvider>().load(),
          ),
        ],
      ),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
        children: [
          // 搜索社区合集入口（一级 → ListSearchScreen 可继续进二级详情）
          _SearchEntry(onTap: () => Navigator.push(
            context,
            MaterialPageRoute(builder: (_) => const ListSearchScreen()),
          )),
          const SizedBox(height: 12),
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 4, 4, 8),
            child: Text('已订阅合集 (${subs.length})',
                style: Theme.of(context)
                    .textTheme
                    .titleSmall
                    ?.copyWith(fontWeight: FontWeight.w700)),
          ),
          if (subs.isEmpty)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 24),
              child: Center(
                child: Text(
                  '搜索并订阅社区合集后展示在这里',
                  style: TextStyle(color: Theme.of(context).hintColor),
                ),
              ),
            )
          else
            for (final s in subs) _SubscribedListCard(sub: s),
        ],
      ),
    );
  }
}

/// 搜索入口卡：与订阅卡同风格，点击进合集搜索页。
class _SearchEntry extends StatelessWidget {
  final VoidCallback onTap;

  const _SearchEntry({required this.onTap});

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(14),
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        decoration: BoxDecoration(
          color: Theme.of(context).cardColor,
          borderRadius: BorderRadius.circular(14),
          border: Border.all(color: Theme.of(context).dividerColor),
        ),
        child: Row(
          children: [
            Container(
              width: 44,
              height: 44,
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.primary.withOpacity(0.12),
                borderRadius: BorderRadius.circular(12),
              ),
              child: Icon(Icons.search,
                  color: Theme.of(context).colorScheme.primary, size: 24),
            ),
            const SizedBox(width: 12),
            const Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text('搜索社区合集',
                      style:
                          TextStyle(fontSize: 15, fontWeight: FontWeight.w600)),
                  SizedBox(height: 2),
                  Text('关键词搜索 JavDB 影单',
                      style: TextStyle(fontSize: 12, color: Colors.grey)),
                ],
              ),
            ),
            Icon(Icons.chevron_right, color: Theme.of(context).hintColor),
          ],
        ),
      ),
    );
  }
}

/// 已订阅合集卡片（灰色图钉标记订阅项）：点击进二级影单详情页。
class _SubscribedListCard extends StatelessWidget {
  final Map<String, dynamic> sub;

  const _SubscribedListCard({required this.sub});

  @override
  Widget build(BuildContext context) {
    final id = (sub['id'] as String?) ?? '';
    final name = (sub['name'] as String?) ?? id;
    final count = (sub['movies_count'] as num?)?.toInt() ?? 0;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Stack(
        children: [
          InkWell(
            borderRadius: BorderRadius.circular(14),
            onTap: () {
              if (id.isEmpty) return;
              Navigator.push(
                context,
                MaterialPageRoute(
                  builder: (_) => ListDetailScreen(
                      listId: id, listName: name, moviesCount: count),
                ),
              );
            },
            child: Container(
              padding:
                  const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
              decoration: BoxDecoration(
                color: Theme.of(context).cardColor,
                borderRadius: BorderRadius.circular(14),
                border: Border.all(color: Theme.of(context).dividerColor),
              ),
              child: Row(
                children: [
                  Container(
                    width: 38,
                    height: 38,
                    decoration: BoxDecoration(
                      color: const Color(0xFFFFD54F).withOpacity(0.15),
                      borderRadius: BorderRadius.circular(10),
                    ),
                    child: const Icon(Icons.collections_bookmark,
                        color: Color(0xFFFFD54F), size: 20),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Text(name,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(
                            fontSize: 14, fontWeight: FontWeight.w600)),
                  ),
                  Text('$count 部',
                      style: TextStyle(
                          fontSize: 12,
                          color: Theme.of(context).hintColor)),
                  const SizedBox(width: 4),
                  Icon(Icons.chevron_right,
                      color: Theme.of(context).hintColor),
                ],
              ),
            ),
          ),
          // 图钉：标记订阅项（灰色低饱和，不做醒目强调）
          const Positioned(
            top: 2,
            right: 4,
            child: Icon(Icons.push_pin, size: 14, color: Color(0xFF9AA3AD)),
          ),
        ],
      ),
    );
  }
}
