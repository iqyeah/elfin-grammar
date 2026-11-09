// Package config 负责加载/保存 config.json 配置。
// 路径查找优先级（参考 bytedp 多级查找模式）：
//  1. 可执行文件同目录 config.json
//  2. 当前工作目录 config.json（兼容 go run / 开发调试）
//  3. ~/.elfin-grammar/config.json（兜底，家目录必可写）
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config 是程序运行配置。
type Config struct {
	APIKey             string  `json:"api_key"`
	BaseURL            string  `json:"base_url"`
	Model              string  `json:"model"`
	ProxyURL           string  `json:"proxy_url"`
	MaxWords           int     `json:"max_words"`
	MaxChars           int     `json:"max_chars"`
	WebMaxWords        int     `json:"web_max_words"`        // 网页选中触发：词数上限（正文常为长段落）
	WebMaxChars        int     `json:"web_max_chars"`        // 网页选中触发：字符数上限
	ClipboardMaxWords  int     `json:"clipboard_max_words"`  // 剪贴板触发：词数上限（复制内容常为整段）
	ClipboardMaxChars  int     `json:"clipboard_max_chars"`  // 剪贴板触发：字符数上限
	ClipboardLangRatio float64 `json:"clipboard_lang_ratio"` // 剪贴板触发：ASCII 字母占比达此门槛走纠正，否则按中文走中译英（默认 0.6）
	// 触发开关（设置窗口「基础配置」页签可配置）：剪贴板监控默认关闭，
	// 打字检测与选中检测默认开启。
	ClipboardMonitor bool    `json:"clipboard_monitor"`
	TypedCheck       bool    `json:"typed_check"`
	SelectionCheck   bool    `json:"selection_check"`
	DebounceMS       int     `json:"debounce_ms"`
	SelectDebounceMS int     `json:"select_debounce_ms"`
	BubbleShowMS     int     `json:"bubble_show_ms"`
	PollIdleMS       int     `json:"poll_idle_ms"`
	MinKnownRatio    float64 `json:"min_known_ratio"`
	MinLangRatio     float64 `json:"min_lang_ratio"` // 语言门槛：选中文本英文占比达此门槛走纠正、否则按中文翻译（默认 0.6）

	// DisclaimerAccepted 首次启动免责声明同意状态：true 表示已同意。
	// 首次启动弹窗确认后持久化，之后不再弹出；旧配置缺省（false）时视为未同意，弹一次。
	DisclaimerAccepted bool `json:"disclaimer_accepted"`

	// ByteDP 内置 LLM 登录态(登录/续签后持久化;device_id 首登生成 32 位随机串)。
	// 内置 LLM 走定制 grammar 接口(access_token 认证),与 OpenAI 兼容的 api_key 互斥。
	BytedpEmail        string `json:"bytedp_email"`
	BytedpRefreshToken string `json:"bytedp_refresh_token"`
	BytedpDeviceID     string `json:"bytedp_device_id"`
}

// Default 返回内置默认配置:默认厂商为「内置 LLM(ByteDP)」,走 bytedp_admin
// 定制 grammar 接口(登录态认证,未登录 10 次匿名试用、注册赠送免费额度);
// API Key 留空。已存在的 config.json 不回改(Load 仅对空字段回填)。
func Default() *Config {
	p := Providers[0] // 内置 LLM（ByteDP）
	return &Config{
		APIKey:             "",
		BaseURL:            p.BaseURL,
		Model:              p.Models[0],
		MaxWords:           40,
		MaxChars:           200,
		WebMaxWords:        400,
		WebMaxChars:        200,
		ClipboardMaxWords:  400,
		ClipboardMaxChars:  2000,
		ClipboardLangRatio: 0.6,
		ClipboardMonitor:   false,
		TypedCheck:         true,
		SelectionCheck:     true,
		DebounceMS:         300,
		SelectDebounceMS:   1000,
		BubbleShowMS:       10000,
		PollIdleMS:         1500,
		MinKnownRatio:      0.3,
		MinLangRatio:       0.6,
	}
}

// Provider 是内置 LLM 厂商预设：设置窗口选中厂商后 Base URL 固定（只读），
// 模型下拉框限定为该厂商支持的模型。厂商 OpenAI 兼容端点参考
// litellm constants.py 的 openai_compatible_endpoints。
type Provider struct {
	Name    string   // 厂商显示名
	BaseURL string   // OpenAI 兼容端点（llm 客户端拼 /chat/completions）
	Models  []string // 该厂商可选模型名（API 请求模型名，第一项为默认）
}

// Providers 是设置窗口厂商下拉框的内置选项。首位为「内置 LLM(ByteDP)」:
// 走 bytedp_admin 定制 grammar 接口(access_token 认证 + 用户计费),不走
// OpenAI 兼容 sk 直连;其余厂商 OpenAI 兼容端点参考 litellm constants.py 的
// openai_compatible_endpoints。
var Providers = []Provider{
	{Name: "内置 LLM（ByteDP）", BaseURL: "https://b.taobed.com/v1", Models: []string{"elfin-grammar"}},
	{Name: "DeepSeek", BaseURL: "https://api.deepseek.com/v1", Models: []string{"deepseek-chat", "deepseek-reasoner", "deepseek-flash"}},
	{Name: "豆包 Ark", BaseURL: "https://ark.cn-beijing.volces.com/api/v3", Models: []string{"doubao-seed-2-0-mini-260428", "doubao-seed-2-1-turbo-260628", "doubao-seed-2-1-lite-260915"}},
	{Name: "通义千问", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", Models: []string{"qwen-plus", "qwen-turbo", "qwen-flash"}},
	{Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Models: []string{"gpt-4o-mini", "gpt-4o"}},
	{Name: "Gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Models: []string{"gemini-2.5-flash"}},
	{Name: "Moonshot", BaseURL: "https://api.moonshot.ai/v1", Models: []string{"moonshot-v1-8k", "moonshot-v1-32k", "kimi-k2"}},
	{Name: "Groq", BaseURL: "https://api.groq.com/openai/v1", Models: []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant"}},
	{Name: "Ollama", BaseURL: "http://localhost:11434/v1", Models: []string{"qwen3:8b", "deepseek-r1:7b"}},
}

// FindProvider 按 BaseURL 匹配内置厂商（URL 比较忽略末尾斜杠差异）。
// 返回厂商、下标与是否命中；未命中（如自建中转网关）由设置窗口走 Custom 分支。
func FindProvider(baseURL string) (Provider, int, bool) {
	u := strings.TrimRight(baseURL, "/")
	for i, p := range Providers {
		if strings.TrimRight(p.BaseURL, "/") == u {
			return p, i, true
		}
	}
	return Provider{}, -1, false
}

// IsBuiltin 判断配置是否使用内置 LLM（ByteDP）：BaseURL 精确命中内置项；
// 或未命中任何厂商且存在内置登录态（BytedpRefreshToken 非空）——远程/自建
// 内置网关场景（计划假设：内置网关地址可在 config.json 改 BaseURL）。
// 命中其他厂商（DeepSeek 等）时优先按厂商解析，不因残留登录态误判。
func IsBuiltin(cfg *Config) bool {
	_, idx, ok := FindProvider(cfg.BaseURL)
	if ok {
		return idx == 0
	}
	return cfg.BytedpRefreshToken != ""
}

// currentPath 是本次 Load 实际使用的配置文件路径（Save 写回同一路径）。
var currentPath string

// Path 返回当前生效的配置文件路径（若尚未 Load 则返回候选路径）。
func Path() string {
	if currentPath != "" {
		return currentPath
	}
	p, _ := locateExisting()
	if p != "" {
		return p
	}
	return defaultPath()
}

// locateExisting 按优先级返回已存在的配置文件路径，都不存在返回空。
func locateExisting() (string, error) {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "config.json"))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "config.json"))
	}
	candidates = append(candidates, defaultPath())
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", nil
}

// defaultPath 返回家目录兜底路径。
func defaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.json"
	}
	return filepath.Join(home, ".elfin-grammar", "config.json")
}

// Load 加载配置：按多级路径查找，缺失字段补默认值并自动落盘。
func Load() (*Config, error) {
	cfg := Default()

	path, err := locateExisting()
	if err != nil {
		return nil, err
	}
	if path == "" {
		path = defaultPath()
	}
	currentPath = path

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		// 文件不存在：使用默认配置并落盘。
		if err := Save(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	// 默认值补齐：关键字段为空时回填默认。
	defaults := Default()
	// 布尔开关的零值（false）也是合法取值，无法用「== 0」判断缺省：
	// 按原始 JSON 键是否存在决定是否回填默认（旧配置缺省 → 打字/选中开启、剪贴板关闭）。
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) == nil {
		if _, ok := raw["clipboard_monitor"]; !ok {
			cfg.ClipboardMonitor = defaults.ClipboardMonitor
		}
		if _, ok := raw["typed_check"]; !ok {
			cfg.TypedCheck = defaults.TypedCheck
		}
		if _, ok := raw["selection_check"]; !ok {
			cfg.SelectionCheck = defaults.SelectionCheck
		}
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaults.BaseURL
	}
	if cfg.Model == "" {
		cfg.Model = defaults.Model
	}
	if cfg.MaxWords == 0 {
		cfg.MaxWords = defaults.MaxWords
	}
	if cfg.MaxChars == 0 {
		cfg.MaxChars = defaults.MaxChars
	}
	if cfg.WebMaxWords == 0 {
		cfg.WebMaxWords = defaults.WebMaxWords
	}
	if cfg.WebMaxChars == 0 {
		cfg.WebMaxChars = defaults.WebMaxChars
	}
	if cfg.ClipboardMaxWords == 0 {
		cfg.ClipboardMaxWords = defaults.ClipboardMaxWords
	}
	if cfg.ClipboardMaxChars == 0 {
		cfg.ClipboardMaxChars = defaults.ClipboardMaxChars
	}
	if cfg.ClipboardLangRatio == 0 {
		cfg.ClipboardLangRatio = defaults.ClipboardLangRatio
	}
	if cfg.DebounceMS == 0 {
		cfg.DebounceMS = defaults.DebounceMS
	}
	if cfg.SelectDebounceMS == 0 {
		cfg.SelectDebounceMS = defaults.SelectDebounceMS
	}
	if cfg.BubbleShowMS == 0 {
		cfg.BubbleShowMS = defaults.BubbleShowMS
	}
	if cfg.PollIdleMS == 0 {
		cfg.PollIdleMS = defaults.PollIdleMS
	}
	if cfg.MinKnownRatio == 0 {
		cfg.MinKnownRatio = defaults.MinKnownRatio
	}
	if cfg.MinLangRatio == 0 {
		cfg.MinLangRatio = defaults.MinLangRatio
	}
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("DEEPSEEK_API_KEY")
	}
	return cfg, nil
}

// Save 将配置写入 currentPath（默认 ~/.elfin-grammar/config.json）。
func Save(cfg *Config) error {
	path := currentPath
	if path == "" {
		path = defaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
