package av

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// cookieJar 是一个极简的、线程安全的 Cookie 存储，实现 http.CookieJar 接口。
// 相比标准库 net/http/cookiejar，这里放宽了域名/路径匹配规则，
// 更适合爬虫在多子域重定向场景下保持会话 Cookie。
type cookieJar struct {
	mu sync.Mutex
	m  map[string][]*http.Cookie
}

func newCookieJar() *cookieJar {
	return &cookieJar{m: map[string][]*http.Cookie{}}
}

// hostKey 以去掉端口后的主机名作为分组键。
func hostKey(u *url.URL) string {
	h := u.Hostname()
	return strings.ToLower(h)
}

func (j *cookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	key := hostKey(u)
	now := time.Now()
	for _, ck := range cookies {
		if ck == nil || ck.Name == "" {
			continue
		}
		// 计算过期时间：优先 MaxAge，其次 Expires。
		var expires time.Time
		switch {
		case ck.MaxAge > 0:
			expires = now.Add(time.Duration(ck.MaxAge) * time.Second)
		case ck.MaxAge < 0:
			j.remove(key, ck.Name)
			continue
		default:
			expires = ck.Expires
		}
		if !expires.IsZero() && now.After(expires) {
			j.remove(key, ck.Name)
			continue
		}
		ck.Expires = expires
		j.replace(key, ck)
	}
}

func (j *cookieJar) replace(key string, ck *http.Cookie) {
	cookies := j.m[key]
	for i, c := range cookies {
		if c.Name == ck.Name {
			cookies[i] = ck
			return
		}
	}
	j.m[key] = append(cookies, ck)
}

func (j *cookieJar) remove(key, name string) {
	cookies := j.m[key]
	out := cookies[:0]
	for _, c := range cookies {
		if c.Name != name {
			out = append(out, c)
		}
	}
	j.m[key] = out
}

func (j *cookieJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	key := hostKey(u)
	now := time.Now()
	cookies := j.m[key]
	out := make([]*http.Cookie, 0, len(cookies))
	for _, c := range cookies {
		if !c.Expires.IsZero() && now.After(c.Expires) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// dialSocks5 实现一个无认证 SOCKS5 客户端拨号器，供 Transport.DialContext 使用。
// 支持 username/password（SOCKS5 GSS-less 用户名密码子协商）。
func dialSocks5(ctx context.Context, d *net.Dialer, proxyURL *url.URL, target string) (net.Conn, error) {
	conn, err := d.DialContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	// 构造 greeting：默认无认证；若带凭据则提出用户名密码子协商
	greeting := []byte{0x05, 1, 0x00}
	var user, pass string
	if proxyURL.User != nil {
		user = proxyURL.User.Username()
		pass, _ = proxyURL.User.Password()
		if user != "" {
			greeting = []byte{0x05, 2, 0x00, 0x02}
		}
	}
	if _, err = conn.Write(greeting); err != nil {
		_ = conn.Close()
		return nil, err
	}
	resp := make([]byte, 2)
	if _, err = io.ReadFull(conn, resp); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if resp[0] != 0x05 {
		_ = conn.Close()
		return nil, errors.New("socks5: invalid version")
	}

	switch resp[1] {
	case 0x02: // 需要用户名密码认证
		if user == "" {
			_ = conn.Close()
			return nil, errors.New("socks5: auth required but no credentials")
		}
		auth := make([]byte, 0, 3+len(user)+len(pass))
		auth = append(auth, 0x01, byte(len(user)))
		auth = append(auth, user...)
		auth = append(auth, byte(len(pass)))
		auth = append(auth, pass...)
		if _, err = conn.Write(auth); err != nil {
			_ = conn.Close()
			return nil, err
		}
		aser := make([]byte, 2)
		if _, err = io.ReadFull(conn, aser); err != nil {
			_ = conn.Close()
			return nil, err
		}
		if aser[1] != 0x00 {
			_ = conn.Close()
			return nil, fmt.Errorf("socks5: auth failed status %d", aser[1])
		}
	case 0x00:
		// 无需认证，继续
	default:
		_ = conn.Close()
		return nil, fmt.Errorf("socks5: unsupported method %d", resp[1])
	}

	// CONNECT 请求（域名地址 ATYP=0x03）
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	port, err := net.LookupPort("tcp", portStr)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, host...)
	req = append(req, byte(port>>8), byte(port&0xff))
	if _, err = conn.Write(req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	head := make([]byte, 4)
	if _, err = io.ReadFull(conn, head); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if head[1] != 0x00 {
		_ = conn.Close()
		return nil, fmt.Errorf("socks5: connect failed reply %d", head[1])
	}
	// 跳过绑定地址
	if err := skipSocks5Addr(conn, head[3]); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func skipSocks5Addr(r io.Reader, atyp byte) error {
	switch atyp {
	case 0x01: // IPv4
		buf := make([]byte, 4)
		_, err := io.ReadFull(r, buf)
		return err
	case 0x03: // 域名
		var l [1]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return err
		}
		buf := make([]byte, l[0])
		_, err := io.ReadFull(r, buf)
		return err
	case 0x04: // IPv6
		buf := make([]byte, 16)
		_, err := io.ReadFull(r, buf)
		return err
	default:
		return fmt.Errorf("socks5: unknown atyp %d", atyp)
	}
}
