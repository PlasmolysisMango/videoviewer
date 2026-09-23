import 'dart:convert';
import 'package:http/http.dart' as http;

import '../services/backend_launcher.dart';
import 'models.dart';

class JavDBClient {
  final String baseUrl;
  String? _token;

  // 自愈 HTTP client：本机内嵌后端被系统冻结/回收导致连接级失败时，
  // 先拉起后端再重试一次（仅非 Web 平台生效）。
  final http.Client _http = createHealingClient();

  JavDBClient(this.baseUrl);

  String? get token => _token;

  void setToken(String? token) {
    _token = token;
  }

  Map<String, String> get _headers => {
        'Content-Type': 'application/json',
        if (_token != null) 'Authorization': 'Bearer $_token',
      };

  Future<Map<String, dynamic>> login(String username, String password) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/login'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({'username': username, 'password': password}),
    );

    if (response.statusCode == 200) {
      final data = jsonDecode(response.body);
      _token = data['token'];
      return data;
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Login failed');
    }
  }

  /// 影片/演员搜索：scope 传 'movie'（默认）或 'actor'。
  /// sort 可选：relevance / newest / oldest / highest / lowest / most_magnets。
  Future<Map<String, dynamic>> search(String query,
      {int page = 1,
      int limit = 20,
      String scope = 'movie',
      String? sort}) async {
    final params = <String, String>{
      'q': query,
      'page': page.toString(),
      'limit': limit.toString(),
      'scope': scope,
    };
    if (sort != null && sort.isNotEmpty) {
      params['sort'] = sort;
    }
    final uri =
        Uri.parse('$baseUrl/api/search').replace(queryParameters: params);
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Search failed');
    }
  }

  /// 获取影片详情。cast=true 仅拉详情（跳过磁链查询），
  /// 供搜索结果渐进补演员使用，速度快得多。
  Future<Map<String, dynamic>> getMovie(String id, {bool cast = false}) async {
    final uri = Uri.parse('$baseUrl/api/movie/$id')
        .replace(queryParameters: cast ? {'cast': '1'} : null);
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Get movie failed');
    }
  }

  /// 相似推荐：/api/similar/{id}。后端按同女演员 / 同系列 / 同题材
  /// 三维度并行聚合打分；未导入网页版 Cookie 时题材维度静默跳过。
  /// 返回 {"similar": [{"movie": {...}, "reason": "..."}]}。
  Future<Map<String, dynamic>> getSimilarMovies(String movieId) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/similar/$movieId'),
      headers: _headers,
    );

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Get similar movies failed');
    }
  }

  /// JavDB 用户评论：/api/reviews/{id}?page=&sort=hotly|latest。
  /// 走 app API（公开可用），返回 {"reviews": [...], "current_page": n, "total": n}。
  Future<Map<String, dynamic>> getReviews(String movieId,
      {int page = 1, String sort = 'hotly'}) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/reviews/$movieId?page=$page&sort=$sort'),
      headers: _headers,
    );

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Get reviews failed');
    }
  }

  Future<Map<String, dynamic>> getRanking(String kind,
      {String? category,
      String? period,
      String? year,
      String? vtype,
      int page = 1,
      int limit = 20}) async {
    final params = <String, String>{
      'page': page.toString(),
      'limit': limit.toString(),
    };
    if (category != null && category.isNotEmpty) {
      params['category'] = category;
    }
    if (period != null && period.isNotEmpty) {
      params['period'] = period;
    }
    if (year != null && year.isNotEmpty) {
      params['year'] = year;
    }
    if (vtype != null && vtype.isNotEmpty) {
      params['vtype'] = vtype;
    }

    final uri = Uri.parse('$baseUrl/api/ranking/$kind')
        .replace(queryParameters: params);
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Get ranking failed');
    }
  }

  /// 搜索 JavDB 社区影单（合集）：/api/lists/search?q=
  Future<List<Map<String, dynamic>>> searchLists(String query,
      {int page = 1}) async {
    final uri =
        Uri.parse('$baseUrl/api/lists/search').replace(queryParameters: {
      'q': query,
      'page': page.toString(),
    });
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      final data = jsonDecode(response.body);
      return (data['lists'] as List?)
              ?.map((e) => e as Map<String, dynamic>)
              .toList() ??
          const <Map<String, dynamic>>[];
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Search lists failed');
    }
  }

  /// 获取一个影单的影片列表：/api/lists/{id}
  /// sort 可选：newest / oldest / highest / most_magnets 等（客户端排序）。
  Future<Map<String, dynamic>> getListMovies(String listId,
      {int page = 1, int limit = 20, String? sort}) async {
    final params = <String, String>{
      'page': page.toString(),
      'limit': limit.toString(),
    };
    if (sort != null && sort.isNotEmpty) {
      params['sort'] = sort;
    }
    final uri = Uri.parse('$baseUrl/api/lists/$listId')
        .replace(queryParameters: params);
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Get list movies failed');
    }
  }

  /// 获取已订阅条目（合集/题材/演员混合，按 kind 字段区分）：/api/subscriptions
  Future<List<Map<String, dynamic>>> getSubscriptions() async {
    final response = await _http.get(Uri.parse('$baseUrl/api/subscriptions'),
        headers: _headers);

    if (response.statusCode == 200) {
      final data = jsonDecode(response.body);
      return (data['subscriptions'] as List?)
              ?.map((e) => e as Map<String, dynamic>)
              .toList() ??
          const <Map<String, dynamic>>[];
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Get subscriptions failed');
    }
  }

  /// 新增订阅：kind 为 collection / genre / actor；重复订阅时刷新元数据。
  Future<void> subscribe(String kind, String id, String name,
      {int moviesCount = 0, String? avatar, String? group}) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/subscriptions'),
      headers: _headers,
      body: jsonEncode({
        'id': id,
        'name': name,
        'kind': kind,
        if (moviesCount > 0) 'movies_count': moviesCount,
        if (avatar != null && avatar.isNotEmpty) 'avatar': avatar,
        if (group != null && group.isNotEmpty) 'group': group,
      }),
    );
    if (response.statusCode != 200) {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Subscribe failed');
    }
  }

  /// 取消订阅：DELETE /api/subscriptions/{id}?kind=
  Future<void> unsubscribe(String kind, String id) async {
    final uri = Uri.parse('$baseUrl/api/subscriptions/$id')
        .replace(queryParameters: {'kind': kind});
    final response = await _http.delete(uri, headers: _headers);
    if (response.statusCode != 200) {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Unsubscribe failed');
    }
  }

  // ---------------------------------------------------------------------------
  // 用户态（标记/清单）：与 JavDB 登录账号双向同步
  // ---------------------------------------------------------------------------

  /// 401 时抛出含 "login required" 的异常，供上层触发静默重登。
  Exception _authOrServer(http.Response response, String fallback) {
    String msg = fallback;
    try {
      msg = (jsonDecode(response.body)['error'] as String?) ?? fallback;
    } catch (_) {}
    if (response.statusCode == 401) {
      return Exception('login required: $msg');
    }
    return Exception(msg);
  }

  /// 我的想看/看过影片（与 JavDB 账号同步）。
  /// status: want_watch | watched
  Future<List<Map<String, dynamic>>> getUserMarks(String status,
      {int page = 1}) async {
    final uri = Uri.parse('$baseUrl/api/user/marks')
        .replace(queryParameters: {'status': status, 'page': page.toString()});
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode == 200) {
      final data = jsonDecode(response.body);
      return (data['movies'] as List?)
              ?.map((e) => e as Map<String, dynamic>)
              .toList() ??
          const <Map<String, dynamic>>[];
    }
    throw _authOrServer(response, 'Get marks failed');
  }

  /// 标记影片为想看/看过（与 JavDB 账号同步）。
  Future<Map<String, dynamic>> setUserMark(
      String movieId, String status) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/user/marks'),
      headers: _headers,
      body: jsonEncode({'movie_id': movieId, 'status': status}),
    );
    if (response.statusCode == 200) {
      return jsonDecode(response.body)['mark'] as Map<String, dynamic>? ?? {};
    }
    throw _authOrServer(response, 'Set mark failed');
  }

  /// 取消标记（幂等）。
  Future<void> clearUserMark(String movieId) async {
    final response = await _http.delete(
      Uri.parse('$baseUrl/api/user/marks')
          .replace(queryParameters: {'movie_id': movieId}),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw _authOrServer(response, 'Clear mark failed');
    }
  }

  /// 我的清单。传 [movieId] 时返回带 has_movie 标志的 simple 变体。
  Future<List<Map<String, dynamic>>> getUserLists({String? movieId}) async {
    final uri = Uri.parse('$baseUrl/api/user/lists').replace(queryParameters: {
      if (movieId != null && movieId.isNotEmpty) 'movie_id': movieId,
    });
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode == 200) {
      final data = jsonDecode(response.body);
      return (data['lists'] as List?)
              ?.map((e) => e as Map<String, dynamic>)
              .toList() ??
          const <Map<String, dynamic>>[];
    }
    throw _authOrServer(response, 'Get lists failed');
  }

  /// 新建清单，返回含 id 的清单对象。
  Future<Map<String, dynamic>> createUserList(String name) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/user/lists'),
      headers: _headers,
      body: jsonEncode({'name': name}),
    );
    if (response.statusCode == 200) {
      return jsonDecode(response.body)['list'] as Map<String, dynamic>? ?? {};
    }
    throw _authOrServer(response, 'Create list failed');
  }

  /// 删除清单。
  Future<void> deleteUserList(String id) async {
    final response = await _http.delete(
      Uri.parse('$baseUrl/api/user/lists/$id'),
      headers: _headers,
    );
    if (response.statusCode != 200) {
      throw _authOrServer(response, 'Delete list failed');
    }
  }

  /// 清单改名。
  Future<void> renameUserList(String id, String name) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/user/lists/$id/rename'),
      headers: _headers,
      body: jsonEncode({'name': name}),
    );
    if (response.statusCode != 200) {
      throw _authOrServer(response, 'Rename list failed');
    }
  }

  /// 从清单移除影片（移动端 API 仅提供移除方向）。
  Future<void> removeMovieFromList(
      String listId, String listName, String movieId) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/user/lists/$listId/remove-movie'),
      headers: _headers,
      body: jsonEncode({'movie_id': movieId, 'list_name': listName}),
    );
    if (response.statusCode != 200) {
      throw _authOrServer(response, 'Remove from list failed');
    }
  }

  /// 退出登录（清除后端会话与本地凭据）。
  Future<void> logout() async {
    try {
      await _http.post(Uri.parse('$baseUrl/api/logout'), headers: _headers);
    } catch (_) {
      // 本地清理不受影响，忽略网络错误。
    }
  }

  Future<Map<String, dynamic>> getMagnets(String movieId) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/magnets/$movieId'),
      headers: _headers,
    );

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Get magnets failed');
    }
  }

  Future<Map<String, dynamic>> getTags({String? category}) async {
    final params = <String, String>{};
    if (category != null && category.isNotEmpty) {
      params['category'] = category;
    }

    final uri = Uri.parse('$baseUrl/api/tags').replace(queryParameters: params);
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Get tags failed');
    }
  }

  /// 题材（tag）影片浏览：/api/genre?group={web_group_id}&tag={tag_id}。
  /// 走 JavDB app 端 /v1/movies/tags（仅需 app 登录，无需网页版 Cookie），
  /// group 仅作兼容参数，app 端 tag id 全局唯一。
  Future<Map<String, dynamic>> getGenreMovies(String group, String tag,
      {int page = 1, int limit = 20, String? sort}) async {
    final params = <String, String>{
      'group': group,
      'tag': tag,
      'page': page.toString(),
      'limit': limit.toString(),
    };
    if (sort != null && sort.isNotEmpty) {
      params['sort'] = sort;
    }
    final uri =
        Uri.parse('$baseUrl/api/genre').replace(queryParameters: params);
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Get genre movies failed');
    }
  }

  /// 把详情页类型标签解析成题材页坐标（group+tag）：详情标签有 app 端
  /// 全局唯一 tag id，分组号由题材库补齐。题材库找不到时返回 null
  /// （调用方降级关键词搜索），其余错误上抛由调用方兜底。
  Future<Map<String, dynamic>?> resolveTag(String name, {String? id}) async {
    final params = <String, String>{'name': name};
    if (id != null && id.isNotEmpty) params['id'] = id;
    final uri =
        Uri.parse('$baseUrl/api/tags/resolve').replace(queryParameters: params);
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body) as Map<String, dynamic>;
    }
    if (response.statusCode == 404) return null;
    final error = jsonDecode(response.body);
    throw Exception(error['error'] ?? 'Resolve tag failed');
  }

  /// 导入网页版登录 Cookie（解锁题材浏览等登录墙页面）。
  Future<void> setWebCookie(String cookie) async {
    final response = await _http.post(
      Uri.parse('$baseUrl/api/web-cookie'),
      headers: _headers,
      body: jsonEncode({'cookie': cookie}),
    );
    if (response.statusCode != 200) {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Set web cookie failed');
    }
  }

  Future<Map<String, dynamic>> getActor(String id) async {
    final response = await _http.get(
      Uri.parse('$baseUrl/api/actor/$id'),
      headers: _headers,
    );

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Get actor failed');
    }
  }

  /// 演员的作品列表（演员专题页），支持分页和排序。
  /// sort 可选：newest / oldest / highest / most_magnets。
  Future<Map<String, dynamic>> actorMovies(String actorId,
      {int page = 1, int limit = 20, String? sort, String? mode}) async {
    final params = <String, String>{
      'page': page.toString(),
      'limit': limit.toString(),
    };
    if (sort != null && sort.isNotEmpty) {
      params['sort'] = sort;
    }
    // mode: 空=全部 / 'solo'=单体作品（服务端过滤）/ 'costar'=共演作品（后端差集）
    if (mode != null && mode.isNotEmpty) {
      params['mode'] = mode;
    }
    final uri = Uri.parse('$baseUrl/api/actor-movies/$actorId')
        .replace(queryParameters: params);
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Get actor movies failed');
    }
  }

  // AV endpoints (MissAV/Jable/HohoJ)

  static List<String>? _cachedSources; // 片源清单是静态数据，进程内缓存避免每次进详情页重复请求

  Future<List<String>> avSources() async {
    final cached = _cachedSources;
    if (cached != null) return cached;
    final uri = Uri.parse('$baseUrl/api/av/sources');
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      final data = jsonDecode(response.body);
      final sources = (data['sources'] as List).cast<String>();
      _cachedSources = sources;
      return sources;
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Failed to get AV sources');
    }
  }

  Future<Map<String, dynamic>> avSearch(String query,
      {int page = 1, int limit = 20, String? source}) async {
    final params = <String, String>{
      'q': query,
      'page': page.toString(),
      'limit': limit.toString(),
    };
    if (source != null && source.isNotEmpty) {
      params['source'] = source;
    }

    final uri =
        Uri.parse('$baseUrl/api/av/search').replace(queryParameters: params);
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'AV search failed');
    }
  }

  Future<Map<String, dynamic>> avDetail(String code, {String? source}) async {
    final params = <String, String>{};
    if (source != null && source.isNotEmpty) {
      params['source'] = source;
    }

    final uri = Uri.parse('$baseUrl/api/av/detail/$code')
        .replace(queryParameters: params);
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'AV detail failed');
    }
  }

  Future<Map<String, dynamic>> avResolve(String code,
      {String? source, String? variant}) async {
    final params = <String, String>{};
    if (source != null && source.isNotEmpty) {
      params['source'] = source;
    }
    if (variant != null && variant.isNotEmpty) {
      params['variant'] = variant;
    }

    final uri = Uri.parse('$baseUrl/api/av/resolve/$code')
        .replace(queryParameters: params);
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'AV resolve failed');
    }
  }

  /// 轻量探测番号可用变体（仅抓取 HTML，不拉取播放列表）。
  /// 用于详情页快速展示变体按钮，用户点击后才按需调用 avResolve。
  Future<Map<String, dynamic>> avProbe(String code, {String? source}) async {
    final params = <String, String>{};
    if (source != null && source.isNotEmpty) {
      params['source'] = source;
    }

    final uri = Uri.parse('$baseUrl/api/av/probe/$code')
        .replace(queryParameters: params);
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'AV probe failed');
    }
  }

  /// 注入 Cloudflare cf_clearance Cookie，后续请求自动携带。
  Future<void> avSetCFCookie(String host, String cookie, {String? ua}) async {
    final uri = Uri.parse('$baseUrl/api/av/cf-cookie');
    final response = await _http.post(uri,
        headers: _headers,
        body: jsonEncode({
          'host': host,
          'cookie': cookie,
          if (ua != null) 'ua': ua,
        }));

    if (response.statusCode != 200) {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'CF cookie injection failed');
    }
  }

  Future<Map<String, dynamic>> avPlay(String code, {String? source}) async {
    final params = <String, String>{};
    if (source != null && source.isNotEmpty) {
      params['source'] = source;
    }

    final uri = Uri.parse('$baseUrl/api/av/play/$code')
        .replace(queryParameters: params);
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'AV play failed');
    }
  }

  Future<Map<String, dynamic>> avDownload(String code,
      {String? source, String? downloadDir}) async {
    final params = <String, String>{};
    if (source != null && source.isNotEmpty) {
      params['source'] = source;
    }
    if (downloadDir != null && downloadDir.isNotEmpty) {
      params['download_dir'] = downloadDir;
    }

    final uri = Uri.parse('$baseUrl/api/av/download/$code')
        .replace(queryParameters: params);
    final response = await _http.post(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'AV download failed');
    }
  }

  // ---- 下载队列（异步任务，后端 /api/downloads） ----

  /// 列出全部下载任务（最新在前）。
  Future<List<DownloadTask>> listDownloads() async {
    final uri = Uri.parse('$baseUrl/api/downloads');
    final response = await _http.get(uri, headers: _headers);
    if (response.statusCode == 200) {
      final data = jsonDecode(response.body);
      return (data['tasks'] as List)
          .map((t) => DownloadTask.fromJson(t as Map<String, dynamic>))
          .toList();
    }
    final error = jsonDecode(response.body);
    throw Exception(error['error'] ?? 'List downloads failed');
  }

  /// 创建下载任务（加入队列）；dir 空则由后端用默认下载目录。
  Future<DownloadTask> createDownload({
    required String code,
    String title = '',
    String cover = '',
    String source = '',
    String variant = '',
    int qualityHeight = 0,
    String dir = '',
  }) async {
    final uri = Uri.parse('$baseUrl/api/downloads');
    final response = await _http.post(uri,
        headers: _headers,
        body: jsonEncode({
          'code': code,
          'title': title,
          'cover': cover,
          'source': source,
          'variant': variant,
          'quality_height': qualityHeight,
          'dir': dir,
        }));
    if (response.statusCode == 200) {
      final data = jsonDecode(response.body);
      return DownloadTask.fromJson(data['task'] as Map<String, dynamic>);
    }
    final error = jsonDecode(response.body);
    throw Exception(error['error'] ?? 'Create download failed');
  }

  /// 取消任务（排队中/下载中）。
  Future<void> cancelDownload(String id) async {
    final uri = Uri.parse('$baseUrl/api/downloads/$id/cancel');
    final response = await _http.post(uri, headers: _headers);
    if (response.statusCode != 200) {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Cancel download failed');
    }
  }

  /// 重试失败/已取消的任务。
  Future<void> retryDownload(String id) async {
    final uri = Uri.parse('$baseUrl/api/downloads/$id/retry');
    final response = await _http.post(uri, headers: _headers);
    if (response.statusCode != 200) {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Retry download failed');
    }
  }

  /// 删除任务记录（deleteFile 为 true 时同时删除已下载文件）。
  Future<void> deleteDownload(String id, {bool deleteFile = false}) async {
    final uri = Uri.parse('$baseUrl/api/downloads/$id')
        .replace(queryParameters: {'delete_file': deleteFile ? '1' : '0'});
    final response = await _http.delete(uri, headers: _headers);
    if (response.statusCode != 200) {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Delete download failed');
    }
  }

  /// 同步下载队列配置（并发上限 / 限速，speedLimitMbps=0 表示不限速）。
  Future<void> setDownloadConfig({
    required int maxConcurrent,
    required int speedLimitMbps,
  }) async {
    final uri = Uri.parse('$baseUrl/api/downloads/config');
    final response = await _http.post(uri,
        headers: _headers,
        body: jsonEncode({
          'max_concurrent': maxConcurrent,
          'speed_limit_mbps': speedLimitMbps,
        }));
    if (response.statusCode != 200) {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Set download config failed');
    }
  }

  /// 字幕：搜索某番号在 subtitlecat / avsubtitles 上的可用条目列表。
  Future<Map<String, dynamic>> searchSubtitles(String code) async {
    final uri = Uri.parse('$baseUrl/api/subtitles/search')
        .replace(queryParameters: {'code': code});
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Subtitle search failed');
    }
  }

  /// 字幕：自动挑选最佳语言（中文优先）并返回 SRT 文本。
  /// 来源信息在响应头（package:http 已归一为小写键）。
  Future<Map<String, String?>> autoSubtitle(String code) async {
    final uri = Uri.parse('$baseUrl/api/subtitles/auto')
        .replace(queryParameters: {'code': code});
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return {
        'srt': response.body,
        'source': response.headers['x-sub-source'],
        'lang': response.headers['x-sub-lang'],
        'name': response.headers['x-sub-name'],
      };
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Subtitle load failed');
    }
  }

  /// 字幕：下载指定条目，返回 SRT 文本与来源头。
  Future<Map<String, String?>> downloadSubtitle({
    required String code,
    required String source,
    required String ref,
    String? lang,
  }) async {
    final uri = Uri.parse('$baseUrl/api/subtitles/download').replace(
      queryParameters: {
        'code': code,
        'source': source,
        'ref': ref,
        if (lang != null && lang.isNotEmpty) 'lang': lang,
      },
    );
    final response = await _http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return {
        'srt': response.body,
        'source': response.headers['x-sub-source'],
        'lang': response.headers['x-sub-lang'],
        'name': response.headers['x-sub-name'],
      };
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Subtitle download failed');
    }
  }
}
