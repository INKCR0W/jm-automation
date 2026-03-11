package utils

import (
	"testing"
	"time"
)

func TestRandomInt(t *testing.T) {
	tests := []struct {
		name string
		min  int
		max  int
	}{
		{"normal range", 0, 10},
		{"same values", 5, 5},
		{"large range", 0, 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RandomInt(tt.min, tt.max)

			if tt.min == tt.max {
				if result != tt.min {
					t.Errorf("RandomInt(%d, %d) = %d, want %d", tt.min, tt.max, result, tt.min)
				}
				return
			}

			if result < tt.min || result >= tt.max {
				t.Errorf("RandomInt(%d, %d) = %d, want value in range [%d, %d)", tt.min, tt.max, result, tt.min, tt.max)
			}
		})
	}
}

func TestRandomIntDistribution(t *testing.T) {
	// 测试随机分布是否合理
	min, max := 0, 10
	counts := make(map[int]int)
	iterations := 10000

	for i := 0; i < iterations; i++ {
		result := RandomInt(min, max)
		counts[result]++
	}

	// 每个数字应该出现约 1000 次（10000 / 10）
	// 允许 30% 的偏差
	expectedCount := iterations / (max - min)
	tolerance := float64(expectedCount) * 0.3

	for i := min; i < max; i++ {
		count := counts[i]
		diff := float64(count - expectedCount)
		if diff < 0 {
			diff = -diff
		}

		if diff > tolerance {
			t.Errorf("RandomInt distribution issue: value %d appeared %d times, expected around %d", i, count, expectedCount)
		}
	}
}

func TestRandomDelay(t *testing.T) {
	tests := []struct {
		name string
		min  time.Duration
		max  time.Duration
	}{
		{"milliseconds", 10 * time.Millisecond, 50 * time.Millisecond},
		{"same duration", 100 * time.Millisecond, 100 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			RandomDelay(tt.min, tt.max)
			elapsed := time.Since(start)

			if tt.min == tt.max {
				// 允许 10ms 的误差
				if elapsed < tt.min-10*time.Millisecond || elapsed > tt.max+10*time.Millisecond {
					t.Errorf("RandomDelay(%v, %v) took %v, expected around %v", tt.min, tt.max, elapsed, tt.min)
				}
				return
			}

			// 允许 10ms 的误差
			if elapsed < tt.min-10*time.Millisecond || elapsed > tt.max+10*time.Millisecond {
				t.Errorf("RandomDelay(%v, %v) took %v, want duration in range [%v, %v]", tt.min, tt.max, elapsed, tt.min, tt.max)
			}
		})
	}
}

func BenchmarkRandomInt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		RandomInt(0, 100)
	}
}

func BenchmarkRandomDelay(b *testing.B) {
	for i := 0; i < b.N; i++ {
		RandomDelay(1*time.Microsecond, 10*time.Microsecond)
	}
}
