import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../api/client.dart';
import '../api/models.dart';
import '../services/backend_launcher.dart';
import '../services/logger.dart';
import 'movie_detail_screen.dart';

class RankingScreen extends StatefulWidget {
  const RankingScreen({super.key});

  @override
  State<RankingScreen> createState() => _RankingScreenState();
}

class _RankingScreenState extends State<RankingScreen> {
  late final JavDBClient _client;
  List<Movie> _movies = [];
  bool _isLoading = true;
  String? _error;
  String _selectedKind = 'uncensored';
  final List<String> _kinds = ['uncensored', 'censored', 'western'];

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    _loadRanking();
  }

  Future<void> _loadRanking() async {
    setState(() {
      _isLoading = true;
      _error = null;
    });

    try {
      AppLogger.info('Loading ranking: $_selectedKind');
      final result = await _client.getRanking(_selectedKind);
      final moviesList = (result['movies'] as List)
          .map((m) => Movie.fromJson(m as Map<String, dynamic>))
          .toList();
      setState(() {
        _movies = moviesList;
        _isLoading = false;
      });
      AppLogger.info('Loaded ${moviesList.length} ranking items');
    } catch (e) {
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
      AppLogger.error('Failed to load ranking', e);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('榜单'),
        actions: [
          PopupMenuButton<String>(
            icon: const Icon(Icons.filter_list),
            onSelected: (kind) {
              setState(() => _selectedKind = kind);
              _loadRanking();
            },
            itemBuilder: (context) => _kinds.map((kind) {
              return PopupMenuItem(
                value: kind,
                child: Row(
                  children: [
                    if (kind == _selectedKind)
                      const Icon(Icons.check, size: 18)
                    else
                      const SizedBox(width: 18),
                    const SizedBox(width: 8),
                    Text(kind == 'uncensored' ? '无码' : kind == 'censored' ? '有码' : '欧美'),
                  ],
                ),
              );
            }).toList(),
          ),
          IconButton(
            icon: const Icon(Icons.refresh),
            onPressed: _isLoading ? null : _loadRanking,
          ),
        ],
      ),
      body: _isLoading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? Center(
                  child: Column(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      const Icon(Icons.error_outline, size: 48, color: Colors.red),
                      const SizedBox(height: 16),
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 32),
                        child: Text(
                          '错误: $_error',
                          textAlign: TextAlign.center,
                          style: const TextStyle(color: Colors.red),
                        ),
                      ),
                      const SizedBox(height: 16),
                      ElevatedButton(
                        onPressed: _loadRanking,
                        child: const Text('重试'),
                      ),
                    ],
                  ),
                )
              : _movies.isEmpty
                  ? const Center(child: Text('暂无数据'))
                  : ListView.builder(
                      itemCount: _movies.length,
                      itemBuilder: (context, index) {
                        final movie = _movies[index];
                        return ListTile(
                          leading: movie.thumbUrl != null
                              ? Image.network(
                                  movie.thumbUrl!,
                                  width: 60,
                                  fit: BoxFit.cover,
                                  errorBuilder: (_, __, ___) =>
                                      const Icon(Icons.movie, size: 60),
                                )
                              : const Icon(Icons.movie, size: 60),
                          title: Text(
                            movie.number,
                            style: const TextStyle(fontWeight: FontWeight.bold),
                          ),
                          subtitle: Text(
                            movie.title,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                          ),
                          trailing: Row(
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              if (movie.ranking != null)
                                Container(
                                  padding: const EdgeInsets.symmetric(
                                      horizontal: 8, vertical: 4),
                                  decoration: BoxDecoration(
                                    color: movie.ranking! <= 3
                                        ? Colors.orange
                                        : Colors.grey[300],
                                    borderRadius: BorderRadius.circular(12),
                                  ),
                                  child: Text(
                                    '#${movie.ranking}',
                                    style: TextStyle(
                                      color: movie.ranking! <= 3
                                          ? Colors.white
                                          : Colors.black,
                                      fontWeight: FontWeight.bold,
                                      fontSize: 12,
                                    ),
                                  ),
                                ),
                              if (movie.score != null)
                                Padding(
                                  padding: const EdgeInsets.only(left: 8),
                                  child: Text(
                                    '${movie.score}',
                                    style: const TextStyle(
                                      color: Colors.orange,
                                      fontWeight: FontWeight.bold,
                                    ),
                                  ),
                                ),
                            ],
                          ),
                          onTap: () {
                            Navigator.push(
                              context,
                              MaterialPageRoute(
                                builder: (context) =>
                                    MovieDetailScreen(movieId: movie.id),
                              ),
                            );
                          },
                        );
                      },
                    ),
    );
  }
}
