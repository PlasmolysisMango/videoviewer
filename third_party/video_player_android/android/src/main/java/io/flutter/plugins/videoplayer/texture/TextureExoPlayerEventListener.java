// Copyright 2013 The Flutter Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package io.flutter.plugins.videoplayer.texture;

import androidx.annotation.NonNull;
import androidx.annotation.OptIn;
import androidx.media3.common.Format;
import androidx.media3.common.VideoSize;
import androidx.media3.exoplayer.ExoPlayer;
import io.flutter.plugins.videoplayer.ExoPlayerEventListener;
import io.flutter.plugins.videoplayer.VideoPlayerCallbacks;
import java.util.Objects;

public final class TextureExoPlayerEventListener extends ExoPlayerEventListener {
  private boolean surfaceProducerHandlesCropAndRotation;

  public TextureExoPlayerEventListener(
      @NonNull ExoPlayer exoPlayer,
      @NonNull VideoPlayerCallbacks events,
      boolean surfaceProducerHandlesCropAndRotation) {
    super(exoPlayer, events);
    this.surfaceProducerHandlesCropAndRotation = surfaceProducerHandlesCropAndRotation;
  }

  @Override
  protected void sendInitialized() {
    VideoSize videoSize = exoPlayer.getVideoSize();
    RotationDegrees rotationCorrection = RotationDegrees.ROTATE_0;
    int width = videoSize.width;
    int height = videoSize.height;
    if (width != 0 && height != 0) {
      // Anamorphic content is stored with non-square pixels, so the coded width has to be
      // scaled by the pixel aspect ratio to obtain the display width. ExoPlayer reports a
      // pixelWidthHeightRatio that already accounts for any applied rotation, so this is
      // correct regardless of the rotation correction computed below.
      // Clamped to at least one pixel so that a malformed ratio cannot report a zero width.
      // [videoviewer] Backport of upstream video_player_android 2.12.2 fix for
      // flutter/flutter#132934 (anamorphic videos reported coded size -> stretched render).
      float pixelWidthHeightRatio = videoSize.pixelWidthHeightRatio;
      if (pixelWidthHeightRatio > 0 && pixelWidthHeightRatio != 1f) {
        width = Math.max(1, Math.round(width * pixelWidthHeightRatio));
      }

      if (surfaceProducerHandlesCropAndRotation) {
        // When the SurfaceTexture backend for Impeller is used, the preview should already
        // be correctly rotated.
        rotationCorrection = RotationDegrees.ROTATE_0;
      } else {
        // The video's Format also provides a rotation correction that may be used to
        // correct the rotation, so we try to use that to correct the video rotation
        // when the ImageReader backend for Impeller is used.
        int rawVideoFormatRotation = getRotationCorrectionFromFormat(exoPlayer);

        try {
          rotationCorrection = RotationDegrees.fromDegrees(rawVideoFormatRotation);
        } catch (IllegalArgumentException e) {
          // Rotation correction other than 0, 90, 180, 270 reported by Format. Because this is
          // unexpected we apply no rotation correction.
          rotationCorrection = RotationDegrees.ROTATE_0;
        }
      }
    }
    events.onInitialized(width, height, exoPlayer.getDuration(), rotationCorrection.getDegrees());
  }

  private RotationDegrees getRotationCorrectionFromUnappliedRotation(
      RotationDegrees unappliedRotationDegrees) {
    RotationDegrees rotationCorrection = RotationDegrees.ROTATE_0;

    // Rotating the video with ExoPlayer does not seem to be possible with a Surface,
    // so inform the Flutter code that the widget needs to be rotated to prevent
    // upside-down playback for videos with unappliedRotationDegrees of 180 (other orientations
    // work correctly without correction).
    if (unappliedRotationDegrees == RotationDegrees.ROTATE_180) {
      rotationCorrection = unappliedRotationDegrees;
    }

    return rotationCorrection;
  }

  @OptIn(markerClass = androidx.media3.common.util.UnstableApi.class)
  // A video's Format and its rotation degrees are unstable because they are not guaranteed
  // the same implementation across API versions. It is possible that this logic may need
  // revisiting should the implementation change across versions of the Exoplayer API.
  private int getRotationCorrectionFromFormat(ExoPlayer exoPlayer) {
    Format videoFormat = Objects.requireNonNull(exoPlayer.getVideoFormat());
    return videoFormat.rotationDegrees;
  }
}
