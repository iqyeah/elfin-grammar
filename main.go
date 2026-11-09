// elfin-grammar：实时读取焦点输入框内容，纠正英文短句语法并以气泡展示。
//
// 用法：
//
//	elfin-grammar            # 常驻运行（无 api_key 时自动打开设置窗口）
//	elfin-grammar --config   # 仅打开设置窗口配置 LLM，保存后退出
//
// 运行中按快捷键可随时打开设置窗口（macOS=Cmd+Shift+,，Windows=Ctrl+Shift+,），
// 保存后热生效（无需重启）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"elfin-grammar/internal/appkit"
	"elfin-grammar/internal/ax"
	"elfin-grammar/internal/bubble"
	"elfin-grammar/internal/config"
	"elfin-grammar/internal/langdet"
	"elfin-grammar/internal/llm"
	"elfin-grammar/internal/settings"
)

// triggerMode 是触发方式：打字句末标点、选中文本或剪贴板复制。
type triggerMode int

const (
	triggerTyped triggerMode = iota
	triggerSelection
	triggerClipboard
)

// correction 是一次待纠正/已纠正的句子。
type correction struct {
	original    string
	corrected   string // 英文行 LLM 结果（en=纠正句，zh=中译英）
	chinese     string // 中文行结果（en=纠正句的中文翻译，zh=原文回显）
	app         string
	rect        ax.Rect
	mode        triggerMode
	selRange    ax.Range          // 选中触发时记录的选中范围（Apply 替换边界依据）
	web         bool              // 来自网页正文选中（AXWebArea），不可 AX 写回，Apply 降级为复制
	clipboard   bool              // 来自剪贴板触发，无输入框锚点不可 AX 写回，行内按钮降级为复制
	lang        string            // "zh"=中文场景，其他=英文场景
	tabCache    map[string]string // 各 tab LLM 结果缓存（仅 analyze 懒加载）
	tabState    map[string]int    // 0 未加载 / 1 加载中 / 2 就绪
	uncorrected bool              // 英文场景 LLM 返回与原文相同（无语法问题），气泡绿色展示
	err         error             // LLM 失败回传（气泡展示错误文案）
}

// bubbleState 是当前气泡会话的状态（主循环唯一访问）。
type bubbleState struct {
	cur          correction
	curTextEn    string    // 气泡英文行当前显示内容（Copy/Apply 操作对象）
	curTextZh    string    // 气泡中文行当前显示内容（Analyze/错误场景为空）
	curTab       int       // 当前选中 tab（bubble.Tab*）
	loadingTab   string    // 非空=懒加载请求中的 tab 名（冻结自动隐藏）
	loadingStart time.Time // 懒加载开始时间（兜底超时）
	hideAt       time.Time // 自动隐藏时刻
}

// tabNames 是 tab 标识到缓存键的映射（Translate 已合并进 Correct）。
var tabNames = map[int]string{
	bubble.TabCorrect: "correct",
	bubble.TabAnalyze: "analyze",
}

// guideURL 在线用户手册地址（状态栏/托盘菜单 User Guide 打开）。
const guideURL = "https://b.taobed.com/elfin-grammar/user_guide.html"

// actionKind 是气泡按钮触发的 LLM 请求类型（目前仅 Analyze 懒加载）。
type actionKind int

const (
	actionAnalyze actionKind = iota
)

// llmAction 是一次按钮触发的 LLM 请求（分析）。
type llmAction struct {
	kind actionKind
	text string // 请求原文；worker 回传时复用为结果文本
	src  string // 请求原文副本（回传时校验会话，旧会话结果丢弃）
	err  error  // 失败回传（主循环据此解除加载态）
}

func main() {
	// 日志落盘：GUI 双击启动时 stdout/stderr 无终端可写，panic 堆栈会丢失。
	// redirectLogToFile 由平台文件实现（darwin 用 dup2 重定向 fd 1/2，
	// Go runtime panic 输出固定走 fd 2，仅替换 os.Stderr 变量无效），
	// 追加模式不覆盖历史日志。
	redirectLogToFile(logFilePath())

	// 主线程锁定：Accessibility API 与 AppKit 调用都必须在此 goroutine 进行。
	runtime.LockOSThread()

	configOnly := flag.Bool("config", false, "仅打开设置窗口")
	flag.Parse()

	appkit.InitApplication()

	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	// 首次启动免责声明：未同意前不进入任何模式（含 --config），
	// 同意后写入配置，之后不再弹出；点「退出」则直接关闭。
	if !cfg.DisclaimerAccepted {
		const disclaimerTitle = "免责声明"
		const disclaimerText = "本软件免费提供，按“现状”提供，不附带任何明示或默示的担保。" +
			"开发者不保证软件无错误或满足你的特定需求。" +
			"在适用法律允许的最大范围内，开发者不对因使用本软件而产生的任何直接或间接损失承担责任，使用风险由你自行承担。"
		if !appkit.ConfirmDialog(disclaimerTitle, disclaimerText, "同意并继续", "退出") {
			fmt.Println("未同意免责声明，程序退出。")
			return
		}
		cfg.DisclaimerAccepted = true
		if err := config.Save(cfg); err != nil {
			log.Printf("保存免责声明同意状态失败: %v", err)
		}
	}

	// --config 模式：仅打开设置窗口，保存后退出。
	if *configOnly {
		runConfigMode(cfg)
		return
	}

	// 权限：未授权时弹系统窗提示（不阻塞启动），主循环轮询检测，授权后自动恢复工作。
	bubble.Init()
	defer bubble.Hide()

	// LLM 客户端热更新：设置窗口保存后原子替换，worker 每次取最新。
	// 内置 LLM（ByteDP）命中时构造 GrammarClient（未登录时为错误态客户端，
	// 请求返回 ErrLoginExpired），其余厂商构造 OpenAI 兼容 Client。
	var clientPtr atomic.Pointer[llm.Completer]
	newClient := func() llm.Completer {
		if config.IsBuiltin(cfg) {
			return llm.NewGrammarClient(cfg, cfg.ProxyURL)
		}
		return llm.New(cfg.APIKey, cfg.BaseURL, cfg.Model, cfg.ProxyURL)
	}
	// atomic.Pointer[T] 的 Store 接收 *T：接口实例取地址存入，Load 后解引用。
	storeClient := func(c llm.Completer) { clientPtr.Store(&c) }
	storeClient(newClient())
	// 启动自动登录：存在 refresh token 时后台静默续签获取 access_token，
	// 使首个触发请求无需等待 refresh 往返。
	if gc, ok := (*clientPtr.Load()).(*llm.GrammarClient); ok && cfg.BytedpRefreshToken != "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := gc.Warmup(ctx); err != nil {
				log.Println("内置 LLM 自动登录:", err)
			} else {
				fmt.Println("内置 LLM 已自动登录")
			}
		}()
	}

	rules := langdet.Rules{
		MaxWords:      cfg.MaxWords,
		MaxChars:      cfg.MaxChars,
		MinWords:      2,
		MinKnownRatio: cfg.MinKnownRatio,
		MinLangRatio:  cfg.MinLangRatio,
	}
	// 选中触发规则：一个词即可；不查标点，英文占比达标走纠正、否则按中文翻译。
	selRules := rules
	selRules.MinWords = 1
	// 网页选中触发规则：网页正文常为长段落，长度与词数阈值单独放宽。
	webRules := selRules
	webRules.MaxChars = cfg.WebMaxChars
	webRules.MaxWords = cfg.WebMaxWords
	// 剪贴板触发规则：与选中触发一致一个词即可（中文无空格按单 token 计，
	// MinWords=2 会把整段中文判为 1 词拒掉）；复制内容常为整段，长度与词数单独放宽。
	clipRules := langdet.Rules{
		MaxWords:     cfg.ClipboardMaxWords,
		MaxChars:     cfg.ClipboardMaxChars,
		MinWords:     1,
		MinLangRatio: cfg.ClipboardLangRatio,
	}
	debounce := time.Duration(cfg.DebounceMS) * time.Millisecond

	results := make(chan correction, 16)
	jobs := make(chan correction, 16)
	go llmWorker(&clientPtr, jobs, results)

	actionJobs := make(chan llmAction, 8)
	actionResults := make(chan llmAction, 8)
	go llmActionWorker(&clientPtr, actionJobs, actionResults)

	// 内置 LLM 登录结果回传（后台 goroutine → 主线程落地）。
	loginResults := make(chan loginResult, 1)

	seen := map[string]bool{}
	var lastValue string
	var lastChange time.Time
	var pending bool
	var lastSel string
	var lastSelChange time.Time
	var selPending bool
	var lastSkip ax.SkipReason
	// 剪贴板监视状态：lastClipCount 记录基线 changeCount，lastClipPoll 限频。
	var lastClipCount = appkit.ClipboardChangeCount()
	var lastClipPoll time.Time
	const clipPollEvery = 500 * time.Millisecond
	bs := bubbleState{curTab: bubble.TabCorrect}

	// 活动感知轮询：有输入活动期间快轮询（fastPoll），
	// 空闲超过 idleAfter 降为慢轮询（cfg.PollIdleMS），降低 AX IPC 次数。
	const fastPoll = 300 * time.Millisecond
	const idleAfter = 5 * time.Second
	idlePoll := time.Duration(cfg.PollIdleMS) * time.Millisecond
	var lastActivity time.Time
	var lastPollAt time.Time

	// 单一 ticker：常规 100ms；淡出动画期间提升到 50ms 保持帧平滑。
	const tickSlow = 100 * time.Millisecond
	const tickFast = 50 * time.Millisecond
	ticker := time.NewTicker(tickSlow)
	defer ticker.Stop()

	// 热键提示文案平台化：macOS 用 Cmd，Windows 用 Ctrl。
	hotkeyHint := "Cmd+Shift+,"
	if runtime.GOOS == "windows" {
		hotkeyHint = "Ctrl+Shift+,"
	}
	if !appkit.RegisterHotkey() {
		fmt.Println("提示：全局快捷键注册失败，可用 --config 打开设置。")
	} else {
		fmt.Printf("按 %s 打开设置窗口。\n", hotkeyHint)
	}
	if !appkit.InitStatusBar() {
		fmt.Printf("提示：状态栏图标创建失败，可用 %s 打开设置。\n", hotkeyHint)
	}
	if !appkit.InitInputMonitor() {
		fmt.Println("提示：输入活动监控安装失败（按键/点击不会淡出气泡）。")
	}

	// 辅助功能授权：未授权时弹系统授权窗，主循环每 tick 轮询检测；
	// 授权后自动恢复焦点轮询，30s 未授权则重弹系统窗提醒。
	trusted := ax.Trusted()
	var lastTrustPrompt time.Time
	if !trusted {
		fmt.Println("需要「辅助功能」权限：请在弹出的系统设置中勾选 ElfinGrammar，")
		fmt.Println("路径：系统设置 → 隐私与安全性 → 辅助功能。授权后自动生效。")
		ax.PromptTrust()
		lastTrustPrompt = time.Now()
	}

	if needSettings(cfg) {
		fmt.Println("请先配置 API Key，已自动打开设置窗口…")
		settings.Show(cfg)
	}
	if config.IsBuiltin(cfg) && cfg.BytedpRefreshToken == "" {
		// 未登录可匿名试用(服务端按设备累计限 10 次,超出自动弹窗引导登录)。
		fmt.Println("未登录内置 LLM：可匿名试用 10 次，超出后请在设置中登录。")
	}
	// 启动提示直接显示配置值，避免硬编码秒数。
	fmt.Printf("elfin-grammar 已启动：选中英文/中文（输入框/网页正文）并保持 %g 秒自动纠正或翻译，输入英文短句（以 . ! ? 结尾）自动纠正。Ctrl+C 退出。\n",
		(time.Duration(cfg.SelectDebounceMS) * time.Millisecond).Seconds())
	fmt.Printf("配置文件: %s\n", config.Path())
	fmt.Printf("功能开关：剪贴板监控=%v，打字检测=%v，选中检测=%v\n",
		cfg.ClipboardMonitor, cfg.TypedCheck, cfg.SelectionCheck)

	for {
		select {
		case c := <-results:
			if c.err != nil {
				// 失败也弹气泡：按错误类型展示分流文案（余额/限流/登录过期/试用用完）。
				log.Println("LLM:", c.err)
				bs.cur = c
				bs.curTextEn = llmErrText(c.err)
				bs.curTextZh = ""
				bs.curTab = bubble.TabCorrect
				bs.loadingTab = ""
				bubble.Show(bs.curTextEn, bs.curTextZh, c.rect, tabOptsFor(&bs), false, nil)
				bs.hideAt = time.Now().Add(time.Duration(cfg.BubbleShowMS) * time.Millisecond)
				if errors.Is(c.err, llm.ErrTrialExhausted) {
					promptLogin(cfg) // 试用次数用完：自动打开设置窗口引导登录
				}
				continue
			}
			fmt.Printf("[%s] %s\n  -> %s\n", c.app, c.original, c.corrected)
			// Correct tab 双行由一次 Both 调用同时就绪；仅 analyze 懒加载。
			c.tabCache = map[string]string{}
			c.tabState = map[string]int{}
			c.tabCache["correct"] = c.corrected
			c.tabState["correct"] = 2
			bs.cur = c
			bs.curTextEn = c.corrected
			bs.curTextZh = c.chinese
			bs.curTab = bubble.TabCorrect
			bs.loadingTab = ""
			// 无纠正结果（与原文相同）照常弹出，绿色文字让用户感知“无语法问题”。
			diffs := correctDiffs(&bs)
			fmt.Printf("[correct] original=%q en=%q zh=%q green=%v diffs=%v\n",
				c.original, bs.curTextEn, bs.curTextZh, c.uncorrected, diffs)
			bubble.Show(bs.curTextEn, bs.curTextZh, c.rect, tabOptsFor(&bs), c.uncorrected, diffs)
			bs.hideAt = time.Now().Add(time.Duration(cfg.BubbleShowMS) * time.Millisecond)

		case a := <-actionResults:
			if a.src != bs.cur.original {
				continue // 会话已切换（新气泡弹出），旧结果丢弃
			}
			name := "analyze"
			if a.err != nil {
				log.Println("LLM:", a.err)
				bs.cur.tabState[name] = 0 // 失败复位：下次切换重试
				if bs.loadingTab == name {
					bs.loadingTab = ""
					bs.hideAt = time.Now().Add(time.Duration(cfg.BubbleShowMS) * time.Millisecond)
				}
				if bubble.Visible() && bs.curTab == bubble.TabAnalyze {
					bs.curTextEn = llmErrText(a.err)
					bs.curTextZh = ""
					bubble.Show(bs.curTextEn, bs.curTextZh, bs.cur.rect, tabOptsFor(&bs), false, nil)
				}
				if errors.Is(a.err, llm.ErrTrialExhausted) {
					promptLogin(cfg) // 试用次数用完：自动打开设置窗口引导登录
				}
				continue
			}
			bs.cur.tabCache[name] = a.text
			bs.cur.tabState[name] = 2
			if bs.loadingTab == name {
				bs.loadingTab = ""
			}
			if bubble.Visible() && bs.curTab == bubble.TabAnalyze {
				bs.curTextEn = a.text
				bs.curTextZh = ""
				bubble.Show(bs.curTextEn, bs.curTextZh, bs.cur.rect, tabOptsFor(&bs), false, nil)
				bs.hideAt = time.Now().Add(time.Duration(cfg.BubbleShowMS) * time.Millisecond)
			}
			// 气泡已隐藏：仅写缓存，不重新弹出

		case r := <-loginResults:
			applyLoginResult(cfg, r, func() { storeClient(newClient()) })

		case <-ticker.C:
			now := time.Now()
			pumpRunLoop() // 泵 CFRunLoop：气泡显示/设置窗口事件依赖它

			// 消费输入活动：任意按键/鼠标点击记录活动时间（自适应轮询依据）并使气泡淡出。
			// 鼠标位于气泡内时不淡出（用户正在查看/操作气泡；Apply 等关闭行为由按钮事件自身处理）。
			if appkit.ConsumeInputActivity() {
				lastActivity = now
				if bubble.Visible() && !bubble.MouseInside() {
					bubble.StartFadeOut()
				}
			}

			// 自动隐藏：悬停中不隐藏，等鼠标离开气泡后淡出；懒加载请求中冻结。
			if !bs.hideAt.IsZero() && now.After(bs.hideAt) && bs.loadingTab == "" {
				bs.hideAt = time.Time{}
				if !bubble.MouseInside() {
					bubble.StartFadeOut()
				}
			}

			// 懒加载兜底：超过 15s 未回传强制解除冻结，迟到结果仅缓存。
			if bs.loadingTab != "" && time.Since(bs.loadingStart) > 15*time.Second {
				bs.cur.tabState[bs.loadingTab] = 0
				bs.loadingTab = ""
				bs.hideAt = now.Add(time.Duration(cfg.BubbleShowMS) * time.Millisecond)
			}

			// 授权轮询：未授权时每 30s 重弹系统授权窗，授权后自动恢复工作。
			if !trusted {
				if ax.Trusted() {
					trusted = true
					fmt.Println("辅助功能权限已授权，开始工作。")
				} else if time.Since(lastTrustPrompt) > 30*time.Second {
					lastTrustPrompt = now
					ax.PromptTrust()
				}
			}

			// 剪贴板监视：不依赖辅助功能权限，changeCount 变化即读取判定。
			// 开关关闭时仅同步基线（重新开启不补触发关闭期间的历史复制）。
			if now.Sub(lastClipPoll) >= clipPollEvery {
				lastClipPoll = now
				if cfg.ClipboardMonitor {
					pollClipboard(&lastClipCount, clipRules, jobs)
				} else {
					lastClipCount = appkit.ClipboardChangeCount()
				}
			}

			// 焦点轮询：输入活动期间快轮询，空闲超过 idleAfter 降为慢轮询。
			pollEvery := fastPoll
			if time.Since(lastActivity) > idleAfter {
				pollEvery = idlePoll
			}
			// 打字/选中开关全部关闭时跳过 AX 读取（省 IPC）；开关热生效。
			if trusted && (cfg.TypedCheck || cfg.SelectionCheck) && now.Sub(lastPollAt) >= pollEvery {
				lastPollAt = now
				poll(ax.ReadFocusedLight(), rules, selRules, webRules, debounce,
					time.Duration(cfg.SelectDebounceMS)*time.Millisecond, seen, jobs,
					&lastValue, &lastChange, &pending,
					&lastSel, &lastSelChange, &selPending, &lastSkip,
					cfg.TypedCheck, cfg.SelectionCheck)
			}

			// 推进淡出动画；淡出完成后隐藏。
			if bubble.FadeStep(now) {
				bubble.Hide()
			}
			// 鼠标进出：进入取消淡出并暂停自动隐藏，离开开始淡出（懒加载请求中不淡出）。
			me := bubble.ConsumeMouseEvents()
			if me&bubble.MouseEnter != 0 {
				bubble.CancelFade()
				bs.hideAt = time.Time{} // 悬停期间不自动隐藏
			}
			if me&bubble.MouseLeave != 0 {
				if bs.loadingTab == "" {
					bubble.StartFadeOut()
				}
			}

			flags := appkit.PollEvents()
			if flags&appkit.EventStatusExit != 0 {
				fmt.Println("状态栏菜单：退出。")
				return
			}
			if flags&appkit.EventStatusSettings != 0 {
				if settings.IsShown() {
					settings.Activate() // 窗口已显示：确保其在前台且为 key
				} else {
					fmt.Println("打开设置窗口…")
					settings.Show(cfg)
				}
			}
			if flags&appkit.EventStatusGuide != 0 {
				appkit.OpenURL(guideURL)
			}
			handleUIEvents(cfg, &clientPtr, newClient, loginResults, flags)
			handleBubbleEvents(flags, &bs, actionJobs, cfg)
			// 厂商选择变化兜底联动：下拉框 action 事件未派发时轮询检测下标变化。
			settings.PollProviderSelection()
			// 用户点 X 关闭设置窗口（不经过 Close）时归还激活状态，避免按键被吞。
			settings.RecheckFocus()

			// 动态调频：淡出动画需要 50ms 帧率，否则 100ms。
			if bubble.Fading() {
				ticker.Reset(tickFast)
			} else {
				ticker.Reset(tickSlow)
			}
		}
	}
}

// logFilePath 返回诊断日志路径：~/Library/Logs/ElfinGrammar.log。
// 目录创建失败时退化为临时目录（保证启动不因日志失败而中断）。
func logFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "elfin-grammar.log")
	}
	dir := filepath.Join(home, "Library", "Logs")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "ElfinGrammar.log")
}

// runConfigMode 仅运行设置窗口：保存后落盘并退出，取消（点 X 关闭）也退出。
// 内置 LLM 登录/退出/注册友情捐献链接/二维码切换同样可用（--config 模式验证入口）。
func runConfigMode(cfg *config.Config) {
	settings.Show(cfg)
	fmt.Printf("配置文件: %s\n", config.Path())
	saved := false
	loginResults := make(chan loginResult, 1)
	for {
		pumpRunLoop()
		flags := appkit.PollEvents()
		if flags&appkit.EventProviderChanged != 0 {
			settings.OnProviderChanged()
		}
		if flags&appkit.EventTabBasic != 0 {
			settings.OnTabChanged(true)
		}
		if flags&appkit.EventTabLLM != 0 {
			settings.OnTabChanged(false)
		}
		settings.PollProviderSelection() // 轮询兜底：事件未派发时仍联动 Base URL/模型
		if flags&appkit.EventLogin != 0 {
			startLogin(cfg, loginResults)
		}
		if flags&appkit.EventLogout != 0 {
			handleLogout(cfg, nil, nil)
		}
		if flags&appkit.EventPortal != 0 {
			appkit.OpenURL(settings.PortalURL())
		}
		if flags&appkit.EventQR != 0 {
			settings.OnQR()
		}
		select {
		case r := <-loginResults:
			applyLoginResult(cfg, r, nil)
		default:
		}
		if flags&appkit.EventSave != 0 {
			settings.Apply(cfg)
			if err := config.Save(cfg); err != nil {
				log.Printf("保存失败: %v", err)
			} else {
				fmt.Printf("已保存: %s\n", config.Path())
				saved = true
			}
			settings.Close()
		}
		if flags&appkit.EventCancel != 0 {
			settings.Close()
		}
		if !settings.IsShown() {
			// 窗口已被关闭（Save 或 Cancel 或用户点 X）。
			if saved {
				fmt.Println("配置已更新，重新运行 elfin-grammar 生效。")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// loginResult 是后台登录 goroutine 回传的结果（主线程落地）。
type loginResult struct {
	email   string
	access  string
	refresh string
	balance int
	err     error
}

// needSettings 是否需要弹设置窗口:内置模式未登录可匿名试用
// (服务端按设备累计限 10 次,超限时自动弹窗引导登录,不启动即强制弹窗);
// 非内置模式未配置 API Key 则必须弹窗。
func needSettings(cfg *config.Config) bool {
	if config.IsBuiltin(cfg) {
		return false
	}
	return cfg.APIKey == ""
}

// llmErrText 把 LLM 错误映射为气泡展示文案。
func llmErrText(err error) string {
	switch {
	case errors.Is(err, llm.ErrInsufficientBalance):
		return "余额不足，请到设置中友情捐献"
	case errors.Is(err, llm.ErrRateLimited):
		return "调用太频繁，请稍后重试"
	case errors.Is(err, llm.ErrLoginExpired):
		return "登录已过期，请重新登录"
	case errors.Is(err, llm.ErrTrialExhausted):
		return "试用次数已用完，请登录后继续使用"
	default:
		return "加载失败，请重试"
	}
}

// promptLogin 引导登录:打开设置窗口并切到 LLM 页签(登录区)。
// 匿名试用次数用完(ErrTrialExhausted)时自动调用,代替用户手动打开设置。
func promptLogin(cfg *config.Config) {
	if settings.IsShown() {
		settings.Activate() // 窗口已显示:带到前台并强制切到 LLM 页签
	} else {
		fmt.Println("试用次数已用完，自动打开设置窗口登录…")
		settings.Show(cfg)
	}
	settings.OnTabChanged(false)
}

// loginErrText 把登录错误映射为提示行文案：错误格式为
// "llm: 登录失败(CODE): message"，直接展示服务端 message；其余展示通用提示。
func loginErrText(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, "): "); i >= 0 {
		return msg[i+3:]
	}
	return "登录失败，请检查网络或稍后重试"
}

// startLogin 读取登录表单并后台调 llm.Login（结果回传 loginResults 由主线程落地）。
// 表单为空时直接提示，不发请求。
func startLogin(cfg *config.Config, results chan<- loginResult) {
	email, password := settings.LoginForm()
	if email == "" || password == "" {
		settings.SetHint("请输入邮箱和密码")
		return
	}
	settings.SetHint("登录中…")
	baseURL := cfg.BaseURL
	deviceID := llm.EnsureDeviceID(cfg)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		access, refresh, err := llm.Login(ctx, baseURL, email, password, deviceID)
		cancel()
		r := loginResult{email: email, access: access, refresh: refresh, err: err}
		if err == nil {
			// 余额尽力查询：失败不阻断登录（余额显示 0）。
			ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
			r.balance, _ = llm.Balance(ctx2, baseURL, access)
			cancel2()
		}
		results <- r
	}()
}

// applyLoginResult 登录结果落地：成功更新 cfg 登录字段并落盘、热替换客户端、
// 刷新窗口显示；失败在提示行展示服务端文案。replace 为 nil 时跳过热替换
// （--config 模式无 worker）。
func applyLoginResult(cfg *config.Config, r loginResult, replace func()) {
	if r.err != nil {
		log.Println("内置 LLM 登录:", r.err)
		settings.SetHint(loginErrText(r.err))
		return
	}
	cfg.BytedpEmail = r.email
	cfg.BytedpRefreshToken = r.refresh
	if err := config.Save(cfg); err != nil {
		log.Printf("保存失败: %v", err)
	}
	if replace != nil {
		replace()
	}
	settings.SetLoggedIn(r.email, r.balance)
	settings.SetHint("登录成功")
	fmt.Println("内置 LLM 登录成功:", r.email)
}

// handleLogout 退出登录：尽力调服务端注销，清 cfg 登录字段并热替换客户端。
// clientPtr 为 nil 时跳过热替换（--config 模式无 worker）。
func handleLogout(cfg *config.Config, clientPtr *atomic.Pointer[llm.Completer], newClient func() llm.Completer) {
	var token string
	var gc *llm.GrammarClient
	if clientPtr != nil {
		gc, _ = (*clientPtr.Load()).(*llm.GrammarClient)
	}
	if gc != nil {
		token = gc.AccessToken()
	}
	if token != "" {
		baseURL := cfg.BaseURL
		go func() { // 尽力注销：不阻塞主线程
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := llm.Logout(ctx, baseURL, token); err != nil {
				log.Println("内置 LLM 注销:", err)
			}
		}()
	}
	if gc != nil {
		gc.ClearLogin() // 清 access 内存 + refresh/email 持久化字段并落盘
	} else {
		cfg.BytedpEmail = ""
		cfg.BytedpRefreshToken = ""
		_ = config.Save(cfg)
	}
	if clientPtr != nil {
		c := newClient()
		clientPtr.Store(&c)
	}
	settings.SetLoggedOut()
	settings.SetHint("已退出登录")
	fmt.Println("已退出内置 LLM 登录")
}

// handleUIEvents 分发设置窗口事件：快捷键打开、保存热更新、取消关闭、
// 内置 LLM 登录/退出/注册友情捐献链接/二维码切换。
func handleUIEvents(cfg *config.Config, clientPtr *atomic.Pointer[llm.Completer], newClient func() llm.Completer, loginResults chan<- loginResult, flags appkit.EventFlags) {
	if flags&appkit.EventHotkey != 0 {
		if settings.IsShown() {
			settings.Activate() // 窗口已显示：确保其在前台且为 key
		} else {
			fmt.Println("打开设置窗口…")
			settings.Show(cfg)
		}
	}
	if flags&appkit.EventProviderChanged != 0 {
		settings.OnProviderChanged()
	}
	if flags&appkit.EventTabBasic != 0 {
		settings.OnTabChanged(true)
	}
	if flags&appkit.EventTabLLM != 0 {
		settings.OnTabChanged(false)
	}
	if flags&appkit.EventSave != 0 {
		settings.Apply(cfg)
		if err := config.Save(cfg); err != nil {
			log.Printf("保存失败: %v", err)
		} else {
			fmt.Printf("配置已保存并热生效: %s\n", config.Path())
			c := newClient()
			clientPtr.Store(&c)
		}
		settings.Close()
	}
	if flags&appkit.EventCancel != 0 {
		settings.Close()
	}
	if flags&appkit.EventLogin != 0 {
		startLogin(cfg, loginResults)
	}
	if flags&appkit.EventLogout != 0 {
		handleLogout(cfg, clientPtr, newClient)
	}
	if flags&appkit.EventPortal != 0 {
		appkit.OpenURL(settings.PortalURL())
	}
	if flags&appkit.EventQR != 0 {
		settings.OnQR()
	}
}

// poll 轮询一次焦点输入框：跳过不支持的场景，判定后投递待纠正句。
// 打字与选中两条触发路径独立：文本变化 debounce 后走句末标点判定，
// 选中稳定 selDebounce 后走无标点判定（英文占比达标走纠正、否则按中文翻译）；
// 网页正文（IsWeb）选中用 webRules（放宽长度/词数）。
// typedOn/selOn 为设置窗口开关：关闭的路径不判定、不投递，且同步基线/清空
// 状态（重新开启后需重新输入/重新选中才会触发，不补触发旧内容）。
// 输入为轻量读取结果（仅文本/选中）；判定通过时才补读重量上下文（app/rect）。
func poll(res ax.LightResult, rules, selRules, webRules langdet.Rules, debounce, selDebounce time.Duration,
	seen map[string]bool, jobs chan<- correction,
	lastValue *string, lastChange *time.Time, pending *bool,
	lastSel *string, lastSelChange *time.Time, selPending *bool, lastSkip *ax.SkipReason,
	typedOn, selOn bool) {

	if res.Skip != ax.SkipNone {
		if res.Skip != *lastSkip {
			fmt.Println("跳过:", res.Skip)
			*lastSkip = res.Skip
		}
		*pending = false
		return
	}
	*lastSkip = ax.SkipNone

	// 选中触发：开关关闭时清空状态（重新开启后需重新稳定选中才判定）。
	if res.Selection != "" {
		if !selOn {
			*lastSel = ""
			*selPending = false
		} else if res.Selection != *lastSel {
			*lastSel = res.Selection
			*lastSelChange = time.Now()
			*selPending = true
			// 诊断：打印 AX 层实际捕获到的选中文本（未截断前），用于定位“选不中”问题。
			sel := res.Selection
			show := sel
			if r := []rune(show); len(r) > 80 {
				show = string(r[:80]) + "…"
			}
			fmt.Printf("[dbg] 选中捕获: role=%q isWeb=%v bytes=%d selection=%q\n", res.Role, res.IsWeb, len(sel), show)
		} else if *selPending && time.Since(*lastSelChange) >= selDebounce {
			*selPending = false
			tr := selRules
			if res.IsWeb {
				tr = webRules
			}
			if judgeText(res.Selection, false, tr, seen, triggerSelection) {
				ctx := ax.ReadFocusedContext() // 判定通过才补读展示上下文（低频）
				// 分流：英文字母占比达标走语法纠正；否则一律按中文走中译英（含中英混合）。
				lang := "zh"
				if langdet.IsLatin(res.Selection, tr.MinLangRatio) {
					lang = ""
				}
				fmt.Printf("[dbg] 选中触发: lang=%q app=%q\n", lang, ctx.App)
				submit(correction{original: res.Selection, app: ctx.App, rect: ctx.Rect,
					mode: triggerSelection, selRange: ctx.SelRange, web: ctx.IsWeb, lang: lang}, jobs)
			} else {
				fmt.Printf("[dbg] 选中判定拒绝: isWeb=%v len=%d\n", res.IsWeb, len(res.Selection))
			}
		}
	} else if *lastSel != "" {
		*lastSel = ""
		*selPending = false
	}

	// 打字触发：开关关闭时同步基线并复位（重新开启后不补触发旧文本）。
	if !typedOn {
		*lastValue = res.Text
		*pending = false
		return
	}

	if res.Text == *lastValue {
		// 值未变：debounce 窗口结束后且尚未判定，才做一次判定。
		if *pending && time.Since(*lastChange) >= debounce {
			*pending = false
			if sentence, ok := judge(res, rules, seen); ok {
				ctx := ax.ReadFocusedContext()
				submit(correction{original: sentence, app: ctx.App, rect: ctx.Rect,
					mode: triggerTyped}, jobs)
			}
		}
		return
	}
	// 值变化：重新进入 debounce 等待。
	*lastValue = res.Text
	*lastChange = time.Now()
	*pending = true
}

// judge 提取当前句子并按规则判定（要求句末标点），通过返回句子与 true。
func judge(res ax.LightResult, rules langdet.Rules, seen map[string]bool) (string, bool) {
	sentence := langdet.ExtractSentence(res.Text)
	return sentence, judgeText(sentence, true, rules, seen, triggerTyped)
}

// judgeText 判定文本（requireComplete 决定是否要求句末标点），去重后返回是否可投递。
// 去重仅作用于打字触发：防止同一句因值抖动重复请求。选中触发由 poll 的
// selPending 状态机保证每次选中只判定一次，跨次重复选中同一文本仍应再次触发
// （用户期望：重新选择即重新弹出气泡），因此不参与 seen 去重。
// 返回 true 后由调用方补读重量上下文（app/rect）并投递 correction。
func judgeText(text string, requireComplete bool, rules langdet.Rules, seen map[string]bool, mode triggerMode) bool {
	var v langdet.Verdict
	if requireComplete {
		v = langdet.Check(text, rules)
	} else {
		v = langdet.CheckSelection(text, rules)
	}
	if v != langdet.Correct {
		return false
	}
	if mode == triggerTyped {
		h := langdet.Hash(text)
		if seen[h] {
			return false
		}
		seen[h] = true
		if len(seen) > 200 { // 防 map 无限膨胀
			clear(seen)
		}
	}
	return true
}

// pollClipboard 轮询系统剪贴板：changeCount 变化说明发生新复制，读取文本后
// 用 CheckSelection 判定（与选中触发同一套规则），通过则投递纠正。
// 分流与选中触发一致：英文字母占比达标走语法纠正，否则一律按中文走中译英。
// 自身写入（Copy 按钮/Apply 降级复制/粘贴兜底）由 appkit 层拦截，不会回环触发；
// 非文本内容（图片/文件）读取为空，静默跳过。
// 剪贴板触发无输入框锚点，气泡显示在鼠标位置附近。
func pollClipboard(lastCount *int64, rules langdet.Rules, jobs chan<- correction) {
	count := appkit.ClipboardChangeCount()
	if count == *lastCount {
		return
	}
	*lastCount = count
	text := strings.TrimSpace(appkit.ClipboardString())
	if text == "" {
		fmt.Printf("[dbg] 剪贴板变化但读取为空（非文本或自身写入）: count=%d\n", count)
		return
	}
	if langdet.CheckSelection(text, rules) != langdet.Correct {
		fmt.Printf("[dbg] 剪贴板判定拒绝: bytes=%d text=%q\n", len(text), dbgClip(text))
		return
	}
	// 分流：英文字母占比达标走语法纠正；否则一律按中文走中译英（含中英混合）。
	lang := "zh"
	if langdet.IsLatin(text, rules.MinLangRatio) {
		lang = ""
	}
	fmt.Printf("[dbg] 剪贴板触发: lang=%q bytes=%d text=%q\n", lang, len(text), dbgClip(text))
	x, y := ax.MousePos()
	submit(correction{original: text, app: "Clipboard", rect: ax.Rect{X: x, Y: y, W: 1, H: 1},
		mode: triggerClipboard, clipboard: true, lang: lang}, jobs)
}

// dbgClip 截断剪贴板文本用于日志展示（前 80 字符）。
func dbgClip(s string) string {
	if r := []rune(s); len(r) > 80 {
		return string(r[:80]) + "…"
	}
	return s
}

// submit 投递纠正任务，队列满则丢弃（避免阻塞主循环）。
func submit(c correction, jobs chan<- correction) {
	select {
	case jobs <- c:
	default:
	}
}

// llmWorker 串行调用 LLM（OpenAI 兼容或内置 GrammarClient），结果回传主线程展示。
// clientPtr 支持热更新。一次 Both 调用同时返回英文与中文：
// 英文场景 english=纠正句、chinese=中文翻译；中文场景 english=英文翻译、chinese=原文回显。
func llmWorker(clientPtr *atomic.Pointer[llm.Completer], jobs <-chan correction, results chan<- correction) {
	for c := range jobs {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		client := *clientPtr.Load() // 每次取最新客户端（热更新生效点）
		english, chinese, err := client.Both(ctx, c.original)
		cancel()
		if err != nil {
			if errors.Is(err, llm.ErrSuperseded) {
				continue // 被更新请求替换：静默放弃
			}
			c.err = err
			results <- c // 失败回传：气泡展示分流文案（余额/限流/登录过期）
			continue
		}
		// 结果与原文相同也弹出气泡：用户主动触发（选中/复制）时期望每次都有反馈，
		// 无语法问题时绿色展示原文而非静默跳过。
		c.corrected = english
		if c.lang == "zh" {
			c.chinese = c.original // 中文场景：中文行回显原文
		} else {
			c.chinese = chinese
			c.uncorrected = english == c.original
		}
		results <- c
	}
}

// llmActionWorker 串行处理气泡 tab 触发的分析请求，结果回传主线程。
// 失败也回传（带 err）：主循环据此解除加载态，避免气泡冻结。
func llmActionWorker(clientPtr *atomic.Pointer[llm.Completer], jobs <-chan llmAction, results chan<- llmAction) {
	for a := range jobs {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		client := *clientPtr.Load() // 每次取最新客户端（热更新生效点）
		out, err := client.Analyze(ctx, a.text)
		cancel()
		if err != nil {
			if errors.Is(err, llm.ErrSuperseded) {
				continue // 被更新请求替换：静默放弃（15s 兜底会复位加载态）
			}
			a.err = err
			results <- a // 失败回传：解除加载态
			continue
		}
		if out == "" {
			a.err = errors.New("llm: 空响应")
			results <- a
			continue
		}
		a.text = out
		results <- a
	}
}

// tabOptsFor 按语言场景构造气泡 tab 配置与行内按钮标题：
// 打字/选中场景行内按钮为 Apply（AX 写回英文/中文行）；
// 剪贴板场景无输入框锚点，行内按钮为 Copy（点击复制成功后变 Copied，可重复点击）；
// Analyze tab 与错误气泡无行内按钮。
func tabOptsFor(bs *bubbleState) bubble.TabOptions {
	visible := bubble.TabCorrect | bubble.TabAnalyze
	if bs.cur.lang == "zh" {
		visible = bubble.TabCorrect // 中文场景仅 Correct（双语双行）
	}
	opts := bubble.TabOptions{Visible: visible, Active: bs.curTab}
	if bs.cur.err != nil || bs.curTab != bubble.TabCorrect {
		return opts
	}
	if bs.cur.clipboard {
		opts.EnTitle = "Copy"
		opts.ZhTitle = "Copy"
	} else {
		opts.EnTitle = "Apply"
		opts.ZhTitle = "Apply"
	}
	return opts
}

// correctDiffs 计算 Correct tab 展示时的词级差异区间（UTF-16 码元单位）：
// 仅当当前显示内容确为纠正文、且确有语法纠正（非绿色）时返回非空；
// 错误文案、加载占位、分析结果均无差异标记。
func correctDiffs(bs *bubbleState) []bubble.Span {
	if bs.curTab != bubble.TabCorrect || bs.cur.uncorrected {
		return nil
	}
	if bs.curTextEn != bs.cur.corrected { // 错误文案/占位文本：不对比
		return nil
	}
	return bubble.DiffSpans(bs.cur.original, bs.curTextEn)
}

// switchTab 处理 tab 切换：Correct 双行由一次 Both 调用已就绪直接显示；
// Analyze 已缓存直接显示，未缓存发懒加载请求（首次切换才调 LLM）。
func switchTab(bs *bubbleState, tab int, actionJobs chan<- llmAction, cfg *config.Config) {
	name := tabNames[tab]
	bs.curTab = tab
	opts := tabOptsFor(bs)
	bs.hideAt = time.Time{} // 切换即用户交互，冻结自动隐藏
	if tab == bubble.TabCorrect {
		// 双语双行直接显示；分析请求仍在途时解除冻结，迟到结果仅写缓存。
		bs.loadingTab = ""
		bs.curTextEn = bs.cur.tabCache["correct"]
		bs.curTextZh = bs.cur.chinese
		bubble.Show(bs.curTextEn, bs.curTextZh, bs.cur.rect, opts, bs.cur.uncorrected, correctDiffs(bs))
		bs.hideAt = time.Now().Add(time.Duration(cfg.BubbleShowMS) * time.Millisecond)
		return
	}
	switch bs.cur.tabState[name] {
	case 2: // 就绪：直接显示缓存
		bs.curTextEn = bs.cur.tabCache[name]
		bs.curTextZh = ""
		bubble.Show(bs.curTextEn, bs.curTextZh, bs.cur.rect, opts, false, nil)
		bs.hideAt = time.Now().Add(time.Duration(cfg.BubbleShowMS) * time.Millisecond)
	case 1: // 加载中：占位显示
		bs.curTextEn = "加载中…"
		bs.curTextZh = ""
		bubble.Show(bs.curTextEn, bs.curTextZh, bs.cur.rect, opts, false, nil)
	default: // 未加载：发懒加载请求
		bs.cur.tabState[name] = 1
		bs.loadingTab = name
		bs.loadingStart = time.Now()
		bs.curTextEn = "加载中…"
		bs.curTextZh = ""
		bubble.Show(bs.curTextEn, bs.curTextZh, bs.cur.rect, opts, false, nil)
		select {
		case actionJobs <- llmAction{kind: actionAnalyze, text: bs.cur.original, src: bs.cur.original}:
		default: // 队列满：复位，下次切换重试
			bs.cur.tabState[name] = 0
			bs.loadingTab = ""
			bs.curTextEn = "请求队列繁忙，请重试"
			bs.curTextZh = ""
			bubble.Show(bs.curTextEn, bs.curTextZh, bs.cur.rect, opts, false, nil)
		}
	}
}

// handleBubbleEvents 分发气泡事件：tab 切换（懒加载）、行内按钮（Apply 写回/Copy 复制）、
// 底部复制、打开设置。
func handleBubbleEvents(flags appkit.EventFlags, bs *bubbleState,
	actionJobs chan<- llmAction, cfg *config.Config) {

	switch {
	case flags&appkit.EventTabCorrect != 0:
		switchTab(bs, bubble.TabCorrect, actionJobs, cfg)

	case flags&appkit.EventTabAnalyze != 0:
		switchTab(bs, bubble.TabAnalyze, actionJobs, cfg)

	case flags&appkit.EventApplyEn != 0:
		applyRow(bs, 0) // 英文行按钮

	case flags&appkit.EventApplyZh != 0:
		applyRow(bs, 1) // 中文行按钮

	case flags&appkit.EventCopy != 0:
		appkit.CopyToPasteboard(bs.curTextEn)
		fmt.Println("已复制到剪贴板")

	case flags&appkit.EventClose != 0:
		bubble.Hide() // 右上角关闭：收起气泡

	case flags&appkit.EventSettings != 0:
		if settings.IsShown() {
			settings.Activate() // 窗口已显示：确保其在前台且为 key
		} else {
			fmt.Println("打开设置窗口…")
			settings.Show(cfg)
		}
	}
}

// applyRow 处理行内按钮点击（which: 0=英文行，1=中文行）。
// 剪贴板场景：复制对应行文本，按钮变 Copied（允许重复点击，气泡不收起）；
// 其余场景：AX 写回对应行文本（网页/剪贴板无锚点降级为复制）后收起气泡。
func applyRow(bs *bubbleState, which int) {
	text := bs.curTextEn
	if which == 1 {
		text = bs.curTextZh
	}
	if bs.cur.original == "" || text == "" {
		return
	}
	if bs.cur.clipboard {
		appkit.CopyToPasteboard(text)
		bubble.SetRowButtonTitle(which, "Copied")
		fmt.Println("已复制到剪贴板")
		return
	}
	if bs.cur.web {
		// 网页正文无输入框锚点，降级为复制当前结果。
		appkit.CopyToPasteboard(text)
		fmt.Println("网页内容不可写回，当前结果已复制到剪贴板")
		bubble.Hide()
		return
	}
	applied, needPaste := ax.ReplaceFocused(bs.cur.original, text, bs.cur.selRange)
	if needPaste {
		appkit.PasteText(text)
		fmt.Println("已通过粘贴应用（AX 写回失败回退）")
	} else if applied {
		fmt.Println("已应用")
	} else {
		fmt.Println("应用失败：未找到原文（内容可能已变化）")
	}
	bubble.Hide()
}
