import 'package:flutter/foundation.dart';

/// Simple logging service for debugging
class AppLogger {
  AppLogger._();

  static final List<LogEntry> _logs = [];
  static const int _maxLogs = 500;

  static List<LogEntry> get logs => List.unmodifiable(_logs);

  static void info(String message) {
    _addLog('INFO', message);
    debugPrint('[INFO] $message');
  }

  static void error(String message, [Object? error]) {
    _addLog('ERROR', '$message${error != null ? ': $error' : ''}');
    debugPrint('[ERROR] $message $error');
  }

  static void warning(String message) {
    _addLog('WARN', message);
    debugPrint('[WARN] $message');
  }

  static void clear() {
    _logs.clear();
  }

  static void _addLog(String level, String message) {
    _logs.add(LogEntry(
      level: level,
      message: message,
      timestamp: DateTime.now(),
    ));
    if (_logs.length > _maxLogs) {
      _logs.removeAt(0);
    }
  }
}

class LogEntry {
  final String level;
  final String message;
  final DateTime timestamp;

  LogEntry({
    required this.level,
    required this.message,
    required this.timestamp,
  });
}
