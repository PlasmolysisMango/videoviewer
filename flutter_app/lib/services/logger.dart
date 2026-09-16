import 'dart:developer' as developer;

/// Simple logging service for debugging
class AppLogger {
  AppLogger._();

  static final List<LogEntry> _logs = [];
  static const int _maxLogs = 500;

  static List<LogEntry> get logs => List.unmodifiable(_logs);

  static void info(String message) {
    _addLog('INFO', message);
    _print('[INFO] $message');
  }

  static void error(String message, [Object? error, StackTrace? stackTrace]) {
    final fullMessage = '$message${error != null ? ': $error' : ''}';
    _addLog('ERROR', fullMessage);
    _print('[ERROR] $fullMessage');
    if (stackTrace != null) {
      _print('[ERROR] StackTrace: $stackTrace');
    }
  }

  static void warning(String message) {
    _addLog('WARN', message);
    _print('[WARN] $message');
  }

  static void clear() {
    _logs.clear();
  }

  /// Print to both console and devtools timeline
  static void _print(String message) {
    // Use print for guaranteed console output
    // ignore: avoid_print
    print(message);
    // Also send to devtools timeline for structured viewing
    developer.log(message, name: 'AppLogger');
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
