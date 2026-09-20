import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../providers/user_state_provider.dart';

/// 列表页右上角"去除看过的"开关：开启后当前列表过滤掉已看过（番号匹配）
/// 的影片。状态全局持久化，默认关闭。
class HideWatchedToggle extends StatelessWidget {
  const HideWatchedToggle({super.key});

  @override
  Widget build(BuildContext context) {
    final s = context.watch<UserStateProvider>();
    return IconButton(
      icon: Icon(
        s.hideWatched ? Icons.visibility_off : Icons.visibility_outlined,
        color: s.hideWatched ? Theme.of(context).colorScheme.primary : null,
      ),
      tooltip: s.hideWatched ? '恢复显示看过的' : '去除看过的',
      onPressed: s.toggleHideWatched,
    );
  }
}
