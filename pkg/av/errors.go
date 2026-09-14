package av

import "errors"

// 预定义错误，调用方可用 errors.Is 判定。
var (
	// ErrNotFound 表示按番号/关键词未找到资源。
	ErrNotFound = errors.New("av: resource not found")
	// ErrNoStream 表示未解析到任何可播放流。
	ErrNoStream = errors.New("av: no playable stream")
	// ErrEncryptedStream 表示 HLS 流使用了 EXT-X-KEY 加密，当前不支持解密下载。
	ErrEncryptedStream = errors.New("av: encrypted stream not supported")
	// ErrNoSource 表示没有注册任何可用数据源。
	ErrNoSource = errors.New("av: no source registered")
	// ErrSourceNotFound 表示指定名称的数据源未注册。
	ErrSourceNotFound = errors.New("av: source not found")
	// ErrNotImplemented 表示该数据源不支持此项能力。
	ErrNotImplemented = errors.New("av: capability not implemented by source")
)
