import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../providers/user_state_provider.dart';
import 'list_detail_screen.dart';

/// 我的清单（与 JavDB 账号同步）：浏览、新建、改名、删除。
/// 点击清单进入内容页（网页版通道，需导入网页版 Cookie）。
class UserListsScreen extends StatefulWidget {
  const UserListsScreen({super.key});

  @override
  State<UserListsScreen> createState() => _UserListsScreenState();
}

class _UserListsScreenState extends State<UserListsScreen> {
  @override
  void initState() {
    super.initState();
    // 进入时刷新一次（离线时展示本地缓存）。
    Future.microtask(() =>
        context.read<UserStateProvider>().refreshLists());
  }

  Future<void> _createList() async {
    final controller = TextEditingController();
    final name = await showDialog<String>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('新建清单'),
        content: TextField(
          controller: controller,
          autofocus: true,
          decoration: const InputDecoration(hintText: '清单名称'),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () =>
                Navigator.of(dialogContext).pop(controller.text.trim()),
            child: const Text('创建'),
          ),
        ],
      ),
    );
    if (name == null || name.isEmpty) return;
    try {
      await context.read<UserStateProvider>().createList(name);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('创建失败：$e')),
      );
    }
  }

  Future<void> _renameList(String id, String current) async {
    final controller = TextEditingController(text: current);
    final name = await showDialog<String>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('重命名清单'),
        content: TextField(
          controller: controller,
          autofocus: true,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () =>
                Navigator.of(dialogContext).pop(controller.text.trim()),
            child: const Text('保存'),
          ),
        ],
      ),
    );
    if (name == null || name.isEmpty || name == current) return;
    try {
      await context.read<UserStateProvider>().renameList(id, name);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('重命名失败：$e')),
      );
    }
  }

  Future<void> _deleteList(String id, String name) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('删除清单'),
        content: Text('确定删除清单「$name」吗？此操作不可撤销。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(context).colorScheme.error,
            ),
            onPressed: () => Navigator.of(dialogContext).pop(true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    try {
      await context.read<UserStateProvider>().deleteList(id);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('删除失败：$e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final lists = context.watch<UserStateProvider>().lists;
    return Scaffold(
      appBar: AppBar(
        title: const Text('我的清单'),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: '刷新',
            onPressed: () => context.read<UserStateProvider>().refreshLists(),
          ),
          IconButton(
            icon: const Icon(Icons.add),
            tooltip: '新建清单',
            onPressed: _createList,
          ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: () => context.read<UserStateProvider>().refreshLists(),
        child: lists.isEmpty
            ? ListView(
                children: const [
                  SizedBox(height: 120),
                  Icon(Icons.playlist_add, size: 56, color: Colors.grey),
                  SizedBox(height: 12),
                  Center(child: Text('还没有清单，点右上角新建')),
                ],
              )
            : ListView.separated(
                itemCount: lists.length,
                separatorBuilder: (_, __) => const Divider(height: 1),
                itemBuilder: (context, i) {
                  final list = lists[i];
                  final id = (list['id'] as String?) ?? '';
                  final name = (list['name'] as String?) ?? '(未命名)';
                  final count = (list['movies_count'] as int?) ?? 0;
                  final isDefault = list['is_default'] == true;
                  return ListTile(
                    leading: Icon(
                      isDefault ? Icons.bookmark : Icons.playlist_play,
                      color: Theme.of(context).colorScheme.primary,
                    ),
                    title: Text(name),
                    subtitle: Text('$count 部影片'),
                    trailing: PopupMenuButton<String>(
                      onSelected: (action) {
                        if (action == 'rename') {
                          _renameList(id, name);
                        } else if (action == 'delete') {
                          _deleteList(id, name);
                        }
                      },
                      itemBuilder: (_) => const [
                        PopupMenuItem(value: 'rename', child: Text('重命名')),
                        PopupMenuItem(value: 'delete', child: Text('删除')),
                      ],
                    ),
                    onTap: () {
                      Navigator.of(context).push(MaterialPageRoute(
                        builder: (_) =>
                            ListDetailScreen(listId: id, listName: name),
                      ));
                    },
                  );
                },
              ),
      ),
    );
  }
}
