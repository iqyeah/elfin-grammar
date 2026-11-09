// GrammarClient 是内置 LLM(ByteDP)客户端:实现 Completer 接口,三方法走定制
// /v1/grammar/{correct|translate|analyze} 接口(access_token 认证,提示词在服务端)。
//
// access_token 仅内存持有,首次调用/过期时用 refresh_token 静默续签并回写配置;
// 从未登录(无 refresh_token)时按匿名试用发请求(带 X-Device-Id,服务端累计限 10 次,
// 超出返回 403 TRIAL_001 -> ErrTrialExhausted);续签失效(refresh 401)清登录态并返回
// ErrLoginExpired。402 BALANCE_001 返回 ErrInsufficientBalance,429 RATELIMIT_001 返回
// ErrRateLimited(解析 Retry-After)。请求经 gate 串行 + 1 req/s(与服务端收费账号
// 限流对齐),积压 latest-wins。
package llm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"elfin-grammar/internal/config"
)

// GrammarClient 是内置 LLM 客户端(见文件注释)。
type GrammarClient struct {
	cfg         *config.Config
	accessToken string
	mu          sync.Mutex // 串行化续签/登录态读写,避免并发 401 重复 refresh
	hc          *http.Client
	gate        *gate
}

// NewGrammarClient 创建内置 LLM 客户端。proxyURL 为空时沿用环境变量。
func NewGrammarClient(cfg *config.Config, proxyURL string) *GrammarClient {
	hc := &http.Client{Timeout: 15 * time.Second}
	if proxyURL != "" {
		hc.Transport = &http.Transport{Proxy: http.ProxyURL(mustParseURL(proxyURL))}
	}
	return &GrammarClient{cfg: cfg, hc: hc, gate: newGate(time.Second)}
}

// Both 一次调用同时返回英文与中文:走服务端 /v1/grammar/both 合并接口
// (服务端一次上游调用,提示词在服务端,JSON 输出)。
// 英文输入 english=纠正句、chinese=中文翻译;中文输入 english=英文翻译、chinese=原文回显。
func (g *GrammarClient) Both(ctx context.Context, sentence string) (string, string, error) {
	body, _, err := g.run(ctx, "both", sentence, false)
	if err != nil {
		return "", "", err
	}
	return parseBothJSON(body) // 与 OpenAI 兼容客户端共用解析
}

// Analyze 简要指出语法错误、给出地道表达并讲解关键语法点。
func (g *GrammarClient) Analyze(ctx context.Context, sentence string) (string, error) {
	out, _, err := g.run(ctx, "analyze", sentence, false)
	return out, err
}

// Warmup 静默续签(启动时后台调用,失败仅返回 error 不清理登录态),
// 使后续首个请求无需等待 refresh 往返。
func (g *GrammarClient) Warmup(ctx context.Context) error {
	return g.ensureToken(ctx)
}

// ClearLogin 清空登录态(access 内存 + refresh 持久化字段),供退出登录调用。
func (g *GrammarClient) ClearLogin() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.accessToken = ""
	g.cfg.BytedpRefreshToken = ""
	g.cfg.BytedpEmail = ""
	_ = config.Save(g.cfg)
}

// AccessToken 返回当前 access token(退出登录时调服务端注销用,可能为空)。
func (g *GrammarClient) AccessToken() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.accessToken
}

// run 经 gate 排队限频后执行一次 grammar 请求。
func (g *GrammarClient) run(ctx context.Context, kind, text string, toEnglish bool) (string, string, error) {
	return g.gate.submit(ctx, func(ctx context.Context) (string, string, error) {
		out, err := g.doRun(ctx, kind, text, toEnglish)
		return out, "", err
	})
}

// doRun 发送 grammar 请求;401 AUTH_002 时续签后重试一次(仅由 gate 调用)。
// 无 access_token 且从未登录(无 refresh_token)时走匿名试用(带 X-Device-Id,
// 不携带 Authorization;服务端按设备累计限 10 次)。
func (g *GrammarClient) doRun(ctx context.Context, kind, text string, toEnglish bool) (string, error) {
	for attempt := 0; ; attempt++ {
		tok := g.currentToken()
		if tok == "" {
			if g.cfg.BytedpRefreshToken == "" {
				// 未登录:匿名试用,不发 Authorization(服务端计数,超限 ErrTrialExhausted)
			} else {
				if err := g.ensureToken(ctx); err != nil {
					return "", err
				}
				tok = g.currentToken()
			}
		}
		out, retry, err := g.postGrammar(ctx, kind, text, toEnglish, tok)
		if err == nil {
			return out, nil
		}
		if !retry || attempt >= 1 {
			return "", err
		}
	}
}

// postGrammar 发送单次 grammar 请求。retry=true 表示已续签可重试一次。
// 始终携带 X-Device-Id:登录链路用于登录上报,匿名链路用于试用计数。
func (g *GrammarClient) postGrammar(
	ctx context.Context, kind, text string, toEnglish bool, tok string,
) (string, bool, error) {
	payload := map[string]any{"text": text}
	if kind == "translate" {
		payload["to_english"] = toEnglish
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", false, err
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, strings.TrimRight(g.cfg.BaseURL, "/")+"/grammar/"+kind,
		bytes.NewReader(body),
	)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	req.Header.Set("X-Request-Id", newRequestID())
	req.Header.Set("X-Device-Id", EnsureDeviceID(g.cfg))

	resp, err := g.hc.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("llm: 请求失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", false, err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// both 接口响应为 JSON 对象 {"english":..,"chinese":..},没有 text 字段:
		// 校验英文非空后原样返回 body,由调用方(Both)统一解析。
		if kind == "both" {
			var bj struct {
				English string `json:"english"`
			}
			if err := json.Unmarshal(data, &bj); err != nil || strings.TrimSpace(bj.English) == "" {
				return "", false, fmt.Errorf("llm: 空响应")
			}
			return strings.TrimSpace(string(data)), false, nil
		}
		var gr struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(data, &gr); err != nil {
			return "", false, fmt.Errorf("llm: 解析响应失败: %w", err)
		}
		out := strings.TrimSpace(gr.Text)
		if out == "" {
			return "", false, fmt.Errorf("llm: 空响应")
		}
		return out, false, nil
	case http.StatusUnauthorized:
		code, _ := decodeAPIError(data)
		if code == "AUTH_002" {
			// access_token 过期:持锁续签(并发 401 时仅第一个真正 refresh),重试一次
			if err := g.refreshFor(ctx, tok); err != nil {
				return "", false, err
			}
			return "", true, nil
		}
		g.clearLoginState()
		return "", false, ErrLoginExpired
	case http.StatusForbidden: // 403 TRIAL_001 匿名试用次数已用完
		if code, _ := decodeAPIError(data); code == "TRIAL_001" {
			return "", false, ErrTrialExhausted
		}
		code, msg := decodeAPIError(data)
		if msg == "" {
			msg = resp.Status
		}
		return "", false, fmt.Errorf("llm: 服务错误(%d %s): %s", resp.StatusCode, code, msg)
	case http.StatusPaymentRequired: // 402 BALANCE_001 余额不足
		return "", false, ErrInsufficientBalance
	case http.StatusTooManyRequests: // 429 RATELIMIT_001
		return "", false, rateLimitErr(resp)
	default:
		code, msg := decodeAPIError(data)
		if msg == "" {
			msg = resp.Status
		}
		return "", false, fmt.Errorf("llm: 服务错误(%d %s): %s", resp.StatusCode, code, msg)
	}
}

// currentToken 返回当前 access_token(可能为空)。
func (g *GrammarClient) currentToken() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.accessToken
}

// ensureToken 无 access_token 时用 refresh_token 静默续签。
func (g *GrammarClient) ensureToken(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.accessToken != "" {
		return nil
	}
	if g.cfg.BytedpRefreshToken == "" {
		return ErrLoginExpired
	}
	return g.refreshLocked(ctx)
}

// refreshFor 续签并替换 access_token;若其他协程已续签(令牌已变化)则直接成功。
func (g *GrammarClient) refreshFor(ctx context.Context, stale string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.accessToken != "" && g.accessToken != stale {
		return nil // 其他协程已续签,直接重试即可
	}
	return g.refreshLocked(ctx)
}

// refreshLocked 执行 refresh 并回写 access/refresh(调用方须持 g.mu)。
func (g *GrammarClient) refreshLocked(ctx context.Context) error {
	access, refresh, err := Refresh(ctx, g.cfg.BaseURL, g.cfg.BytedpRefreshToken)
	if err != nil {
		if err == ErrLoginExpired {
			g.accessToken = ""
			g.cfg.BytedpRefreshToken = ""
			_ = config.Save(g.cfg)
		}
		return err
	}
	g.accessToken = access
	if refresh != "" {
		g.cfg.BytedpRefreshToken = refresh
		_ = config.Save(g.cfg)
	}
	return nil
}

// clearLoginState 清空 access 与 refresh(保留 email 供重新登录预填)。
func (g *GrammarClient) clearLoginState() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.accessToken = ""
	if g.cfg.BytedpRefreshToken != "" {
		g.cfg.BytedpRefreshToken = ""
		_ = config.Save(g.cfg)
	}
}

// rateLimitErr 包装限流错误,附带服务端 Retry-After。
func rateLimitErr(resp *http.Response) error {
	ra := resp.Header.Get("Retry-After")
	if ra == "" {
		return ErrRateLimited
	}
	if n, err := strconv.Atoi(ra); err == nil {
		return fmt.Errorf("%w (retry_after=%ds)", ErrRateLimited, n)
	}
	return fmt.Errorf("%w (retry_after=%s)", ErrRateLimited, ra)
}

// newRequestID 生成 UUIDv4 形态的 X-Request-Id(rand 失败时降级时间戳串)。
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("g-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
