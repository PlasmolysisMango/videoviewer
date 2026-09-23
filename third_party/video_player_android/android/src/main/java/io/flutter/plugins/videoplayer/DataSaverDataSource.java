// Copyright 2013 The Flutter Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package io.flutter.plugins.videoplayer;

import android.content.Context;
import android.net.Uri;
import android.os.SystemClock;
import androidx.annotation.Nullable;
import androidx.annotation.OptIn;
import androidx.media3.common.util.UnstableApi;
import androidx.media3.datasource.DataSource;
import androidx.media3.datasource.DataSpec;
import androidx.media3.datasource.TransferListener;
import java.io.IOException;
import java.util.List;
import java.util.Map;

/**
 * [videoviewer] 下载速率限制：包装 HTTP {@link DataSource}，在 read() 返回后按「窗口平均
 * 速率」补偿等待，把蜂窝网络下的实际下载速率压到设置档位（见 {@link DataSaver}）。
 *
 * <p>仅在「设置开关 && 蜂窝网络 && 限速值大于 0」时生效，其余情况零开销直通；限速值可在
 * 播放中修改（窗口自动重置）。等待发生在 ExoPlayer 的加载线程上，不阻塞播放与 UI。
 */
@OptIn(markerClass = UnstableApi.class)
final class DataSaverDataSource implements DataSource {
  private final Context context;
  private final DataSource delegate;

  /** 限速是否正处于激活状态（用于在 Wi-Fi/蜂窝切换时重建窗口）。 */
  private boolean active = false;
  /** 当前限速窗口起点（elapsedRealtime ms），-1 表示尚未起算。 */
  private long windowStartMs = -1;
  /** 当前窗口内已读取的字节数。 */
  private long bytesInWindow;
  /** 窗口建立时的限速值（字节/秒），限速值变化时重置窗口。 */
  private int windowLimitBps = -1;

  DataSaverDataSource(Context context, DataSource delegate) {
    this.context = context.getApplicationContext();
    this.delegate = delegate;
  }

  @Override
  public void addTransferListener(TransferListener transferListener) {
    delegate.addTransferListener(transferListener);
  }

  @Override
  public long open(DataSpec dataSpec) throws IOException {
    // 新请求重新起算限速窗口，避免复用上一次请求的累计时间。
    windowStartMs = -1;
    return delegate.open(dataSpec);
  }

  @Override
  public int read(byte[] buffer, int offset, int length) throws IOException {
    int bytesRead = delegate.read(buffer, offset, length);
    if (bytesRead > 0) {
      throttle(bytesRead);
    }
    return bytesRead;
  }

  @Override
  @Nullable
  public Uri getUri() {
    return delegate.getUri();
  }

  @Override
  public Map<String, List<String>> getResponseHeaders() {
    return delegate.getResponseHeaders();
  }

  @Override
  public void close() throws IOException {
    delegate.close();
  }

  /**
   * 窗口平均速率限速：窗口内累计读取的目标耗时 = 字节数 / 限速值，实际耗时不足时补一次
   * 等待，使窗口的平均速率收敛到限速值。与限速无关的读取（Wi-Fi、开关关闭）不计入窗口。
   */
  private void throttle(int addedBytes) {
    if (!DataSaver.isLimitActive(context)) {
      active = false;
      return;
    }
    int limitBps = DataSaver.getSpeedLimitBytesPerSecond();
    if (limitBps <= 0) {
      active = false;
      return;
    }
    long now = SystemClock.elapsedRealtime();
    if (!active || windowStartMs < 0 || limitBps != windowLimitBps) {
      // 限速刚激活（如 Wi-Fi 切蜂窝）、新请求、或限速值变化：重建窗口。
      active = true;
      windowStartMs = now;
      bytesInWindow = 0;
      windowLimitBps = limitBps;
    }
    bytesInWindow += addedBytes;
    long targetElapsedMs = bytesInWindow * 1000L / limitBps;
    long sleepMs = targetElapsedMs - (now - windowStartMs);
    if (sleepMs > 0) {
      SystemClock.sleep(sleepMs);
    }
  }

  /** 包装任意 {@link DataSource.Factory}（本插件在 HTTP 层注入）。 */
  static final class Factory implements DataSource.Factory {
    private final Context context;
    private final DataSource.Factory delegateFactory;

    Factory(Context context, DataSource.Factory delegateFactory) {
      this.context = context.getApplicationContext();
      this.delegateFactory = delegateFactory;
    }

    @Override
    public DataSource createDataSource() {
      return new DataSaverDataSource(context, delegateFactory.createDataSource());
    }
  }
}
