package util

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
)

// 资金台账金额一律以「分」(int64) 存储与计算，杜绝浮点累计误差。
// 对外（JSON/前端）使用两位小数字符串表示「元」。

// YuanToCents 将元（float64）四舍五入为分。
func YuanToCents(yuan float64) int64 {
	return int64(math.Round(yuan * 100))
}

// ErrInvalidAmount 金额非法。
var ErrInvalidAmount = errors.New("invalid amount")

// ParseYuanToCents 将「元」精确解析为「分」。
// 接受 json.Number（数字或字符串）或字符串，按十进制精确换算，最多两位小数；
// 超过两位小数、非数字、负数或超出上限均视为非法，避免浮点误差。
func ParseYuanToCents(n json.Number) (int64, error) {
	s := strings.TrimSpace(n.String())
	if s == "" {
		return 0, ErrInvalidAmount
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = strings.TrimPrefix(s, "-")
	}
	if s == "" || strings.ContainsAny(s, "eE+") {
		return 0, ErrInvalidAmount
	}
	parts := strings.SplitN(s, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole < 0 {
		return 0, ErrInvalidAmount
	}
	var frac int64
	if len(parts) == 2 {
		f := parts[1]
		if len(f) > 2 || f == "" {
			return 0, ErrInvalidAmount
		}
		if len(f) == 1 {
			f += "0"
		}
		frac, err = strconv.ParseInt(f, 10, 64)
		if err != nil {
			return 0, ErrInvalidAmount
		}
	}
	cents := whole*100 + frac
	if neg {
		cents = -cents
	}
	if cents < 0 {
		return 0, ErrInvalidAmount
	}
	return cents, nil
}

// CentsToYuan 将分换算为元的 float64（仅用于需要数值的场景）。
func CentsToYuan(cents int64) float64 {
	return float64(cents) / 100.0
}

// FormatCents 将分格式化为带千分位、两位小数的元字符串。
func FormatCents(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	whole := cents / 100
	frac := cents % 100

	var b strings.Builder
	s := strconv.FormatInt(whole, 10)
	n := len(s)
	for i, ch := range s {
		if i > 0 && (n-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	out := b.String() + "." + strconv.FormatInt(frac/10, 10) + strconv.FormatInt(frac%10, 10)
	if neg {
		return "-" + out
	}
	return out
}
