// handler 小工具函数。
package api

import (
	"encoding/json"
	"strconv"
)

func strconvParseInt(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

func jsonUnmarshal(b []byte, v any) error {
	return json.Unmarshal(b, v)
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
