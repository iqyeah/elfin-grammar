package llm

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrSuperseded 表示请求已被更新的请求替换（latest-wins 合并），调用方应静默放弃。
var ErrSuperseded = errors.New("llm: 请求已被更新请求替换")

// gate 是 LLM 请求闸门：统一串行化所有请求（并发排队），发送间隔至少 interval
// （默认 1 req/s），队列积压时只保留最新请求（latest wins）。
// 单槽等待队列（pending）：新请求替换旧请求，避免积压过期请求逐条发送。
type gate struct {
	mu       sync.Mutex
	pending  *gateReq // 单槽等待队列，latest wins
	running  bool     // 是否已有调度器在运行（批次接力，无任务时退出）
	interval time.Duration
	lastSend time.Time
}

type gateReq struct {
	ctx  context.Context
	fn   func(ctx context.Context) (string, string, error)
	done chan gateResult
}

type gateResult struct {
	a   string // 主结果（单结果请求用 a 返回，b 置空）
	b   string // 副结果（合并请求 both 的第二个字段）
	err error
}

func newGate(interval time.Duration) *gate {
	return &gate{interval: interval}
}

// submit 提交请求并阻塞等待结果。队列中已有等待请求时替换之（旧请求立即返回
// ErrSuperseded）；已有请求在途时排队等待（间隔至少 interval）。
// 单结果请求把 fn 包装为 "返回 a, 置空 b" 即可。
func (g *gate) submit(ctx context.Context, fn func(ctx context.Context) (string, string, error)) (string, string, error) {
	req := &gateReq{ctx: ctx, fn: fn, done: make(chan gateResult, 1)}
	g.mu.Lock()
	if g.pending != nil {
		old := g.pending
		g.pending = req
		g.mu.Unlock()
		old.done <- gateResult{err: ErrSuperseded}
	} else {
		g.pending = req
		if !g.running {
			g.running = true
			go g.drainLoop()
		}
		g.mu.Unlock()
	}
	res := <-req.done
	return res.a, res.b, res.err
}

// drainLoop 是请求调度器：循环取单槽请求，按 interval 限频执行。
// 无任务时退出（running=false），由下一个 submit 重新拉起，避免常驻 goroutine。
func (g *gate) drainLoop() {
	for {
		g.mu.Lock()
		req := g.pending
		g.pending = nil
		if req == nil {
			g.running = false
			g.mu.Unlock()
			return
		}
		wait := g.interval - time.Since(g.lastSend)
		g.mu.Unlock()

		if wait > 0 {
			time.Sleep(wait)
		}
		// 排队等待已耗尽调用方超时：不再发送，直接失败。
		if err := req.ctx.Err(); err != nil {
			req.done <- gateResult{err: err}
			continue
		}
		g.mu.Lock()
		g.lastSend = time.Now()
		g.mu.Unlock()
		a, b, err := req.fn(req.ctx)
		req.done <- gateResult{a: a, b: b, err: err}
	}
}
