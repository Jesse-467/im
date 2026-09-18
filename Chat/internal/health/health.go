// Package health 提供依赖健康检查抽象，供就绪探针 /readyz 使用。
//
// 约定：
//   - 存活探针 /healthz 只反映进程自身，不检查外部依赖；
//   - 就绪探针 /readyz 会真实探测数据库、缓存等依赖，任一不可用即返回未就绪。
//
// 这样在依赖短暂抖动时，进程不会被无谓重启，但会被摘除流量。
package health

import (
	"context"
	"sync"
	"time"
)

// Checker 是可被探测的依赖。
type Checker interface {
	// Name 返回依赖名称，用于探针输出。
	Name() string
	// Check 探测依赖可用性，返回 nil 表示健康。
	Check(ctx context.Context) error
}

// Result 是单个依赖的探测结果。
type Result struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Err  string `json:"err,omitempty"`
}

// CheckAll 并发探测全部依赖，保证总耗时约等于最慢的一个。
func CheckAll(ctx context.Context, checkers []Checker) []Result {
	results := make([]Result, len(checkers))
	var wg sync.WaitGroup

	for i, c := range checkers {
		wg.Add(1)
		go func(i int, c Checker) {
			defer wg.Done()
			results[i] = Result{Name: c.Name()}
			if err := c.Check(ctx); err != nil {
				results[i].OK = false
				results[i].Err = err.Error()
				return
			}
			results[i].OK = true
		}(i, c)
	}
	wg.Wait()

	return results
}

// Healthy 判断全部依赖是否健康。
func Healthy(results []Result) bool {
	for _, r := range results {
		if !r.OK {
			return false
		}
	}
	return true
}

// WithTimeout 为探测附加超时，避免探针被慢依赖拖死。
func WithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}
