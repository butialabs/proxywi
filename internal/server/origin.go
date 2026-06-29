package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

func HashOrigin(secret []byte, ip string) string {
	if ip == "" {
		return ""
	}
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(ip))
	return hex.EncodeToString(m.Sum(nil))
}

func ShortOrigin(key string) string {
	const n = 12
	if len(key) <= n {
		return key
	}
	return key[:n]
}
