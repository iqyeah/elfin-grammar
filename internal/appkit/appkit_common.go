// Package appkit 的跨平台共享常量：按钮/快捷键事件标志位与下拉框 tag。
// 平台实现（darwin 的 AppKit 桥接 / windows 的 Win32 桥接）共享同一套位定义，
// main/settings/bubble 无需感知平台差异。
package appkit

// EventFlags 是按钮/快捷键事件标志位组合。
type EventFlags int

const (
	// EventSave 保存按钮被点击。
	EventSave EventFlags = 1 << iota
	// EventCancel 取消按钮被点击。
	EventCancel
	// EventHotkey 全局快捷键被按下（darwin=Cmd+Shift+,，windows=Ctrl+Shift+,）。
	EventHotkey
	// EventApply 气泡 Apply 按钮被点击。
	EventApply
	// EventTranslate 气泡 Translate 按钮被点击。
	EventTranslate
	// EventCopy 气泡 Copy 按钮被点击。
	EventCopy
	// EventAnalyze 气泡 Grammar 按钮被点击。
	EventAnalyze
	// EventSettings 气泡 Settings 按钮被点击。
	EventSettings
	// EventModelChanged 设置窗口模型下拉框选择变化。
	EventModelChanged
	// EventStatusSettings 状态栏/托盘菜单 Settings 被点击。
	EventStatusSettings
	// EventStatusExit 状态栏/托盘菜单 Exit 被点击。
	EventStatusExit
	// EventProviderChanged 设置窗口厂商下拉框选择变化。
	EventProviderChanged
	// EventTabCorrect 气泡 Correct tab 被点击。
	EventTabCorrect
	// EventTabTranslate 气泡 Translate tab 被点击。
	EventTabTranslate
	// EventTabAnalyze 气泡 Analyze tab 被点击。
	EventTabAnalyze
	// EventClose 气泡右上角关闭按钮被点击。
	EventClose
	// EventLogin 设置窗口内置 LLM 登录按钮被点击。
	EventLogin
	// EventLogout 设置窗口内置 LLM 退出登录按钮被点击。
	EventLogout
	// EventPortal 设置窗口注册/友情捐献链接被点击。
	EventPortal
	// EventQR 设置窗口二维码切换按钮被点击。
	EventQR
	// EventStatusGuide 状态栏/托盘菜单 User Guide 被点击（打开在线手册）。
	EventStatusGuide
	// EventTabBasic 设置窗口「基础配置」页签被点击。
	EventTabBasic
	// EventTabLLM 设置窗口「LLM」页签被点击。
	EventTabLLM
	// EventApplyEn 气泡英文行 Apply/Copy 按钮被点击。
	EventApplyEn
	// EventApplyZh 气泡中文行 Apply/Copy 按钮被点击。
	EventApplyZh
)

// Popup 下拉框 tag 值（darwin 与 C 侧 BTN_TAG_POPUP_* 一致；windows 作为控件 ID）：
// 区分选择变化的事件来源。
const (
	// TagPopupModel 模型下拉框 tag：选择变化上报 EventModelChanged。
	TagPopupModel int64 = 3001
	// TagPopupProvider 厂商下拉框 tag：选择变化上报 EventProviderChanged。
	TagPopupProvider int64 = 3002
)

// 内置 LLM 登录区按钮 tag 值（与 C 侧 BTN_TAG_LOGIN..BTN_TAG_QR 一致；
// windows 作为控件 ID）：点击上报 EventLogin/EventLogout/EventPortal/EventQR。
const (
	// TagLogin 登录按钮 tag。
	TagLogin int64 = 6001
	// TagLogout 退出登录按钮 tag。
	TagLogout int64 = 6002
	// TagPortal 注册/友情捐献链接 tag（NSButton 无边框样式充当链接）。
	TagPortal int64 = 6003
	// TagQR 二维码切换按钮 tag。
	TagQR int64 = 6004
	// TagTabBasic 设置窗口「基础配置」页签 tag。
	TagTabBasic int64 = 6005
	// TagTabLLM 设置窗口「LLM」页签 tag。
	TagTabLLM int64 = 6006
)
