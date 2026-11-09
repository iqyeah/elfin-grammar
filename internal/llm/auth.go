// 内置 LLM(ByteDP)认证与账户辅助:Login / Logout / Refresh / Balance。
// 与 GrammarClient 共享 X-Request-Id 生成、错误码解析与请求构造约定。
package llm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"elfin-grammar/internal/config"
)

// 登录时上报的客户端环境(platform=macos)。
const (
	appName    = "elfin-grammar"
	appVersion = "1.0.0"
)

type clientInfo struct {
	Platform   string `json:"platform"`
	AppName    string `json:"app_name"`
	AppVersion string `json:"app_version"`
}

// apiErrBody 是后端统一错误体 {error:{code,message,...}} 的解析壳。
type apiErrBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// decodeAPIError 从响应体解析错误码与文案,解析失败返回空串。
func decodeAPIError(data []byte) (code, message string) {
	var b apiErrBody
	if err := json.Unmarshal(data, &b); err != nil {
		return "", ""
	}
	return b.Error.Code, b.Error.Message
}

// EnsureDeviceID 返回设备 ID:配置中不存在则生成 32 位随机 hex 并落盘。
func EnsureDeviceID(cfg *config.Config) string {
	if cfg.BytedpDeviceID != "" {
		return cfg.BytedpDeviceID
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		cfg.BytedpDeviceID = fmt.Sprintf("%032x", time.Now().UnixNano())
	} else {
		cfg.BytedpDeviceID = hex.EncodeToString(b[:])
	}
	_ = config.Save(cfg)
	return cfg.BytedpDeviceID
}

// Login 登录内置 LLM,返回 (access_token, refresh_token)。
// 失败(如 AUTH_001 密码错误)返回 error,文案取自服务端 message。
func Login(ctx context.Context, baseURL, email, password, deviceID string) (string, string, error) {
	status, data, err := request(ctx, baseURL, http.MethodPost, "/auth/login", map[string]any{
		"email":       email,
		"password":    password,
		"device_id":   deviceID,
		"client_info": clientInfo{Platform: "macos", AppName: appName, AppVersion: appVersion},
		"remember_me": true,
	}, "")
	if err != nil {
		return "", "", err
	}
	if status != http.StatusOK {
		code, msg := decodeAPIError(data)
		return "", "", fmt.Errorf("llm: 登录失败(%s): %s", code, msg)
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", "", err
	}
	return out.AccessToken, out.RefreshToken, nil
}

// Refresh 用 refresh_token 续签双 token。refresh 失效(401)返回 ErrLoginExpired。
func Refresh(ctx context.Context, baseURL, refreshToken string) (string, string, error) {
	status, data, err := request(ctx, baseURL, http.MethodPost, "/auth/refresh",
		map[string]any{"refresh_token": refreshToken}, "")
	if err != nil {
		return "", "", err
	}
	if status == http.StatusUnauthorized {
		return "", "", ErrLoginExpired
	}
	if status != http.StatusOK {
		code, msg := decodeAPIError(data)
		return "", "", fmt.Errorf("llm: 续签失败(%s): %s", code, msg)
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", "", err
	}
	return out.AccessToken, out.RefreshToken, nil
}

// Logout 尽力注销当前会话(access_token 认证)。会话已失效也视为注销完成。
func Logout(ctx context.Context, baseURL, accessToken string) error {
	status, _, err := request(ctx, baseURL, http.MethodPost, "/auth/logout",
		map[string]any{}, accessToken)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && status != http.StatusOK &&
		status != http.StatusUnauthorized {
		return fmt.Errorf("llm: 注销失败(%d)", status)
	}
	return nil
}

// Balance 查询账户可用余额(分)。
func Balance(ctx context.Context, baseURL, accessToken string) (int, error) {
	status, data, err := request(ctx, baseURL, http.MethodGet, "/billing/balance", nil, accessToken)
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		code, msg := decodeAPIError(data)
		return 0, fmt.Errorf("llm: 余额查询失败(%s): %s", code, msg)
	}
	var out struct {
		Available int `json:"available"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return 0, err
	}
	return out.Available, nil
}

// request 发送一次 JSON 请求(自动携带 X-Request-Id;accessToken 非空则带 Bearer)。
func request(
	ctx context.Context, baseURL, method, path string, payload any, accessToken string,
) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(
		ctx, method, strings.TrimRight(baseURL, "/")+path, body,
	)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", newRequestID())
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	hc := &http.Client{Timeout: 15 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("llm: 请求失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, data, nil
}
