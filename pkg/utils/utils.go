package utils

import (
	"math/rand/v2"
	"time"
)

// RandomDelay 随机延迟，模拟人工操作
func RandomDelay(min, max time.Duration) {
	if min >= max {
		time.Sleep(min)
		return
	}

	delay := min + time.Duration(rand.Int64N(int64(max-min)))
	time.Sleep(delay)
}

// RandomInt 生成指定范围内的随机整数
func RandomInt(min, max int) int {
	if min >= max {
		return min
	}
	return min + rand.IntN(max-min)
}
