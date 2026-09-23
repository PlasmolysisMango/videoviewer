import 'package:flutter/services.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// 移动网络加载限制设置（Android）。
///
/// 经 vendored video_player_android 的 `videoviewer/data_saver` 通道把开关与档位
/// 同步到原生，由 DataSaverLoadControl（预读水位）与 DataSaverDataSource（下载
/// 速率）在每次决策时读取，因此修改后无需重建播放器、即时生效。
///
/// 与 PlayerDefaults 相同：main 启动时预读；设置页修改后写盘并同步原生。
/// 非 Android 平台没有该通道（调用安全失败），Web 端不使用 video_player。
class DataSaver {
  static const _channel = MethodChannel('videoviewer/data_saver');

  static const _enabledKey = 'limit_mobile_loading';
  static const _speedKey = 'mobile_load_speed_mbps';
  static const _bufferKey = 'mobile_load_buffer_seconds';

  /// 开关：蜂窝网络下限制加载（默认开）。
  static bool limitOnMobile = true;

  /// 下载速率上限（MB/s），0 = 不限制；默认 2 MB/s。
  static int speedLimitMbps = 2;

  /// 预读上限（秒），0 = 不限制（保持播放器默认）；默认 20 秒。
  static int bufferSeconds = 20;

  /// 预读本地设置（main 启动时调用，不阻塞启动）。
  static Future<void> load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      limitOnMobile = prefs.getBool(_enabledKey) ?? true;
      speedLimitMbps = prefs.getInt(_speedKey) ?? 2;
      bufferSeconds = prefs.getInt(_bufferKey) ?? 20;
    } catch (_) {}
  }

  /// 修改开关（写盘 + 同步原生）。
  static Future<void> setEnabled(bool value) async {
    limitOnMobile = value;
    await _persist(_enabledKey, value);
    await syncToNative();
  }

  /// 修改下载速率上限（MB/s，0 = 不限制）。
  static Future<void> setSpeedLimitMbps(int value) async {
    speedLimitMbps = value < 0 ? 0 : value;
    await _persist(_speedKey, speedLimitMbps);
    await syncToNative();
  }

  /// 修改预读上限（秒，0 = 不限制）。
  static Future<void> setBufferSeconds(int value) async {
    bufferSeconds = value < 0 ? 0 : value;
    await _persist(_bufferKey, bufferSeconds);
    await syncToNative();
  }

  /// 把当前设置同步给原生侧（播放器创建前与设置修改后调用；非 Android 安全失败）。
  static Future<void> syncToNative() async {
    try {
      await _channel.invokeMethod('setEnabled', {'enabled': limitOnMobile});
      await _channel
          .invokeMethod('setSpeedLimitMbps', {'mbps': speedLimitMbps});
      await _channel
          .invokeMethod('setBufferSeconds', {'seconds': bufferSeconds});
    } catch (_) {}
  }

  static Future<void> _persist(String key, Object value) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      if (value is bool) {
        await prefs.setBool(key, value);
      } else if (value is int) {
        await prefs.setInt(key, value);
      }
    } catch (_) {}
  }
}
