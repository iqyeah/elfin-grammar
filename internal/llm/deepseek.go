// Package llm 提供两类客户端(均实现 Completer 接口):
//   - Client:OpenAI 兼容 chat/completions(DeepSeek 等第三方厂商,api_key 认证);
//   - GrammarClient:内置 LLM(ByteDP)定制 grammar 接口(access_token 认证,提示词在服务端)。
//
// 所有请求经内部 gate 串行排队：1 req/s 限频，积压时保留最新请求。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Completer 是语法处理能力的抽象:内置 LLM(GrammarClient)与 OpenAI 兼容
// 客户端(Client)均实现,main 经 atomic.Pointer[Completer] 热替换。
type Completer interface {
	// Both 一次调用同时返回英文与中文:
	// 英文输入 english=纠正句、chinese=纠正句的中文翻译;
	// 中文输入 english=英文翻译、chinese=原文回显。
	Both(ctx context.Context, sentence string) (english, chinese string, err error)
	Analyze(ctx context.Context, sentence string) (string, error)
}

// 内置 LLM(ByteDP)专属哨兵错误,main 按其分流用户文案:
//   - ErrInsufficientBalance:余额不足,请到设置中友情捐献;
//   - ErrRateLimited:调用太频繁,请稍后重试;
//   - ErrLoginExpired:登录已过期,请重新登录;
//   - ErrTrialExhausted:匿名试用次数已用完(服务端 403 TRIAL_001),请登录。
var (
	ErrInsufficientBalance = errors.New("llm: 余额不足")
	ErrRateLimited         = errors.New("llm: 调用太频繁")
	ErrLoginExpired        = errors.New("llm: 登录已过期")
	ErrTrialExhausted      = errors.New("llm: 试用次数已用完")
)

// Client 是 DeepSeek 客户端。
type Client struct {
	apiKey  string
	baseURL string
	model   string
	hc      *http.Client
	gate    *gate // 请求闸门：串行 + 1/s 限频 + latest-wins
}

// New 创建客户端。proxyURL 为空时沿用环境变量（http_proxy/https_proxy）。
func New(apiKey, baseURL, model, proxyURL string) *Client {
	hc := &http.Client{Timeout: 15 * time.Second}
	if proxyURL != "" {
		hc.Transport = &http.Transport{Proxy: http.ProxyURL(mustParseURL(proxyURL))}
	}
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}
	if model == "" {
		model = "deepseek-flash"
	}
	return &Client{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/"), model: model, hc: hc,
		gate: newGate(time.Second)}
}

// mustParseURL 解析代理地址，失败时降级为环境变量代理。
func mustParseURL(u string) *url.URL {
	pu, err := url.Parse(u)
	if err != nil {
		return nil
	}
	return pu
}

const bothPrompt = `You are a bilingual writing assistant. The user gives a sentence in English or Chinese.
- If the input is English: correct its grammar, spelling and punctuation, then translate the corrected sentence into natural Chinese.
- If the input is Chinese: translate it into natural English.
Output ONLY a JSON object with two fields, no markdown, no commentary:
{"english": "<corrected English sentence, or the English translation for Chinese input>", "chinese": "<natural Chinese translation of the English input, or the original Chinese input unchanged>"}`

const analyzePrompt = `你是一名英语老师。如果用户的句子有语法错误，用一句简短的话指出，然后给出自然地道的版本，再简要解释关键语法点（简洁）。

重点关注动词：检查谓语动词的形式——原形、-ing、-ed——以及它的时态。说明正确形式及其原因。

只输出这三个部分。 `

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// thinkingConfig 控制 DeepSeek 思考模式；type=disabled 关闭 think 以最快速返回。
type thinkingConfig struct {
	Type string `json:"type"`
}

type chatRequest struct {
	Model           string         `json:"model"`
	Messages        []chatMessage  `json:"messages"`
	Temperature     float64        `json:"temperature"`
	Stream          bool           `json:"stream"`
	Thinking        thinkingConfig `json:"thinking"`
	MaxTokens       int            `json:"max_tokens,omitempty"`
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Both 一次调用同时返回英文与中文(见 Completer.Both 注释)。
// 上游返回 JSON,解析失败(非 JSON/字段缺失)直接报错,不返回半成品。
func (c *Client) Both(ctx context.Context, sentence string) (string, string, error) {
	out, _, err := c.complete(ctx, bothPrompt, sentence, 768)
	if err != nil {
		return "", "", err
	}
	return parseBothJSON(out)
}

// Analyze 简要指出语法错误、给出地道表达,并讲解关键语法点。
func (c *Client) Analyze(ctx context.Context, sentence string) (string, error) {
	out, _, err := c.complete(ctx, analyzePrompt, sentence, 512)
	return out, err
}

// parseBothJSON 解析合并接口的 JSON 输出(兼容 markdown 围栏),返回英文/中文。
func parseBothJSON(content string) (string, string, error) {
	s := strings.TrimSpace(content)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	start, end := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return "", "", errors.New("llm: 合并响应非 JSON")
	}
	var data struct {
		English string `json:"english"`
		Chinese string `json:"chinese"`
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &data); err != nil {
		return "", "", fmt.Errorf("llm: 合并响应解析失败: %w", err)
	}
	english := strings.TrimSpace(data.English)
	chinese := strings.TrimSpace(data.Chinese)
	if english == "" {
		return "", "", errors.New("llm: 合并响应缺少英文")
	}
	return english, chinese, nil
}

// complete 是 chat/completions 调用的公共入口：经 gate 排队限频后执行。
func (c *Client) complete(ctx context.Context, sysPrompt, user string, maxTokens int) (string, string, error) {
	if c.apiKey == "" {
		return "", "", errors.New("llm: api_key 未配置")
	}
	return c.gate.submit(ctx, func(ctx context.Context) (string, string, error) {
		out, err := c.doComplete(ctx, sysPrompt, user, maxTokens)
		return out, "", err
	})
}

// doComplete 发送一次 chat/completions 请求（仅由 gate 调用，保证 1 req/s）。
func (c *Client) doComplete(ctx context.Context, sysPrompt, user string, maxTokens int) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: user},
		},
		Temperature: 0.1,
		Stream:      false,
		// 关闭 think 模式，追求最快速返回。
		Thinking:        thinkingConfig{Type: "disabled"},
		ReasoningEffort: "minimal",
		// 语法纠正/翻译输出较短，限制生成长度加速返回。
		MaxTokens: maxTokens,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: 请求失败: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return "", fmt.Errorf("llm: 解析响应失败(%d): %w", resp.StatusCode, err)
	}
	if cr.Error != nil {
		return "", fmt.Errorf("llm: 服务错误: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 || cr.Choices[0].Message.Content == "" {
		return "", errors.New("llm: 空响应")
	}
	out := strings.TrimSpace(cr.Choices[0].Message.Content)
	out = strings.Trim(out, "\"'`")
	return out, nil
}
