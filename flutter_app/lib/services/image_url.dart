import 'package:flutter/foundation.dart';

import 'backend_launcher.dart';

/// Flutter Web 用 canvas 绘制网络图片时需要读取像素数据，
/// 要求图片响应带 CORS 头；第三方图片 CDN（JavDB/MissAV 等）不提供，
/// 因此 Web 平台统一经后端 `/api/img` 同源代理转发。
/// 原生平台（Android/Windows）无此限制，直接使用原始 URL。
String resolveImageUrl(String url) {
  if (url.isEmpty || !kIsWeb) return url;
  return '${BackendLauncher.baseUrl}/api/img?url=${Uri.encodeComponent(url)}';
}
