import 'package:flutter/material.dart';

import '../api/models.dart';

/// 非 Web 平台的编译桩：原生端直接使用 [VideoPlayerScreen]，
/// 本桩永远不会被实际展示。
class HlsPlayerScreen extends StatelessWidget {
  final List<VideoStream> streams;
  final String title;

  const HlsPlayerScreen(
      {super.key, required this.streams, required this.title});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(title)),
      body: const Center(child: Text('HLS 播放仅支持 Web 平台')),
    );
  }
}
