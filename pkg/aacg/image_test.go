package aacg

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 与实现中的常量独立书写：改动解密常量会同时打断这里，避免同义反复。
const (
	imageTestKey = "f5d965df75336270"
	imageTestIV  = "97b60394abc2fbe1"
)

func imageTestEncrypt(t *testing.T, plain []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher([]byte(imageTestKey))
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte(imageTestIV)).CryptBlocks(encrypted, padded)
	return encrypted
}

func TestImage(t *testing.T) {
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 'J', 'F', 'I', 'F', 0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	gif := []byte("GIF89a fixture gif bytes")
	encryptedJPEG := imageTestEncrypt(t, jpeg)
	encryptedGIF := imageTestEncrypt(t, gif)
	mislabeled := imageTestEncrypt(t, []byte("plain text, not an image, but block aligned data"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plain.jpeg":
			w.Write(jpeg)
		case "/secret.jpeg":
			w.Write(encryptedJPEG)
		case "/secret.gif":
			w.Write(encryptedGIF)
		case "/mislabeled.bin":
			w.Write(mislabeled)
		case "/garbage.bin":
			w.Write([]byte("not an image at all"))
		case "/missing.jpeg":
			w.WriteHeader(http.StatusNotFound)
		case "/oversized.bin":
			w.Write(bytes.Repeat([]byte{0xFF}, maxImageBytes+1))
		default:
			t.Errorf("unexpected image request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv, "")
	cases := []struct {
		name, path, contentType string
		want                    []byte
		errText                 string
	}{
		{name: "plain jpeg passes through", path: "/plain.jpeg", contentType: "image/jpeg", want: jpeg},
		{name: "encrypted jpeg is decrypted", path: "/secret.jpeg", contentType: "image/jpeg", want: jpeg},
		{name: "encrypted gif is decrypted", path: "/secret.gif", contentType: "image/gif", want: gif},
		{name: "decrypts to non-image", path: "/mislabeled.bin", errText: "unrecognized payload"},
		{name: "garbage payload", path: "/garbage.bin", errText: "unrecognized payload"},
		{name: "upstream error", path: "/missing.jpeg", errText: "HTTP 404"},
		{name: "oversized response", path: "/oversized.bin", errText: "exceeds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, contentType, err := c.Image(context.Background(), srv.URL+tc.path)
			if tc.errText != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errText) || data != nil || contentType != "" {
					t.Fatalf("Image = %v, %q, %v; want error %q", data, contentType, err, tc.errText)
				}
				return
			}
			if err != nil || contentType != tc.contentType || !bytes.Equal(data, tc.want) {
				t.Fatalf("Image = %d bytes, %q, %v; want %q", len(data), contentType, err, tc.contentType)
			}
		})
	}
}
