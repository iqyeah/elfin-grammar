// ax 包的跨平台共享类型与工具：SkipReason/Rect/Range 与三种读取结果结构体，
// 以及 UTF-16 偏移换算工具（darwin 与 windows 的 ReplaceFocused 共用）。
package ax

import (
	"unicode/utf16"
	"unicode/utf8"
)

// SkipReason 说明本次跳过读取的原因。
type SkipReason int

const (
	// SkipNone 表示正常读取。
	SkipNone SkipReason = iota
	// SkipSecure 表示密码框等安全字段。
	SkipSecure
	// SkipNoAccess 表示应用未暴露辅助功能信息（微信/Flutter/Electron 等）。
	SkipNoAccess
	// SkipNotText 表示焦点元素不是文本输入域。
	SkipNotText
)

func (s SkipReason) String() string {
	switch s {
	case SkipSecure:
		return "安全字段(密码框)"
	case SkipNoAccess:
		return "应用不暴露辅助功能信息"
	case SkipNotText:
		return "非文本输入域"
	default:
		return ""
	}
}

// Rect 是输入框在屏幕坐标系中的位置（原点主屏左上，y 向下：
// darwin=AX 坐标，windows=物理像素）。
type Rect struct {
	X, Y, W, H float64
}

// Range 是文本选中范围（UTF-16 码元单位）。
// darwin 来自 kAXSelectedTextRangeAttribute；windows 的 UIA TextRange 是不透明
// 对象、无数值区间，恒为 Has=false（替换定位退化为 LastIndex 查找）。
type Range struct {
	Has      bool
	Loc, Len int
}

// Result 是一次焦点读取的产物。
type Result struct {
	Text      string
	Selection string // 当前选中文本（无选中时为空）
	SelRange  Range  // 当前选中范围（无选中或读取失败时 Has=false）
	App       string
	Rect      Rect
	IsWeb     bool // 目标是网页正文而非输入框（darwin=AXWebArea，windows=浏览器 Document）
	Skip      SkipReason
}

// LightResult 是一次轻量焦点读取的产物：仅文本与选中，供高频轮询路径使用。
// rect/app 等展示上下文不在此读取（降低跨进程 IPC 次数），由 ReadFocusedContext 在触发时补充。
type LightResult struct {
	Text      string
	Selection string
	Role      string // 读取目标的角色（诊断选中来源：AXWebArea/AXTextField 或 UIA Edit/Document）
	IsWeb     bool   // 目标是网页正文而非输入框
	Skip      SkipReason
}

// Context 是触发展示时才读取的重量上下文：应用名、输入框位置与选中范围。
type Context struct {
	App      string
	Rect     Rect
	SelRange Range
	IsWeb    bool // 目标是网页正文而非输入框
}

// utf16Slice 返回 s 中 [loc, loc+length) 的 UTF-16 码元对应的文本。
func utf16Slice(s string, loc, length int) (string, bool) {
	u := utf16.Encode([]rune(s))
	if loc < 0 || length < 0 || loc+length > len(u) {
		return "", false
	}
	return string(utf16.Decode(u[loc : loc+length])), true
}

// utf16ToByte 把 UTF-16 码元偏移换算为 UTF-8 字节偏移。
func utf16ToByte(s string, loc16 int) (int, bool) {
	u := utf16.Encode([]rune(s))
	if loc16 < 0 || loc16 > len(u) {
		return 0, false
	}
	n := 0
	for _, r := range utf16.Decode(u[:loc16]) {
		n += utf8.RuneLen(r)
	}
	return n, true
}
