/// AACG 专栏（镜像站）模型：字段对应后端 /api/aacg/* 的 JSON envelope。
class AacgArticle {
  final String title;
  final String url;
  final String summary;
  final String coverUrl;
  final String publishedAt;

  AacgArticle({
    required this.title,
    required this.url,
    this.summary = '',
    this.coverUrl = '',
    this.publishedAt = '',
  });

  factory AacgArticle.fromJson(Map<String, dynamic> json) {
    return AacgArticle(
      title: json['title'] as String? ?? '',
      url: json['url'] as String? ?? '',
      summary: json['summary'] as String? ?? '',
      coverUrl: json['cover_url'] as String? ?? '',
      publishedAt: json['published_at'] as String? ?? '',
    );
  }
}

/// 一页信息流（推荐/分类/搜索共用）。
class AacgFeedPage {
  final String title;
  final List<AacgArticle> items;
  final int page;
  final int maxPage; // 0 = 页面未报告总页数
  final bool hasNext;
  final bool hasPrev;

  AacgFeedPage({
    required this.title,
    required this.items,
    required this.page,
    required this.maxPage,
    required this.hasNext,
    required this.hasPrev,
  });

  factory AacgFeedPage.fromJson(Map<String, dynamic> json) {
    return AacgFeedPage(
      title: json['title'] as String? ?? '',
      items: (json['items'] as List<dynamic>?)
              ?.map((e) => AacgArticle.fromJson(e as Map<String, dynamic>))
              .toList() ??
          const <AacgArticle>[],
      page: json['page'] as int? ?? 1,
      maxPage: json['maxPage'] as int? ?? 0,
      hasNext: json['hasNext'] as bool? ?? false,
      hasPrev: json['hasPrev'] as bool? ?? false,
    );
  }
}

class AacgCategory {
  final String name;
  final String url;

  AacgCategory({required this.name, required this.url});

  factory AacgCategory.fromJson(Map<String, dynamic> json) {
    return AacgCategory(
      name: json['name'] as String? ?? '',
      url: json['url'] as String? ?? '',
    );
  }
}

class AacgVideoLink {
  final String url;
  final String type;
  final String posterUrl;

  AacgVideoLink({required this.url, this.type = '', this.posterUrl = ''});

  factory AacgVideoLink.fromJson(Map<String, dynamic> json) {
    return AacgVideoLink(
      url: json['url'] as String? ?? '',
      type: json['type'] as String? ?? '',
      posterUrl: json['poster_url'] as String? ?? '',
    );
  }
}

class AacgArticleDetail {
  final AacgArticle article;
  final String content;
  final List<AacgVideoLink> videos;

  AacgArticleDetail({
    required this.article,
    required this.content,
    required this.videos,
  });

  factory AacgArticleDetail.fromJson(Map<String, dynamic> json) {
    return AacgArticleDetail(
      article: AacgArticle.fromJson(json),
      content: json['content'] as String? ?? '',
      videos: (json['videos'] as List<dynamic>?)
              ?.map((e) => AacgVideoLink.fromJson(e as Map<String, dynamic>))
              .toList() ??
          const <AacgVideoLink>[],
    );
  }
}
