package aacg

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"io"
	"net/http"
)

// 站点封面/缩略图经 AES-128-CBC(PKCS7) 混淆存储，页面 JS（loadImage →
// decryptImage，定义于 /usr/plugins/tbxw/js/zzz.js）在浏览器端解成 data URL；
// 直连拿到的是密文，客户端无法解码，因此由服务端解密后转发。
const (
	imageKey      = "f5d965df75336270"
	imageIV       = "97b60394abc2fbe1"
	maxImageBytes = 8 << 20
)

// Image 抓取站点图片并返回解码后的字节与 Content-Type。
// 带图片魔数的明文直接透传；否则按站点方案解密，解密后仍无魔数则报错。
func (c *Client) Image(ctx context.Context, raw string) ([]byte, string, error) {
	if err := c.validateURL(raw); err != nil {
		return nil, "", err
	}
	budget := maxHops
	ctx = context.WithValue(ctx, hopKey{}, &budget)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "image/*,*/*")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("aacg image: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("aacg image: %w", err)
	}
	if len(body) > maxImageBytes {
		return nil, "", fmt.Errorf("aacg image: response exceeds %d bytes", maxImageBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("aacg image: HTTP %d", resp.StatusCode)
	}
	if contentType, ok := imageMagic(body); ok {
		return body, contentType, nil
	}
	plain, err := decryptImage(body)
	if err != nil {
		return nil, "", fmt.Errorf("aacg image: unrecognized payload: %w", err)
	}
	contentType, ok := imageMagic(plain)
	if !ok {
		return nil, "", fmt.Errorf("aacg image: unrecognized payload")
	}
	return plain, contentType, nil
}

// decryptImage 复刻站点 js 的 AES-128-CBC + PKCS7 unpad（key/iv 为 ASCII 字节）。
func decryptImage(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext is not block aligned")
	}
	block, err := aes.NewCipher([]byte(imageKey))
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, []byte(imageIV)).CryptBlocks(plain, data)
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > aes.BlockSize || padding > len(plain) {
		return nil, fmt.Errorf("invalid padding")
	}
	for _, b := range plain[len(plain)-padding:] {
		if int(b) != padding {
			return nil, fmt.Errorf("invalid padding")
		}
	}
	return plain[:len(plain)-padding], nil
}

func imageMagic(data []byte) (string, bool) {
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg", true
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", true
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif", true
	case len(data) >= 12 && bytes.Equal(data[0:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp", true
	}
	return "", false
}
