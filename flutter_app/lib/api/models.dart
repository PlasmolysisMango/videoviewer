class Movie {
  final String id;
  final String number;
  final String title;
  final String? originTitle;
  final String? coverUrl;
  final String? thumbUrl;
  final String? releaseDate;
  final int? duration;
  final int? magnetsCount;
  final bool? hasCnsub;
  final bool? canPlay;
  final double? score;
  final int? ranking;
  final String? href;

  Movie({
    required this.id,
    required this.number,
    required this.title,
    this.originTitle,
    this.coverUrl,
    this.thumbUrl,
    this.releaseDate,
    this.duration,
    this.magnetsCount,
    this.hasCnsub,
    this.canPlay,
    this.score,
    this.ranking,
    this.href,
  });

  factory Movie.fromJson(Map<String, dynamic> json) {
    return Movie(
      id: json['id'] as String,
      number: json['number'] as String,
      title: json['title'] as String,
      originTitle: json['origin_title'] as String?,
      coverUrl: json['cover_url'] as String?,
      thumbUrl: json['thumb_url'] as String?,
      releaseDate: json['release_date'] as String?,
      duration: json['duration'] as int?,
      magnetsCount: json['magnets_count'] as int?,
      hasCnsub: json['has_cnsub'] as bool?,
      canPlay: json['can_play'] as bool?,
      score: (json['score'] as num?)?.toDouble(),
      ranking: json['ranking'] as int?,
      href: json['href'] as String?,
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
      'release_date': releaseDate,
      'duration': duration,
      'magnets_count': magnetsCount,
      'has_cnsub': hasCnsub,
      'can_play': canPlay,
      'score': score,
      'ranking': ranking,
      'href': href,
    };
  }
}

class Magnet {
  final String name;
  final String? size;
  final int? sizeBytes;
  final String? date;
  final int? files;
  final bool? hasCnsub;
  final bool? isHd;
  final String? link;

  Magnet({
    required this.name,
    this.size,
    this.sizeBytes,
    this.date,
    this.files,
    this.hasCnsub,
    this.isHd,
    this.link,
  });

  factory Magnet.fromJson(Map<String, dynamic> json) {
    return Magnet(
      name: json['name'] as String,
      size: json['size'] as String?,
      sizeBytes: json['size_bytes'] as int?,
      date: json['date'] as String?,
      files: json['files'] as int?,
      hasCnsub: json['has_cnsub'] as bool?,
      isHd: json['is_hd'] as bool?,
      link: json['link'] as String?,
    );
  }
}

class Actor {
  final String id;
  final String name;
  final String? avatarUrl;
  final int? videosCount;
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
