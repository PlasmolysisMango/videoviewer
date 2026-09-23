import 'dart:async';

import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../services/backend_launcher.dart';
import '../services/download_settings.dart';
import '../services/image_url.dart';
import '../services/logger.dart';
import '../widgets/common_ui.dart';
import 'video_player_screen.dart';

/// 下载管理页：任务列表（进度轮询）、暂停/继续/取消/重试/删除，点已完成任务直接播放。
///
/// 后端为异步队列（见 pkg/goserver/downloads.go）：任务只记录 code/源/
/// 变体/清晰度，下载时重新解析播放流；已完成的文件经后端 /file 端点
/// （支持 Range）流式播放，三端一致。
class DownloadsScreen extends StatefulWidget {
  const DownloadsScreen({super.key});

  @override
  State<DownloadsScreen> createState() => _DownloadsScreenState();
}

class _DownloadsScreenState extends State<DownloadsScreen>
    with WidgetsBindingObserver {
  late final JavDBClient _client;
  List<DownloadTask> _tasks = [];
  bool _loading = true;
  bool _foreground = true;
  bool _fetching = false;
  String? _error;
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _client = JavDBClient(BackendLauncher.baseUrl);
    _refresh();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _poll?.cancel();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      _foreground = true;
      // 从系统授权页返回时刷新存储授权状态与位置列表。
      DownloadSettings.refreshStorage().then((_) {
        if (mounted) setState(() {});
      });
      _refresh();
    } else if (state == AppLifecycleState.paused) {
      _foreground = false;
      _poll?.cancel();
      _poll = null;
    }
  }

  /// 拉取任务列表（页面可见时轮询：有活动任务 1s，全部结束 5s）。
  Future<void> _refresh() async {
    if (_fetching) return;
    _fetching = true;
    try {
      final tasks = await _client.listDownloads();
      if (!mounted) return;
      setState(() {
        _tasks = tasks;
        _error = null;
        _loading = false;
      });
    } catch (e) {
      AppLogger.warning('List downloads failed: $e');
      if (!mounted) return;
      setState(() {
        _loading = false;
        // 已有数据时保留列表，仅在首屏为空时展示错误态
        if (_tasks.isEmpty) _error = e.toString();
      });
    } finally {
      _fetching = false;
      _schedulePoll();
    }
  }

  void _schedulePoll() {
    _poll?.cancel();
    if (!mounted || !_foreground) return;
    final hasActive = _tasks.any((t) => t.isActive);
    _poll = Timer(Duration(seconds: hasActive ? 1 : 5), _refresh);
  }

  Future<void> _cancel(DownloadTask t) async {
    try {
      await _client.cancelDownload(t.id);
    } catch (e) {
      AppLogger.warning('Cancel download failed: $e');
      _toast('取消失败: $e');
    }
    _refresh();
  }

  Future<void> _retry(DownloadTask t) async {
    try {
      await _client.retryDownload(t.id);
    } catch (e) {
      AppLogger.warning('Retry download failed: $e');
      _toast('重试失败: $e');
    }
    _refresh();
  }

  Future<void> _pause(DownloadTask t) async {
    try {
      await _client.pauseDownload(t.id);
    } catch (e) {
      AppLogger.warning('Pause download failed: $e');
      _toast('暂停失败: $e');
    }
    _refresh();
  }

  Future<void> _resume(DownloadTask t) async {
    try {
      await _client.resumeDownload(t.id);
    } catch (e) {
      AppLogger.warning('Resume download failed: $e');
      _toast('继续失败: $e');
    }
    _refresh();
  }

  /// 删除任务：已完成的询问是否同时删除文件；进行中的先确认。
  Future<void> _confirmDelete(DownloadTask t) async {
    var deleteFile = false;
    if (t.isDone) {
      final choice = await showDialog<String>(
        context: context,
        builder: (dialogContext) => AlertDialog(
          title: const Text('删除任务'),
          content: const Text('是否同时删除已下载的视频文件？'),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext, 'cancel'),
              child: const Text('取消'),
            ),
            TextButton(
              onPressed: () => Navigator.pop(dialogContext, 'record'),
              child: const Text('仅删除记录'),
            ),
            TextButton(
              onPressed: () => Navigator.pop(dialogContext, 'file'),
              child: const Text('记录和文件'),
            ),
          ],
        ),
      );
      if (choice == null || choice == 'cancel') return;
      deleteFile = choice == 'file';
    } else if (t.isActive || t.isPaused) {
      final ok = await showDialog<bool>(
        context: context,
        builder: (dialogContext) => AlertDialog(
          title: const Text('删除任务'),
          content: Text(
              t.isPaused ? '该任务已暂停，删除将中断任务，是否继续？' : '该任务正在下载，删除将中断下载，是否继续？'),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext, false),
              child: const Text('取消'),
            ),
            TextButton(
              onPressed: () => Navigator.pop(dialogContext, true),
              child: const Text('删除'),
            ),
          ],
        ),
      );
      if (ok != true) return;
    }
    try {
      await _client.deleteDownload(t.id, deleteFile: deleteFile);
    } catch (e) {
      AppLogger.warning('Delete download failed: $e');
      _toast('删除失败: $e');
    }
    _refresh();
  }

  /// 播放已下载文件：统一走后端 /file 端点（支持 Range seek）。
  void _play(DownloadTask t) {
    final stream = VideoStream(
      url: '${BackendLauncher.baseUrl}/api/downloads/${t.id}/file',
    );
    Navigator.push(
      context,
      MaterialPageRoute(
        builder: (_) => VideoPlayerScreen(
          streams: [stream],
          title: t.title.isNotEmpty ? t.title : t.code,
          movieNumber: t.code,
          cover: t.cover,
        ),
      ),
    );
  }

  void _toast(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('下载'),
        actions: [
          IconButton(
            tooltip: '刷新',
            icon: const Icon(Icons.refresh),
            onPressed: _refresh,
          ),
        ],
      ),
      body: _buildBody(),
    );
  }

  Widget _buildBody() {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null && _tasks.isEmpty) {
      return ErrorRetryView(error: _error!, onRetry: _refresh);
    }
    if (_tasks.isEmpty) {
      final theme = Theme.of(context);
      return Center(
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(Icons.download_outlined, size: 48, color: theme.hintColor),
            const SizedBox(height: 12),
            const Text('暂无下载任务'),
            const SizedBox(height: 4),
            Text(
              '在影片详情页点击「下载」，选择变体与清晰度后加入队列',
              style: TextStyle(fontSize: 12, color: theme.hintColor),
            ),
          ],
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: _refresh,
      child: ListView.builder(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
        itemCount: _tasks.length + 1,
        itemBuilder: (context, i) {
          if (i == 0) return _buildHeader();
          return _buildTaskCard(_tasks[i - 1]);
        },
      ),
    );
  }

  /// 列表头部：当前存储位置与权限提示。
  Widget _buildHeader() {
    final theme = Theme.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Icon(Icons.folder_outlined, size: 15, color: theme.hintColor),
            const SizedBox(width: 6),
            Expanded(
              child: Text(
                '保存至 ${DownloadSettings.storage?.label ?? '默认目录'}',
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(fontSize: 12, color: theme.hintColor),
              ),
            ),
          ],
        ),
        if (DownloadSettings.needsPermission) ...[
          const SizedBox(height: 8),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 12),
            decoration: BoxDecoration(
              color: theme.colorScheme.errorContainer.withValues(alpha: 0.5),
              borderRadius: BorderRadius.circular(10),
            ),
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    '尚未授予存储权限，下载可能失败',
                    style:
                        TextStyle(fontSize: 12, color: theme.colorScheme.error),
                  ),
                ),
                TextButton(
                  onPressed: () => StorageService.requestPermission(),
                  child: const Text('去授权'),
                ),
              ],
            ),
          ),
        ],
        const SizedBox(height: 12),
      ],
    );
  }

  Widget _buildTaskCard(DownloadTask t) {
    final theme = Theme.of(context);
    return Container(
      margin: const EdgeInsets.only(bottom: 10),
      decoration: cardDecoration(context),
      padding: const EdgeInsets.all(10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              ClipRRect(
                borderRadius: BorderRadius.circular(10),
                child: SizedBox(
                  width: 56,
                  height: 74,
                  child: t.cover.isNotEmpty
                      ? CachedNetworkImage(
                          imageUrl: resolveImageUrl(t.cover),
                          fit: BoxFit.cover,
                          memCacheWidth: 168, // 56dp×3x：列表缩略图按显示尺寸解码
                          placeholder: (_, __) =>
                              Container(color: theme.dividerColor),
                          errorWidget: (_, __, ___) => _coverFallback(theme),
                        )
                      : _coverFallback(theme),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Expanded(
                          child: Text(
                            t.code.isNotEmpty ? t.code : '未知番号',
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: const TextStyle(
                                fontSize: 15, fontWeight: FontWeight.w700),
                          ),
                        ),
                        _statusBadge(t),
                      ],
                    ),
                    const SizedBox(height: 3),
                    Text(
                      t.title.isNotEmpty ? t.title : ' ',
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                          fontSize: 12, color: theme.hintColor, height: 1.3),
                    ),
                    const SizedBox(height: 5),
                    _metaRow(t),
                  ],
                ),
              ),
            ],
          ),
          if (t.isActive || t.isPaused || t.isDone) ...[
            const SizedBox(height: 8),
            _progressBar(t),
          ],
          if (t.error.isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(
              '失败原因: ${t.error}',
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(fontSize: 11, color: theme.colorScheme.error),
            ),
          ],
          const SizedBox(height: 2),
          Align(
            alignment: Alignment.centerRight,
            child: _actionButtons(t),
          ),
        ],
      ),
    );
  }

  Widget _coverFallback(ThemeData theme) => Container(
        color: theme.dividerColor,
        child: const Icon(Icons.movie, size: 22),
      );

  /// 状态徽标：颜色随状态（下载中/已完成绿/失败红）。
  Widget _statusBadge(DownloadTask t) {
    final theme = Theme.of(context);
    final (label, color) = switch (t.status) {
      'queued' => ('排队中', theme.hintColor),
      'running' => ('下载中', theme.colorScheme.primary),
      'done' => ('已完成', Colors.green),
      'failed' => ('失败', theme.colorScheme.error),
      'canceled' => ('已取消', theme.hintColor),
      'paused' => ('已暂停', Colors.orange),
      _ => (t.status, theme.hintColor),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Text(label, style: TextStyle(fontSize: 11, color: color)),
    );
  }

  /// 元信息行：变体 / 清晰度 / 文件大小（已完成）。
  Widget _metaRow(DownloadTask t) {
    final theme = Theme.of(context);
    final parts = <String>[
      if (t.variantLabel.isNotEmpty) t.variantLabel,
      if (t.qualityLabel.isNotEmpty) t.qualityLabel,
      if (t.isDone && t.size > 0) _formatSize(t.size),
    ];
    if (parts.isEmpty) return const SizedBox.shrink();
    return Wrap(
      spacing: 6,
      children: [
        for (final p in parts)
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
            decoration: BoxDecoration(
              color: theme.dividerColor,
              borderRadius: BorderRadius.circular(6),
            ),
            child:
                Text(p, style: TextStyle(fontSize: 10, color: theme.hintColor)),
          ),
      ],
    );
  }

  Widget _progressBar(DownloadTask t) {
    final theme = Theme.of(context);
    final unknownRunning = t.status == 'running' && t.totalSegments == 0;
    return Row(
      children: [
        Expanded(
          child: ClipRRect(
            borderRadius: BorderRadius.circular(4),
            child: LinearProgressIndicator(
              value: unknownRunning ? null : t.progress,
              minHeight: 6,
              backgroundColor: theme.dividerColor,
            ),
          ),
        ),
        const SizedBox(width: 8),
        Text(
          switch (t.status) {
            'running' =>
              t.totalSegments > 0 ? '${(t.progress * 100).round()}%' : '准备中…',
            'paused' =>
              t.totalSegments > 0 ? '${(t.progress * 100).round()}%' : '',
            'done' => '100%',
            _ => '',
          },
          style: TextStyle(fontSize: 11, color: theme.hintColor),
        ),
      ],
    );
  }

  /// 操作按钮：播放（已完成）/ 重试（失败/取消）/ 暂停（进行中）/ 继续（已暂停）/
  /// 取消（未结束）/ 删除（全部）。
  Widget _actionButtons(DownloadTask t) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        if (t.isDone)
          TextButton.icon(
            onPressed: () => _play(t),
            icon: const Icon(Icons.play_arrow, size: 18),
            label: const Text('播放'),
          ),
        if (t.status == 'failed' || t.status == 'canceled')
          TextButton.icon(
            onPressed: () => _retry(t),
            icon: const Icon(Icons.refresh, size: 18),
            label: const Text('重试'),
          ),
        if (t.isActive)
          TextButton.icon(
            onPressed: () => _pause(t),
            icon: const Icon(Icons.pause, size: 18),
            label: const Text('暂停'),
          ),
        if (t.isPaused)
          TextButton.icon(
            onPressed: () => _resume(t),
            icon: const Icon(Icons.play_arrow, size: 18),
            label: const Text('继续'),
          ),
        if (t.isActive || t.isPaused)
          TextButton.icon(
            onPressed: () => _cancel(t),
            icon: const Icon(Icons.close, size: 18),
            label: const Text('取消'),
          ),
        TextButton.icon(
          onPressed: () => _confirmDelete(t),
          icon: const Icon(Icons.delete_outline, size: 18),
          label: const Text('删除'),
        ),
      ],
    );
  }

  String _formatSize(int bytes) {
    if (bytes <= 0) return '';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    var size = bytes.toDouble();
    var i = 0;
    while (size >= 1024 && i < units.length - 1) {
      size /= 1024;
      i++;
    }
    final digits = (size >= 100 || i == 0) ? 0 : 1;
    return '${size.toStringAsFixed(digits)} ${units[i]}';
  }
}
