// Copyright 2013 The Flutter Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package io.flutter.plugins.videoplayer;

import android.content.Context;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.os.SystemClock;

/**
 * [videoviewer] 移动网络流量保护开关与档位的状态持有者。
 *
 * <p>设置页的「移动网络下限制加载」开关（默认开启）及限速/预读档位通过 MethodChannel
 * 同步到这里；播放器创建时安装的 {@link DataSaverLoadControl}（缓冲水位）与
 * {@link DataSaverDataSource}（下载速率）会在每次决策时查询 {@link #isLimitActive(Context)}，
 * 从而在蜂窝网络下收紧预加载缓冲并限制下载速率，Wi-Fi 下保持 ExoPlayer 默认行为。
 * 网络类型查询带 TTL 缓存：决策调用频率很高，不能每次都走 ConnectivityManager。
 */
final class DataSaver {
  private static final String TAG = "DataSaver";

  /** 蜂窝判定缓存有效期：网络切换后最多 3 秒内自动生效。 */
  private static final long CACHE_TTL_MS = 3000;

  /** 限速档位「不限制」。 */
  static final int SPEED_UNLIMITED_MBPS = 0;

  /** 预读档位「不限制」：不干预缓冲水位，保持 ExoPlayer 默认。 */
  static final int BUFFER_UNLIMITED_SECONDS = 0;

  /** 用户设置，默认开启（与 Dart 端设置页默认值一致）。 */
  private static volatile boolean enabled = true;

  /** 蜂窝下的下载速率上限（MB/s），{@link #SPEED_UNLIMITED_MBPS} 表示不限制；默认 2 MB/s。 */
  private static volatile int speedLimitMbps = 2;

  /** 蜂窝下的预读上限（秒），{@link #BUFFER_UNLIMITED_SECONDS} 表示不限制；默认 20 秒。 */
  private static volatile int bufferSeconds = 20;

  private static volatile boolean cachedCellular = false;
  private static volatile long cacheAtMs = 0;

  private DataSaver() {}

  static void setEnabled(boolean value) {
    enabled = value;
    io.flutter.Log.i(TAG, "limit on mobile network = " + value);
  }

  static void setSpeedLimitMbps(int value) {
    speedLimitMbps = Math.max(value, 0);
    io.flutter.Log.i(TAG, "speed limit = " + speedLimitMbps + " MB/s");
  }

  static void setBufferSeconds(int value) {
    bufferSeconds = Math.max(value, 0);
    io.flutter.Log.i(TAG, "read ahead limit = " + bufferSeconds + "s");
  }

  /** 当前限速值（字节/秒），0 表示不限制。 */
  static int getSpeedLimitBytesPerSecond() {
    return speedLimitMbps * 1024 * 1024;
  }

  static int getBufferSeconds() {
    return bufferSeconds;
  }

  /** 开关开启且当前处于计费/蜂窝网络时才限制加载。 */
  static boolean isLimitActive(Context context) {
    return enabled && isOnCellularNetwork(context);
  }

  static boolean isOnCellularNetwork(Context context) {
    long now = SystemClock.elapsedRealtime();
    if (now - cacheAtMs < CACHE_TTL_MS) {
      return cachedCellular;
    }
    boolean cellular = queryCellular(context);
    cachedCellular = cellular;
    cacheAtMs = now;
    return cellular;
  }

  /**
   * 蜂窝判定：优先看传输类型；VPN 等场景下传输类型不可见时，回退到系统
   * 的「计费网络」判断（热点共享的 Wi-Fi 同样应视为流量场景）。
   */
  private static boolean queryCellular(Context context) {
    try {
      ConnectivityManager cm =
          (ConnectivityManager) context.getSystemService(Context.CONNECTIVITY_SERVICE);
      if (cm == null) {
        return false;
      }
      Network network = cm.getActiveNetwork();
      if (network == null) {
        return false;
      }
      NetworkCapabilities caps = cm.getNetworkCapabilities(network);
      if (caps != null && caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) {
        return true;
      }
      return cm.isActiveNetworkMetered();
    } catch (Exception e) {
      return false;
    }
  }
}
