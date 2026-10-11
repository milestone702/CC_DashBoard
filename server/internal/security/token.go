// 上报 Token 生成。
package security

import (
	"crypto/rand"
	"encoding/hex"
)

// RandomToken 生成 32 字符十六进制节点 Token（X-CC-Node-Token）。
func RandomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
