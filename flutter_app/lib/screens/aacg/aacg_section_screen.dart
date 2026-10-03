import 'package:flutter/material.dart';

import '../../api/aacg_models.dart';
import '../../api/client.dart';
import '../../services/backend_launcher.dart';
import '../../widgets/common_ui.dart';
import 'aacg_category_feed_screen.dart';
import 'aacg_feed_list.dart';

/// AACG 专栏主页：单页三 Tab（推荐/分类/搜索）。
class AacgSectionScreen extends StatefulWidget {
  const AacgSectionScreen({super.key});

  @override
  State<AacgSectionScreen> createState() => _AacgSectionScreenState();
}

class _AacgSectionScreenState extends State<AacgSectionScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabs;
  late final JavDBClient _client;

  /// 懒建标记：只构建访问过的 Tab（避免进入专栏即并发三个请求）。
  final Set<int> _visited = {0};

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    _tabs = TabController(length: 3, vsync: this);
    _tabs.addListener(_onTabChanged);
  }

  void _onTabChanged() {
    if (_visited.contains(_tabs.index)) return;
    setState(() => _visited.add(_tabs.index));
  }

  @override
  void dispose() {
    _tabs.removeListener(_onTabChanged);
    _tabs.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.newspaper, color: Theme.of(context).colorScheme.primary),
            const SizedBox(width: 8),
            const Text('AACG 专栏'),
          ],
        ),
        bottom: TabBar(
          controller: _tabs,
          tabs: const [Tab(text: '推荐'), Tab(text: '分类'), Tab(text: '搜索')],
        ),
      ),
      // IndexedStack 保活已建 Tab（切回不重抓）；未访问的 Tab 占位不发起请求。
      body: IndexedStack(
        index: _tabs.index,
        children: [
          if (_visited.contains(0))
            _HomeTab(client: _client)
          else
            const SizedBox.shrink(),
          if (_visited.contains(1))
            _CategoriesTab(client: _client)
          else
            const SizedBox.shrink(),
          if (_visited.contains(2))
            _SearchTab(client: _client)
          else
            const SizedBox.shrink(),
        ],
      ),
    );
  }
}

/// 推荐 Tab：首页信息流（含分页/下拉刷新）。
class _HomeTab extends StatelessWidget {
  final JavDBClient client;

  const _HomeTab({required this.client});

  @override
  Widget build(BuildContext context) {
    return AacgFeedList(load: (page) => client.aacgHome(page: page));
  }
}

/// 分类 Tab：分类导航列表，点击进入该分类的信息流。
class _CategoriesTab extends StatefulWidget {
  final JavDBClient client;

  const _CategoriesTab({required this.client});

  @override
  State<_CategoriesTab> createState() => _CategoriesTabState();
}

class _CategoriesTabState extends State<_CategoriesTab> {
  List<AacgCategory> _categories = const [];
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final categories = await widget.client.aacgCategories();
      if (!mounted) return;
      setState(() {
        _categories = categories;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _loading = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return ErrorRetryView(error: _error!, onRetry: _load);
    }
    if (_categories.isEmpty) {
      return Center(
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(Icons.inbox_outlined,
                size: 56, color: Theme.of(context).hintColor),
            const SizedBox(height: 12),
            Text('暂无分类',
                style: TextStyle(color: Theme.of(context).hintColor)),
          ],
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        physics: const AlwaysScrollableScrollPhysics(),
        itemCount: _categories.length,
        separatorBuilder: (_, __) => const Divider(height: 1),
        itemBuilder: (context, index) {
          final category = _categories[index];
          return ListTile(
            leading: const Icon(Icons.folder_outlined),
            title: Text(category.name),
            trailing: const Icon(Icons.chevron_right, size: 20),
            onTap: () => Navigator.push(
              context,
              MaterialPageRoute(
                builder: (context) => AacgCategoryFeedScreen(
                    name: category.name, url: category.url),
              ),
            ),
          );
        },
      ),
    );
  }
}

/// 搜索 Tab：关键词搜索专栏内容（提交后走分页信息流）。
class _SearchTab extends StatefulWidget {
  final JavDBClient client;

  const _SearchTab({required this.client});

  @override
  State<_SearchTab> createState() => _SearchTabState();
}

class _SearchTabState extends State<_SearchTab> {
  final _controller = TextEditingController();

  /// 已提交的关键词；空 = 还未搜索过。
  String _submitted = '';

  /// 提交代际：同词重搜也全量重建列表（key 变化触发重新加载）。
  int _gen = 0;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  void _submit() {
    final query = _controller.text.trim();
    if (query.isEmpty) return;
    setState(() {
      _submitted = query;
      _gen++;
    });
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
          child: TextField(
            controller: _controller,
            textInputAction: TextInputAction.search,
            onSubmitted: (_) => _submit(),
            decoration: InputDecoration(
              hintText: '输入关键词',
              prefixIcon: const Icon(Icons.search),
              suffixIcon: IconButton(
                icon: const Icon(Icons.send),
                tooltip: '搜索',
                onPressed: _submit,
              ),
              isDense: true,
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(12),
              ),
            ),
          ),
        ),
        Expanded(
          child: _submitted.isEmpty
              ? Center(
                  child: Column(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      Icon(Icons.search,
                          size: 56, color: Theme.of(context).hintColor),
                      const SizedBox(height: 12),
                      Text('输入关键词搜索专栏内容',
                          style: TextStyle(color: Theme.of(context).hintColor)),
                    ],
                  ),
                )
              : AacgFeedList(
                  key: ValueKey('$_submitted#$_gen'),
                  emptyText: '未找到相关内容',
                  load: (page) =>
                      widget.client.aacgSearch(_submitted, page: page),
                ),
        ),
      ],
    );
  }
}
