import 'package:flutter/material.dart';
import '../services/logger.dart';

class LogScreen extends StatelessWidget {
  const LogScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('日志'),
        actions: [
          IconButton(
            icon: const Icon(Icons.delete_outline),
            onPressed: () {
              AppLogger.clear();
              (context as Element).markNeedsBuild();
            },
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
