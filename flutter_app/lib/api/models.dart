class Movie {
  final String id;
  final String number;
  final String title;
  final String? originTitle;
  final String? coverUrl;
  final String? thumbUrl;

  /// 竖版海报（2:3，来自 /thumbs/ 目录）。app API 的 cover/thumb 是横版剧照，
  /// 用竖版容器展示会裁剪成中间竖条；需要竖版展示时优先用 posterUrl。
  final String? posterUrl;
  final String? releaseDate;
  final int? duration;
  final int? magnetsCount;
  final bool? hasCnsub;
  final bool? canPlay;
  final double? score;
  final int? ranking;
  final String? href;
  final List<String>? previewImages;

  /// 演员名列表（app API 搜索/详情结果带，网页源列表为空）。
  final List<String>? actors;

  Movie({
    required this.id,
    required this.number,
    required this.title,
    this.originTitle,
    this.coverUrl,
    this.thumbUrl,
    this.posterUrl,
    this.releaseDate,
    this.duration,
    this.magnetsCount,
    this.hasCnsub,
    this.canPlay,
    this.score,
    this.ranking,
    this.href,
    this.previewImages,
    this.actors,
  });

  factory Movie.fromJson(Map<String, dynamic> json) {
    return Movie(
      id: json['id'] as String,
      number: json['number'] as String,
      title: json['title'] as String,
      originTitle: json['origin_title'] as String?,
      coverUrl: json['cover_url'] as String?,
      thumbUrl: json['thumb_url'] as String?,
      posterUrl: json['poster_url'] as String?,
      releaseDate: json['release_date'] as String?,
      duration: json['duration'] as int?,
      magnetsCount: json['magnets_count'] as int?,
      hasCnsub: json['has_cnsub'] as bool?,
      canPlay: json['can_play'] as bool?,
      score: (json['score'] as num?)?.toDouble(),
      ranking: json['ranking'] as int?,
      href: json['href'] as String?,
      previewImages: (json['preview_images'] as List<dynamic>?)?.cast<String>(),
      actors: (json['actors'] as List<dynamic>?)?.cast<String>(),
    );
  }

  /// 复制并替换部分字段（渐进补演员时用）。
  Movie copyWith({List<String>? actors}) {
    return Movie(
      id: id,
      number: number,
      title: title,
      originTitle: originTitle,
      coverUrl: coverUrl,
      thumbUrl: thumbUrl,
      posterUrl: posterUrl,
      releaseDate: releaseDate,
      duration: duration,
      magnetsCount: magnetsCount,
      hasCnsub: hasCnsub,
      canPlay: canPlay,
      score: score,
      ranking: ranking,
      href: href,
      previewImages: previewImages,
      actors: actors ?? this.actors,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'id': id,
      'number': number,
      'title': title,
      'origin_title': originTitle,
      'cover_url': coverUrl,
      'thumb_url': thumbUrl,
      'poster_url': posterUrl,
      'release_date': releaseDate,
      'duration': duration,
      'magnets_count': magnetsCount,
      'has_cnsub': hasCnsub,
      'can_play': canPlay,
      'score': score,
      'ranking': ranking,
      'href': href,
      'preview_images': previewImages,
      'actors': actors,
    };
  }
}

class Magnet {
  final String name;
  final String? hash;
  final String? sizeText;
  final int? sizeBytes;
  final String? createdAt;
  final int? filesCount;
  final bool? cnsub;
  final bool? hd;

  Magnet({
    required this.name,
    this.hash,
    this.sizeText,
    this.sizeBytes,
    this.createdAt,
    this.filesCount,
    this.cnsub,
    this.hd,
  });

  /// 标准磁力链接，无 hash 时为 null。
  String? get magnetUrl =>
      (hash == null || hash!.isEmpty) ? null : 'magnet:?xt=urn:btih:$hash';

  factory Magnet.fromJson(Map<String, dynamic> json) {
    return Magnet(
      name: json['name'] as String,
      hash: json['hash'] as String?,
      sizeText: json['size_text'] as String?,
      sizeBytes: json['size_bytes'] as int?,
      createdAt: json['created_at'] as String?,
      filesCount: json['files_count'] as int?,
      cnsub: json['cnsub'] as bool?,
      hd: json['hd'] as bool?,
    );
  }
}

class Actor {
  final String id;
  final String name;
  final String? avatarUrl;
  final int? videosCount;
  final int? ranking;
  final String? birthday;
  final int? age;
  final String? height;
  final String? bust;
  final String? waist;
  final String? hips;
  final String? birthplace;
  final String? hobby;
  final List<String>? aliases;

  Actor({
    required this.id,
    required this.name,
    this.avatarUrl,
    this.videosCount,
    this.ranking,
    this.birthday,
    this.age,
    this.height,
    this.bust,
    this.waist,
    this.hips,
    this.birthplace,
    this.hobby,
    this.aliases,
  });

  factory Actor.fromJson(Map<String, dynamic> json) {
    return Actor(
      id: json['id'] as String,
      name: json['name'] as String,
      avatarUrl: json['avatar_url'] as String?,
      videosCount: json['videos_count'] as int?,
      ranking: json['ranking'] as int?,
      birthday: json['birthday'] as String?,
      age: json['age'] as int?,
      height: json['height'] as String?,
      bust: json['bust'] as String?,
      waist: json['waist'] as String?,
      hips: json['hips'] as String?,
      birthplace: json['birthplace'] as String?,
      hobby: json['hobby'] as String?,
      aliases: (json['aliases'] as List<dynamic>?)?.cast<String>(),
    );
  }
}

class VideoStream {
  final String url;
  final String? referer;
  final String? resolution;
  final int? qualityHeight;
  final int? bandwidth;
  final String? source;
  final bool? uncensored; // 来自无码变体页
  final bool? cnsub; // 来自中文字幕变体页

  VideoStream({
    required this.url,
    this.referer,
    this.resolution,
    this.qualityHeight,
    this.bandwidth,
    this.source,
    this.uncensored,
    this.cnsub,
  });

  factory VideoStream.fromJson(Map<String, dynamic> json) {
    return VideoStream(
      url: json['url'] as String,
      referer: json['referer'] as String?,
      resolution: json['resolution'] as String?,
      qualityHeight: json['quality_height'] as int?,
      bandwidth: json['bandwidth'] as int?,
      source: json['source'] as String?,
      uncensored: json['uncensored'] as bool?,
      cnsub: json['cnsub'] as bool?,
    );
  }

  /// 播放器内清晰度菜单展示名（仅清晰度；无码/中字变体在详情页片源下拉中选择）。
  String get label {
    if (qualityHeight != null && qualityHeight! > 0) {
      return '${qualityHeight}P';
    }
    if (resolution != null && resolution != 'media') {
      return resolution!;
    }
    return '默认';
  }

  /// 片源优先级：无码 > 中字 > 普通。
  int get _variantRank => uncensored == true ? 0 : (cnsub == true ? 1 : 2);

  /// 排序：无码优先、次选中字，同变体内按清晰度降序。
  /// 排序后首项即为默认选中的片源。
  static List<VideoStream> sortStreams(List<VideoStream> input) {
    final list = List<VideoStream>.from(input);
    list.sort((a, b) {
      final r = a._variantRank.compareTo(b._variantRank);
      if (r != 0) return r;
      return (b.qualityHeight ?? 0).compareTo(a.qualityHeight ?? 0);
    });
    return list;
  }
}

class TagGroup {
  final String name;
  final List<Tag> tags;

  TagGroup({required this.name, required this.tags});

  factory TagGroup.fromJson(Map<String, dynamic> json) {
    return TagGroup(
      name: json['name'] as String,
      tags: (json['tags'] as List<dynamic>)
          .map((t) => Tag.fromJson(t as Map<String, dynamic>))
          .toList(),
    );
  }
}

class Tag {
  final String name;
  final String? value;

  Tag({required this.name, this.value});

  factory Tag.fromJson(Map<String, dynamic> json) {
    return Tag(
      name: json['name'] as String,
      value: json['value'] as String?,
    );
  }
}

/// 下载任务（后端 /api/downloads 的任务模型，异步队列）。
/// status 取值：queued（排队）/ running（下载中）/ done / failed / canceled。
class DownloadTask {
  final String id;
  final String code;
  final String title;
  final String cover;
  final String source;
  final String variant; // uncensored / cnsub / normal / ''
  final int qualityHeight; // 0 = 未知/默认
  final String dir;
  final String status;
  final int doneSegments;
  final int totalSegments;
  final int size;
  final String filePath;
  final String error;
  final DateTime createdAt;

  DownloadTask({
    required this.id,
    required this.code,
    this.title = '',
    this.cover = '',
    this.source = '',
    this.variant = '',
    this.qualityHeight = 0,
    this.dir = '',
    this.status = 'queued',
    this.doneSegments = 0,
    this.totalSegments = 0,
    this.size = 0,
    this.filePath = '',
    this.error = '',
    DateTime? createdAt,
  }) : createdAt = createdAt ?? DateTime.now();

  factory DownloadTask.fromJson(Map<String, dynamic> json) {
    return DownloadTask(
      id: json['id'] as String,
      code: json['code'] as String? ?? '',
      title: json['title'] as String? ?? '',
      cover: json['cover'] as String? ?? '',
      source: json['source'] as String? ?? '',
      variant: json['variant'] as String? ?? '',
      qualityHeight: json['quality_height'] as int? ?? 0,
      dir: json['dir'] as String? ?? '',
      status: json['status'] as String? ?? 'queued',
      doneSegments: json['done_segments'] as int? ?? 0,
      totalSegments: json['total_segments'] as int? ?? 0,
      size: json['size'] as int? ?? 0,
      filePath: json['file_path'] as String? ?? '',
      error: json['error'] as String? ?? '',
      createdAt: DateTime.tryParse(json['created_at'] as String? ?? ''),
    );
  }

  /// 下载进度 0~1；总分片数未知时按状态退化（已完成=1，其余=0）。
  double get progress {
    if (totalSegments > 0) return (doneSegments / totalSegments).clamp(0, 1);
    return status == 'done' ? 1 : 0;
  }

  bool get isActive => status == 'queued' || status == 'running';
  bool get isDone => status == 'done';

  String get statusLabel {
    switch (status) {
      case 'queued':
        return '排队中';
      case 'running':
        return totalSegments > 0 ? '下载中 $doneSegments/$totalSegments' : '下载中';
      case 'done':
        return '已完成';
      case 'failed':
        return '失败';
      case 'canceled':
        return '已取消';
      default:
        return status;
    }
  }

  /// 清晰度标签（如 1080P），未知时不显示。
  String get qualityLabel => qualityHeight > 0 ? '${qualityHeight}P' : '';

  /// 变体标签（无码 / 中字 / 普通），普通或未知时为空。
  String get variantLabel {
    switch (variant) {
      case 'uncensored':
        return '无码';
      case 'cnsub':
        return '中字';
      case 'normal':
        return '普通';
      default:
        return '';
    }
  }
}
