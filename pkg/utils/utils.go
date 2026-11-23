package utils

import (
	"context"
	"math/rand/v2"
	"time"
)

// Wait 等待指定时长，context 取消时提前返回
func Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil || delay <= 0 {
		return err
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
	return ctx.Err()
}

// RandomDelay 随机延迟，模拟人工操作
func RandomDelay(ctx context.Context, min, max time.Duration) error {
	if min >= max {
		return Wait(ctx, min)
	}
	delay := min + time.Duration(rand.Int64N(int64(max-min)))
	return Wait(ctx, delay)
}

// RandomInt 生成指定范围内的随机整数
func RandomInt(min, max int) int {
	if min >= max {
		return min
	}
	return min + rand.IntN(max-min)
}
