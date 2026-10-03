import 'package:flutter/material.dart';

import '../../api/client.dart';
import '../../services/backend_launcher.dart';
import 'aacg_feed_list.dart';

/// AACG 分类信息流页：展示单个分类下的文章列表（含分页/下拉刷新）。
class AacgCategoryFeedScreen extends StatefulWidget {
  final String name;
  final String url;

  const AacgCategoryFeedScreen({
    super.key,
    required this.name,
    required this.url,
  });

  @override
  State<AacgCategoryFeedScreen> createState() => _AacgCategoryFeedScreenState();
}

class _AacgCategoryFeedScreenState extends State<AacgCategoryFeedScreen> {
  late final JavDBClient _client;

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(widget.name)),
      body: AacgFeedList(
        load: (page) => _client.aacgFeed(widget.url, page: page),
        emptyText: '该分类暂无内容',
      ),
    );
  }
}
