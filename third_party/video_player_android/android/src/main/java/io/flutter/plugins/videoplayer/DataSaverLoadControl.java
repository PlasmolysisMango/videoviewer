// Copyright 2013 The Flutter Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package io.flutter.plugins.videoplayer;

import android.content.Context;
import androidx.annotation.OptIn;
import androidx.media3.common.C;
import androidx.media3.common.Timeline;
import androidx.media3.common.util.NullableType;
import androidx.media3.common.util.UnstableApi;
import androidx.media3.exoplayer.DefaultLoadControl;
import androidx.media3.exoplayer.LoadControl;
import androidx.media3.exoplayer.analytics.PlayerId;
import androidx.media3.exoplayer.source.MediaSource.MediaPeriodId;
import androidx.media3.exoplayer.source.TrackGroupArray;
import androidx.media3.exoplayer.trackselection.ExoTrackSelection;
import androidx.media3.exoplayer.upstream.Allocator;
import androidx.media3.exoplayer.upstream.DefaultAllocator;

/**
 * [videoviewer] 动态缓冲策略：默认策略与 4 档省流预读策略（10s/20s/30s/60s，见
 * {@link DataSaver#getBufferSeconds()}）各持一份 {@link DefaultLoadControl}，每次加载
 * 决策时按「设置开关 && 蜂窝网络」（见 {@link DataSaver}）选择其一。
 *
 * <p>ExoPlayer 的 LoadControl 在播放器创建后不可更换，而网络类型/开关/档位可以在播放中
 * 变化，因此这里把各档策略包在同一个实例里动态委托——无需重建播放器，也无需重建被包装
 * 实例（重建会丢失各自的加载状态表）：切到蜂窝时下一次加载决策即停止继续预读（缓冲自然
 * 消耗到档位水位），切回 Wi-Fi 后恢复默认预读。生命周期回调同时转发给全部策略，保证各自
 * 的加载状态表始终与播放器一致。
 */
@OptIn(markerClass = UnstableApi.class)
public final class DataSaverLoadControl implements LoadControl {
  private static final String TAG = "DataSaverLoadControl";

  /** 预读档位（秒）：预读上限 max 即档位值，min 为档位一半（缓冲低于一半才恢复加载）。 */
  private static final int[] BUFFER_TIERS_SECONDS = {10, 20, 30, 60};

  // 起播/重缓冲门槛保持默认（2.5s/5s），避免增加起播等待。
  private static final int BUFFER_FOR_PLAYBACK_MS = 2_500;
  private static final int BUFFER_FOR_PLAYBACK_AFTER_REBUFFER_MS = 5_000;

  private final Context context;
  private final DefaultAllocator allocator;
  private final DefaultLoadControl normalControl;
  private final DefaultLoadControl[] saverControls =
      new DefaultLoadControl[BUFFER_TIERS_SECONDS.length];

  public DataSaverLoadControl(Context context) {
    this.context = context.getApplicationContext();
    // 各档策略共享同一个分配器：切换策略时不改变已有缓冲的分配记账。
    this.allocator = new DefaultAllocator(/* trimOnReset= */ true, C.DEFAULT_BUFFER_SEGMENT_SIZE);
    this.normalControl = new DefaultLoadControl.Builder().setAllocator(allocator).build();
    for (int i = 0; i < BUFFER_TIERS_SECONDS.length; i++) {
      int tierSeconds = BUFFER_TIERS_SECONDS[i];
      this.saverControls[i] =
          new DefaultLoadControl.Builder()
              .setAllocator(allocator)
              .setBufferDurationsMs(
                  tierSeconds * 1000 / 2,
                  tierSeconds * 1000,
                  BUFFER_FOR_PLAYBACK_MS,
                  BUFFER_FOR_PLAYBACK_AFTER_REBUFFER_MS)
              .build();
    }
    io.flutter.Log.i(
        TAG,
        "installed, limitActive="
            + DataSaver.isLimitActive(this.context)
            + ", bufferSeconds="
            + DataSaver.getBufferSeconds());
  }

  private LoadControl active() {
    if (!DataSaver.isLimitActive(context)) {
      return normalControl;
    }
    int seconds = DataSaver.getBufferSeconds();
    for (int i = 0; i < BUFFER_TIERS_SECONDS.length; i++) {
      if (BUFFER_TIERS_SECONDS[i] == seconds) {
        return saverControls[i];
      }
    }
    // 档位为「不限制」（0）或未知值：使用默认策略。
    return normalControl;
  }

  @Override
  public void onPrepared(PlayerId playerId) {
    normalControl.onPrepared(playerId);
    for (DefaultLoadControl control : saverControls) {
      control.onPrepared(playerId);
    }
  }

  @Override
  public void onTracksSelected(
      Parameters parameters,
      TrackGroupArray trackGroups,
      @NullableType ExoTrackSelection[] trackSelections) {
    normalControl.onTracksSelected(parameters, trackGroups, trackSelections);
    for (DefaultLoadControl control : saverControls) {
      control.onTracksSelected(parameters, trackGroups, trackSelections);
    }
  }

  @Override
  public void onStopped(PlayerId playerId) {
    normalControl.onStopped(playerId);
    for (DefaultLoadControl control : saverControls) {
      control.onStopped(playerId);
    }
  }

  @Override
  public void onReleased(PlayerId playerId) {
    normalControl.onReleased(playerId);
    for (DefaultLoadControl control : saverControls) {
      control.onReleased(playerId);
    }
  }

  @Override
  public Allocator getAllocator() {
    return allocator;
  }

  @Override
  public long getBackBufferDurationUs(PlayerId playerId) {
    return active().getBackBufferDurationUs(playerId);
  }

  @Override
  public boolean retainBackBufferFromKeyframe(PlayerId playerId) {
    return active().retainBackBufferFromKeyframe(playerId);
  }

  @Override
  public boolean shouldContinueLoading(Parameters parameters) {
    return active().shouldContinueLoading(parameters);
  }

  @Override
  public boolean shouldStartPlayback(Parameters parameters) {
    return active().shouldStartPlayback(parameters);
  }

  @Override
  public boolean shouldContinuePreloading(
      Timeline timeline, MediaPeriodId mediaPeriodId, long bufferedDurationUs) {
    return active().shouldContinuePreloading(timeline, mediaPeriodId, bufferedDurationUs);
  }
}
