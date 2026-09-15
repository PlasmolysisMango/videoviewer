import 'dart:convert';
import 'package:http/http.dart' as http;

class JavDBClient {
  final String baseUrl;
  String? _token;

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
    final response = await http.post(
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
      {int page = 1, int limit = 20, String scope = 'movie', String? sort}) async {
    final params = <String, String>{
      'q': query,
      'page': page.toString(),
      'limit': limit.toString(),
      'scope': scope,
    };
    if (sort != null && sort.isNotEmpty) {
      params['sort'] = sort;
    }
    final uri = Uri.parse('$baseUrl/api/search').replace(queryParameters: params);
    final response = await http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Search failed');
    }
  }

  Future<Map<String, dynamic>> getMovie(String id) async {
    final response = await http.get(
      Uri.parse('$baseUrl/api/movie/$id'),
      headers: _headers,
    );

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Get movie failed');
    }
  }

  Future<Map<String, dynamic>> getRanking(String kind,
      {String? category, String? period, int page = 1, int limit = 20}) async {
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

    final uri = Uri.parse('$baseUrl/api/ranking/$kind')
        .replace(queryParameters: params);
    final response = await http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Get ranking failed');
    }
  }

  Future<Map<String, dynamic>> getMagnets(String movieId) async {
    final response = await http.get(
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
    final response = await http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Get tags failed');
    }
  }

  Future<Map<String, dynamic>> getActor(String id) async {
    final response = await http.get(
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
      {int page = 1, int limit = 20, String? sort}) async {
    final params = <String, String>{
      'page': page.toString(),
      'limit': limit.toString(),
    };
    if (sort != null && sort.isNotEmpty) {
      params['sort'] = sort;
    }
    final uri = Uri.parse('$baseUrl/api/actor-movies/$actorId')
        .replace(queryParameters: params);
    final response = await http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'Get actor movies failed');
    }
  }

  // AV endpoints (MissAV/Jable/HohoJ)

  Future<List<String>> avSources() async {
    final uri = Uri.parse('$baseUrl/api/av/sources');
    final response = await http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      final data = jsonDecode(response.body);
      return (data['sources'] as List).cast<String>();
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

    final uri = Uri.parse('$baseUrl/api/av/search')
        .replace(queryParameters: params);
    final response = await http.get(uri, headers: _headers);

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
    final response = await http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'AV detail failed');
    }
  }

  Future<Map<String, dynamic>> avResolve(String code, {String? source}) async {
    final params = <String, String>{};
    if (source != null && source.isNotEmpty) {
      params['source'] = source;
    }

    final uri = Uri.parse('$baseUrl/api/av/resolve/$code')
        .replace(queryParameters: params);
    final response = await http.get(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'AV resolve failed');
    }
  }

  Future<Map<String, dynamic>> avPlay(String code, {String? source}) async {
    final params = <String, String>{};
    if (source != null && source.isNotEmpty) {
      params['source'] = source;
    }

    final uri = Uri.parse('$baseUrl/api/av/play/$code')
        .replace(queryParameters: params);
    final response = await http.get(uri, headers: _headers);

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
    final response = await http.post(uri, headers: _headers);

    if (response.statusCode == 200) {
      return jsonDecode(response.body);
    } else {
      final error = jsonDecode(response.body);
      throw Exception(error['error'] ?? 'AV download failed');
    }
  }
}
