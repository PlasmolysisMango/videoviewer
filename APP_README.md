# JavDB 跨平台应用

基于 Go HTTP Server + Flutter 前端的跨平台视频搜索、播放、下载应用。

## 架构

```
┌─────────────────┐     HTTP/JSON     ┌──────────────────┐
│  Flutter 前端   │ ◄──────────────► │  Go HTTP Server  │
│  (Android/Win)  │                   │  (localhost:9090)│
└─────────────────┘                   └────────┬─────────┘
                                               │
                        ┌──────────────────────┼──────────────────────┐
                        ▼                      ▼                      ▼
               ┌─────────────────┐   ┌─────────────────┐   ┌─────────────────┐
               │  pkg/javdb      │   │  pkg/av         │   │  下载目录       │
               │  (榜单/搜索/磁链)│   │  (MissAV/播放)  │   │  (-dl-dir)      │
               └─────────────────┘   └─────────────────┘   └─────────────────┘
```

## 快速开始

### 1. 启动 Go HTTP Server

```bash
# 编译
go build -o javdbserver ./cmd/javdbserver

# 运行（需要 JAVDB_TOKEN 环境变量）
export JAVDB_TOKEN='your_jwt_token_here'
./javdbserver -addr :9090
```

### 2. 运行 Flutter 应用

```bash
cd flutter_app

# 安装依赖
flutter pub get

# 运行（确保 Go server 已在 9090 端口运行）
flutter run
```

## API 端点

### JavDB（榜单、分类、搜索）

| 端点 | 方法 | 说明 |
|------|------|------|
| `/health` | GET | 健康检查 |
| `/api/login` | POST | 登录 `{username, password}` |
| `/api/search` | GET | 搜索 `?q=keyword&limit=20&page=1` |
| `/api/movie/:id` | GET | 电影详情 + 磁链 |
| `/api/ranking/:kind` | GET | 榜单 `?category=&period=&limit=` |
| `/api/magnets/:id` | GET | 磁链列表 |
| `/api/tags` | GET | 标签分组 |
| `/api/actor/:id` | GET | 演员详情 |

### AV（MissAV/Jable/HohoJ 播放、下载）

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/av/search` | GET | 搜索视频 `?q=keyword&source=missav&limit=20` |
| `/api/av/detail/:code` | GET | 视频详情 |
| `/api/av/resolve/:code` | GET | 解析所有可播放流 |
| `/api/av/play/:code` | GET | 获取最优播放流 |
| `/api/av/download/:code` | POST | 下载视频（需配置 `-dl-dir`） |

## 项目结构

```
videoviewer/
├── cmd/
│   ├── javdbcli/          # CLI 工具
│   └── javdbserver/       # HTTP Server
├── pkg/
│   └── javdb/             # JavDB API 封装
└── flutter_app/           # Flutter 前端
    └── lib/
        ├── api/           # HTTP client + models
        ├── providers/     # 状态管理
        └── screens/       # 页面
```

## 功能清单

- [x] 登录（App API JWT）
- [x] 搜索
- [x] 电影详情
- [x] 磁链列表
- [x] 榜单（按分类/周期）
- [ ] 下载管理
- [ ] 视频播放
- [ ] 演员详情

## 构建

### Windows

```bash
# Go server
go build -o javdbserver.exe ./cmd/javdbserver

# Flutter
cd flutter_app
flutter build windows
```

### Android

```bash
# Go server (需要 gomobile 或独立运行)
# 方案 A: 使用 gomobile 编译为 .so
# 方案 B: Go server 作为独立 APK 后台服务

# Flutter
cd flutter_app
flutter build apk
```

## 配置

Go HTTP Server 支持以下参数：

- `-addr`: 监听地址（默认 `:9090`）
- `-api-base`: JavDB API 地址（默认 `https://jdforrepam.com/api`）
- `-token`: App JWT token（或设置 `JAVDB_TOKEN` 环境变量）
- `-cookie`: Web session cookie（或设置 `JAVDB_COOKIE` 环境变量）
- `-dl-dir`: 下载目录（或设置 `JAVDB_DL_DIR` 环境变量）

## 注意事项

1. **登录**: App API 登录需要用户名（不是邮箱），账号必须是在 App 注册的
2. **Token**: JWT token 有过期时间，需要定期重新登录
3. **网络**: 需要能访问 `jdforrepam.com`（可能需要代理）
4. **Flutter**: 默认连接 `http://localhost:9090`，生产环境需要配置

## 开发计划

- [ ] 下载功能（aria2c 集成）
- [ ] 视频播放（下载后本地播放）
- [ ] 榜单页面 UI
- [ ] 演员页面 UI
- [ ] 收藏功能
- [ ] 历史记录
