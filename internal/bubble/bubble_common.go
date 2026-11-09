// Package bubble 的跨平台共享逻辑：tab 常量、布局常量、按钮 tag 与淡出动画状态机。
// 平台相关实现（darwin 的 NSPanel / windows 的 Win32 弹出窗口）各自提供
// setAlpha（设置整体透明度）与 hideWindow（隐藏窗口）两个钩子。
package bubble

import "time"

// 按钮布局常量（darwin 与 windows 实现共用，保持两平台气泡观感一致）。
const (
	btnPadX = 12.0
	btnPadY = 8.0
	btnRowH = 24.0
	btnGap  = 6.0
	rowBtnW = 54.0 // 行内 Apply/Copy/Copied 按钮宽（需容纳 Copied 文案）
	rowGap2 = 4.0  // 英文行与中文行的行间距
	tabH    = 22.0
	tabGap  = 6.0
	closeW  = 24.0 // 右上角关闭按钮宽（与 tab 行同高摆放）
)

// 气泡按钮 tag（darwin 与 appkit 侧 BTN_TAG_* 一致；windows 侧作为控件 ID）。
const (
	tagCopy       = 2003
	tagSettings   = 2005
	tagApplyEn    = 2006 // 英文行行内按钮（Apply/Copy/Copied 动态）
	tagApplyZh    = 2007 // 中文行行内按钮（Apply/Copy/Copied 动态）
	tagTabCorrect = 5001
	tagTabAnalyze = 5003
	tagClose      = 5004
)

// Tab 是气泡顶部页签的位掩码/标识。Correct 已合并原 Translate（同时展示英文与中文）。
const (
	TabCorrect = 1 << iota
	TabAnalyze
)

// TabOptions 是 Show 的 tab 配置：可见页签位掩码、选中页签与行内按钮标题。
type TabOptions struct {
	Visible int    // 位掩码（TabCorrect|TabAnalyze）；zh 场景仅 TabCorrect
	Active  int    // 当前选中 tab（驱动高亮）
	EnTitle string // 英文行行内按钮标题（空=隐藏；"Apply"/"Copy"/"Copied"）
	ZhTitle string // 中文行行内按钮标题（空=隐藏；"Apply"/"Copy"/"Copied"）
}

// MouseEvent 是气泡鼠标进出事件标志。
type MouseEvent int

const (
	// MouseEnter 鼠标进入气泡区域。
	MouseEnter MouseEvent = 1 << iota
	// MouseLeave 鼠标离开气泡区域。
	MouseLeave
)

// 淡出动画状态（仅主循环访问，勿并发）。
var (
	fadeActive bool
	fadeStart  time.Time
	fadeAlpha  = 1.0
)

// fadeDuration 是淡出动画时长。
const fadeDuration = 500 * time.Millisecond

// setAlpha 与 hideWindow 由各平台实现文件定义（darwin=NSPanel alphaValue/orderOut，
// windows=SetLayeredWindowAttributes/ShowWindow）。

// Hide 隐藏气泡。必须在主线程调用。
func Hide() {
	fadeActive = false
	fadeAlpha = 1
	setAlpha(1.0)
	hideWindow()
}

// StartFadeOut 开始淡出（鼠标离开气泡或停留超时触发）。必须在主线程调用。
func StartFadeOut() {
	if fadeActive {
		return
	}
	fadeActive = true
	fadeStart = time.Now()
	setAlpha(fadeAlpha)
}

// CancelFade 取消淡出并恢复完全不透明。必须在主线程调用。
func CancelFade() {
	fadeActive = false
	fadeAlpha = 1
	setAlpha(1.0)
}

// FadeStep 推进一帧淡出动画；返回 true 表示淡出完成（调用方应 Hide）。
// 必须在主线程调用。
func FadeStep(now time.Time) bool {
	if !fadeActive {
		return false
	}
	t := now.Sub(fadeStart).Seconds() / fadeDuration.Seconds()
	if t >= 1 {
		return true
	}
	fadeAlpha = 1 - t
	setAlpha(fadeAlpha)
	return false
}

// Fading 返回当前是否有淡出动画进行中（主循环据此动态调整 tick 频率）。
// 必须在主线程调用。
func Fading() bool {
	return fadeActive
}
