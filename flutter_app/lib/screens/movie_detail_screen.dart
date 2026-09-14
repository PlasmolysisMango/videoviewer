import 'package:flutter/material.dart';
import '../api/client.dart';
import '../api/models.dart';
import '../services/backend_launcher.dart';
import '../services/logger.dart';
import 'video_player_screen.dart';

class MovieDetailScreen extends StatefulWidget {
  final String movieId;
  final String movieNumber;

  const MovieDetailScreen({
    super.key,
    required this.movieId,
    required this.movieNumber,
  });

  @override
  State<MovieDetailScreen> createState() => _MovieDetailScreenState();
}

class _MovieDetailScreenState extends State<MovieDetailScreen> {
  late final JavDBClient _client;
  Map<String, dynamic>? _movieData;
  List<Magnet> _magnets = [];
  Map<String, dynamic>? _avData;
  Map<String, dynamic>? _streamData;
  bool _isLoading = true;
  bool _isLoadingAv = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _client = JavDBClient(BackendLauncher.baseUrl);
    _loadMovie();
    _loadAvData();
  }

  Future<void> _loadMovie() async {
    try {
      AppLogger.info('Loading movie: ${widget.movieId}');
      final result = await _client.getMovie(widget.movieId);
      final magnetsList = <Magnet>[];
      
      // Handle both single magnet (Map) and list of magnets
      final magnetsData = result['magnets'];
      if (magnetsData != null) {
        if (magnetsData is List) {
          magnetsList.addAll(magnetsData
              .map((m) => Magnet.fromJson(m as Map<String, dynamic>)));
        } else if (magnetsData is Map) {
          magnetsList.add(Magnet.fromJson(magnetsData as Map<String, dynamic>));
        }
      }
      
      setState(() {
        _movieData = result['movie'] as Map<String, dynamic>?;
        _magnets = magnetsList;
        _isLoading = false;
      });
      AppLogger.info('Movie loaded: ${_movieData?['number']}, magnets: ${_magnets.length}');
    } catch (e) {
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
      AppLogger.error('Failed to load movie', e);
    }
  }

  Future<void> _loadAvData() async {
    try {
      AppLogger.info('Loading AV data for: ${widget.movieNumber}');
      final result = await _client.avDetail(widget.movieNumber);
      setState(() {
        _avData = result['video'] as Map<String, dynamic>?;
        _isLoadingAv = false;
      });
      AppLogger.info('AV data loaded, m3u8: ${_avData?['m3u8']}');
      
      // Also try to get stream with referer
      _loadStream();
    } catch (e) {
      AppLogger.warning('AV data not available: $e');
    }
  }

  Future<void> _loadStream() async {
    try {
      final result = await _client.avPlay(widget.movieNumber);
      setState(() {
        _streamData = result['stream'] as Map<String, dynamic>?;
      });
      AppLogger.info('Stream loaded: ${_streamData?['url']}, referer: ${_streamData?['referer']}');
    } catch (e) {
      AppLogger.warning('Stream not available: $e');
    }
  }

  void _playVideo() {
    // Prefer stream data (has referer), fallback to avData m3u8
    final streamUrl = _streamData?['url'] as String?;
    final streamReferer = _streamData?['referer'] as String?;
    final m3u8Url = _avData?['m3u8'] as String?;
    
    final videoUrl = streamUrl ?? m3u8Url;
    if (videoUrl == null || videoUrl.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('视频源不可用')),
      );
      return;
    }
    
    AppLogger.info('Playing: $videoUrl, referer: $streamReferer');
    Navigator.push(
      context,
      MaterialPageRoute(
        builder: (context) => VideoPlayerScreen(
          videoUrl: videoUrl,
          title: _movieData?['title'] as String? ?? 'Video',
          referer: streamReferer,
        ),
      ),
    );
  }

  void _downloadVideo() {
    if (_avData == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('下载源不可用')),
      );
      return;
    }
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('下载功能开发中...')),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('电影详情'),
      ),
      body: _isLoading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? Center(child: Text('错误: $_error'))
              : _movieData == null
                  ? const Center(child: Text('未找到电影'))
                  : _buildContent(),
    );
  }

  Widget _buildContent() {
    final movie = _movieData!;
    return SingleChildScrollView(
      padding: const EdgeInsets.all(16.0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Cover image
          if (movie['cover_url'] != null)
            Center(
              child: Image.network(
                movie['cover_url'] as String,
                height: 300,
                fit: BoxFit.contain,
                errorBuilder: (_, __, ___) =>
                    const Icon(Icons.movie, size: 100),
              ),
            ),
          const SizedBox(height: 16),
          // Title
          Text(
            movie['number'] as String? ?? '',
            style: const TextStyle(fontSize: 24, fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 8),
          Text(
            movie['title'] as String? ?? '',
            style: const TextStyle(fontSize: 16),
          ),
          const SizedBox(height: 16),
          // Info
          _buildInfoRow('发行日期', movie['release_date'] as String?),
          _buildInfoRow('时长',
              movie['duration'] != null ? '${movie['duration']} 分钟' : null),
          _buildInfoRow(
              '评分', movie['score'] != null ? '${movie['score']}' : null),
          const SizedBox(height: 24),
          // Play/Download buttons
          if (_avData != null) ...[
            Row(
              children: [
                Expanded(
                  child: ElevatedButton.icon(
                    onPressed: _playVideo,
                    icon: const Icon(Icons.play_arrow),
                    label: const Text('播放'),
                    style: ElevatedButton.styleFrom(
                      padding: const EdgeInsets.symmetric(vertical: 16),
                    ),
                  ),
                ),
                const SizedBox(width: 16),
                Expanded(
                  child: OutlinedButton.icon(
                    onPressed: _downloadVideo,
                    icon: const Icon(Icons.download),
                    label: const Text('下载'),
                    style: OutlinedButton.styleFrom(
                      padding: const EdgeInsets.symmetric(vertical: 16),
                    ),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 24),
          ],
          // Magnets
          const Text(
            '磁链',
            style: TextStyle(fontSize: 20, fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 8),
          if (_magnets.isEmpty)
            const Text('暂无磁链')
          else
            ..._magnets.map((magnet) => Card(
                  margin: const EdgeInsets.only(bottom: 8),
                  child: ListTile(
                    title: Text(
                      magnet.name,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                    subtitle: Row(
                      children: [
                        if (magnet.size != null) Text('大小: ${magnet.size}  '),
                        if (magnet.hasCnsub == true)
                          const Text('中字  ',
                              style: TextStyle(color: Colors.green)),
                        if (magnet.isHd == true)
                          const Text('HD  ',
                              style: TextStyle(color: Colors.blue)),
                      ],
                    ),
                    trailing: IconButton(
                      icon: const Icon(Icons.download),
                      onPressed: () {
                        // TODO: Implement download
                        ScaffoldMessenger.of(context).showSnackBar(
                          const SnackBar(content: Text('下载功能待实现')),
                        );
                      },
                    ),
                  ),
                )),
        ],
      ),
    );
  }

  Widget _buildInfoRow(String label, String? value) {
    if (value == null) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 80,
            child: Text(
              label,
              style: const TextStyle(color: Colors.grey),
            ),
          ),
          Expanded(child: Text(value)),
        ],
      ),
    );
  }
}
