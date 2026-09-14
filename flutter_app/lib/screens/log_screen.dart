import 'dart:convert';
import 'package:flutter/material.dart';
import '../api/client.dart';
import '../services/backend_launcher.dart';
import '../services/logger.dart';

class LogScreen extends StatefulWidget {
  const LogScreen({super.key});

  @override
  State<LogScreen> createState() => _LogScreenState();
}

class _LogScreenState extends State<LogScreen> {
  bool _isUploading = false;

  Future<void> _uploadLogs() async {
    if (AppLogger.logs.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('没有日志可上传')),
      );
      return;
    }

    setState(() => _isUploading = true);

    try {
      final client = JavDBClient(BackendLauncher.baseUrl);
      
      // Convert logs to JSON
      final logData = jsonEncode({
        'timestamp': DateTime.now().toIso8601String(),
        'logs': AppLogger.logs.map((log) => {
          'level': log.level,
          'message': log.message,
          'timestamp': log.timestamp.toIso8601String(),
        }).toList(),
      });

      final result = await client.uploadLog(logData);
      
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('日志已上传: ${result['file']}'),
            backgroundColor: Colors.green,
          ),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('上传失败: $e'),
            backgroundColor: Colors.red,
          ),
        );
      }
    } finally {
      if (mounted) {
        setState(() => _isUploading = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('日志'),
        actions: [
          IconButton(
            icon: _isUploading
                ? const SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Icon(Icons.cloud_upload),
            onPressed: _isUploading ? null : _uploadLogs,
            tooltip: '上传日志到服务器',
          ),
          IconButton(
            icon: const Icon(Icons.delete_outline),
            onPressed: () {
              AppLogger.clear();
              setState(() {});
            },
            tooltip: '清空日志',
          ),
        ],
      ),
      body: AppLogger.logs.isEmpty
          ? const Center(child: Text('暂无日志'))
          : ListView.builder(
              itemCount: AppLogger.logs.length,
              itemBuilder: (context, index) {
                final log = AppLogger.logs[index];
                return ListTile(
                  dense: true,
                  leading: Icon(
                    log.level == 'ERROR'
                        ? Icons.error
                        : log.level == 'WARN'
                            ? Icons.warning
                            : Icons.info,
                    color: log.level == 'ERROR'
                        ? Colors.red
                        : log.level == 'WARN'
                            ? Colors.orange
                            : Colors.blue,
                    size: 20,
                  ),
                  title: Text(
                    log.message,
                    style: const TextStyle(fontSize: 12),
                    maxLines: 3,
                    overflow: TextOverflow.ellipsis,
                  ),
                  subtitle: Text(
                    '${log.timestamp.hour.toString().padLeft(2, '0')}:${log.timestamp.minute.toString().padLeft(2, '0')}:${log.timestamp.second.toString().padLeft(2, '0')}',
                    style: const TextStyle(fontSize: 10, color: Colors.grey),
                  ),
                );
              },
            ),
    );
  }
}
