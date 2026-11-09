// Package settings 提供双页签配置窗口：
// 「基础配置」页签配置三个功能开关（剪贴板监控/打字检测/选中检测，
// 默认 关/开/开）与两项时长（气泡停留时长/弹出前等待时长，默认 10 秒/1 秒，
// 单位秒、支持小数）；「LLM」页签编辑 LLM 的 api_key(sk)、provider、
// base_url、model、proxy_url 五项并保存（热生效）。
// provider 下拉框选中厂商后 Base URL 固定（只读）、模型下拉框限定为该厂商模型；
// 选中 Custom（自定义）时 Base URL 与模型均为自由输入（如自建中转网关）。
// 选中内置 LLM（ByteDP）时隐藏四行，显示登录区（邮箱/密码/登录退出/注册友情捐献
// 链接与二维码切换），登录态持久化在 cfg 的 Bytedp* 字段。
//
// 事件模型：按钮点击通过 appkit 的 C 回调设置标志位，由 main 主循环统一
// 调 appkit.PollEvents() 分发（Save → Apply+落盘+热更新；Cancel → 关闭），
// 避免 cgo 回调重入 Go。
// 所有方法必须在主线程（已 LockOSThread 的 goroutine）调用。
package settings

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"elfin-grammar/internal/appkit"
	"elfin-grammar/internal/config"
)

// 窗口布局常量。
const (
	winW   = 460.0
	winH   = 260.0
	padX   = 20.0
	rowH   = 30.0
	labelW = 90.0
	fieldX = padX + labelW + 10
	fieldW = winW - fieldX - padX
	qrSize = 80.0 // 行内二维码边长（左下角展示）
)

// 页签：基础配置（功能开关）/ LLM（模型配置与登录区）。
const (
	tabBasic = iota
	tabLLM
)

var (
	win           *appkit.Window
	lblKey        *appkit.Label // 四行 label（LLM 页签显示、内置模式隐藏），包变量供 updateVisibility 切换
	lblProvider   *appkit.Label // Provider 行 label（LLM 页签显示）
	lblBase       *appkit.Label
	lblModel      *appkit.Label
	lblProxy      *appkit.Label
	fieldKey      *appkit.TextField
	popupProvider *appkit.Popup // 厂商下拉框（只读）：选中后固定 Base URL、限定模型选项
	fieldBase     *appkit.TextField
	popupModel    *appkit.Popup // 模型下拉框：选项随厂商变化，也可直接输入自定义模型名
	fieldProxy    *appkit.TextField
	// 页签按钮与功能开关（懒创建）。
	btnTabBasic *appkit.Button   // 页签：基础配置（功能开关）
	btnTabLLM   *appkit.Button   // 页签：LLM（模型配置与登录区）
	cbClipboard *appkit.Checkbox // 剪贴板监控开关（默认关闭）
	cbTyped     *appkit.Checkbox // 打字检测开关（默认开启）
	cbSelection *appkit.Checkbox // 选中检测开关（默认开启）
	// 时长输入框（秒，支持小数；基础配置页签）。
	lblBubbleShow    *appkit.Label // 气泡停留时长（默认 10 秒）
	fieldBubbleShow  *appkit.TextField
	lblSelDebounce   *appkit.Label // 弹出前等待时长（默认 1 秒）
	fieldSelDebounce *appkit.TextField
	// 内置 LLM 登录区控件（懒创建，选中内置厂商时显示）。
	lblEmail      *appkit.Label
	fieldEmail    *appkit.TextField
	lblPassword   *appkit.Label
	fieldPassword *appkit.TextField // 未登录：密码输入；已登录：隐藏，由 lblBalance 替代
	lblBalance    *appkit.Label     // 已登录显示「余额 x.xx 元」
	btnLogin      *appkit.Button    // 登录/退出登录（标题随登录态切换）
	btnPortal     *appkit.Button    // 注册/友情捐献链接
	btnQR         *appkit.Button    // 链接|二维码切换
	lblHint       *appkit.Label     // 提示行（错误/状态文案）
	qrImage       *appkit.ImageView // 行内 80x80 二维码
	winQR         *appkit.Window    // 二维码放大窗口（懒创建，title bar X 关闭）
	qrZoomImg     *appkit.ImageView
	// loggedIn 登录态内存镜像（持久化在 cfg.Bytedp*）；balanceCents 余额（分）。
	loggedIn     bool
	balanceCents int
	qrMode       bool   // true=二维码模式，false=链接模式
	builtinBase  string // 内置网关实际地址（远程网关时与 Providers[0] 不同）
	// wasShown 跟踪窗口从可见变隐藏的过渡（含用户点 X 关闭），用于归还激活状态。
	wasShown bool
	// lastProviderIdx 上次已联动的厂商下拉框下标（-2 表示尚未同步过），
	// syncProvider 据此去重，保证轮询/事件双路径幂等。
	lastProviderIdx = -2
	// curTab 当前页签（进程内记忆，跨 Show 保持）；默认 LLM 页签——
	// 未配置时自动弹窗的场景需要先配置 LLM。
	curTab = tabLLM
)

// customIdx 返回厂商下拉框 Custom（自定义）项下标（追加在厂商列表末尾）。
func customIdx() int { return len(config.Providers) }

// IsShown 配置窗口是否可见。
func IsShown() bool {
	return win != nil && win.Visible()
}

// Show 显示配置窗口并填充当前配置值（懒创建）。
func Show(cfg *config.Config) {
	if win == nil {
		appkit.InitApplication()
		win = appkit.NewWindow(winW, winH, "elfin-grammar 设置")
		// 页签栏：基础配置（功能开关）/ LLM（模型配置与登录区）。
		tabY := winH - 12.0 - 26.0
		btnTabBasic = appkit.NewButton(padX, tabY, 110, 26, "基础配置", appkit.TagTabBasic, false)
		btnTabLLM = appkit.NewButton(padX+120, tabY, 110, 26, "LLM", appkit.TagTabLLM, false)
		for _, tb := range []*appkit.Button{btnTabBasic, btnTabLLM} {
			tb.SetBezelStyle(15) // 页签风格（与气泡 tab 一致），SetState 驱动选中高亮
			tb.SetFontSize(13)
		}
		y := tabY - 34.0
		lblKey = appkit.NewLabel(padX, y, labelW, 22, "API Key (sk)")
		lblProvider = appkit.NewLabel(padX, y-rowH, labelW, 22, "Provider")
		lblBase = appkit.NewLabel(padX, y-2*rowH, labelW, 22, "Base URL")
		lblModel = appkit.NewLabel(padX, y-3*rowH, labelW, 22, "Model")
		lblProxy = appkit.NewLabel(padX, y-4*rowH, labelW, 22, "Proxy")
		fieldKey = appkit.NewTextField(fieldX, y, fieldW, 22, "", true) // 密码式
		popupProvider = appkit.NewPopup(fieldX, y-rowH, fieldW, 26, appkit.TagPopupProvider)
		popupProvider.SetEditable(false) // 厂商不可手输：URL 由厂商决定
		for _, p := range config.Providers {
			popupProvider.AddItem(p.Name)
		}
		popupProvider.AddItem("Custom（自定义）")
		fieldBase = appkit.NewTextField(fieldX, y-2*rowH, fieldW, 22, "", false)
		popupModel = appkit.NewPopup(fieldX, y-3*rowH, fieldW, 26, appkit.TagPopupModel)
		fieldProxy = appkit.NewTextField(fieldX, y-4*rowH, fieldW, 22, "", false)
		// 内置 LLM 登录区：复用行 2-5 位置（邮箱/密码/按钮/提示），
		// 行内 80x80 二维码展示在左下角（Save/Cancel 按钮上方空间）。
		lblEmail = appkit.NewLabel(padX, y-2*rowH, labelW, 22, "邮箱")
		fieldEmail = appkit.NewTextField(fieldX, y-2*rowH, fieldW, 22, "", false)
		lblPassword = appkit.NewLabel(padX, y-3*rowH, labelW, 22, "密码")
		fieldPassword = appkit.NewTextField(fieldX, y-3*rowH, fieldW, 22, "", true) // 密码式
		lblBalance = appkit.NewLabel(fieldX, y-3*rowH, fieldW, 22, "")
		lblBalance.SetAlignment(0)
		btnLogin = appkit.NewButton(fieldX, y-4*rowH, 100, 28, "登录", appkit.TagLogin, false)
		btnPortal = appkit.NewButton(fieldX+110, y-4*rowH, 100, 28, "注册/友情捐献", appkit.TagPortal, false)
		btnQR = appkit.NewButton(fieldX+220, y-4*rowH, 100, 28, "二维码", appkit.TagQR, false)
		lblHint = appkit.NewLabel(fieldX, y-5*rowH, fieldW, 22, "")
		lblHint.SetAlignment(0)
		// 功能开关与时长（基础配置页签）：剪贴板监控默认关闭，打字/选中检测默认开启；
		// 气泡停留时长默认 10 秒、弹出前等待时长（选中保持）默认 1 秒，均支持小数秒。
		lblBubbleShow = appkit.NewLabel(padX, y, labelW, 22, "气泡停留(秒)")
		fieldBubbleShow = appkit.NewTextField(fieldX, y, 90, 22, "", false)
		lblSelDebounce = appkit.NewLabel(padX, y-rowH, labelW, 22, "弹出等待(秒)")
		fieldSelDebounce = appkit.NewTextField(fieldX, y-rowH, 90, 22, "", false)
		cbClipboard = appkit.NewCheckbox(fieldX, y-2*rowH, fieldW, 22, "开启剪贴板监控")
		cbTyped = appkit.NewCheckbox(fieldX, y-3*rowH, fieldW, 22, "开启输入文字语法检测")
		cbSelection = appkit.NewCheckbox(fieldX, y-4*rowH, fieldW, 22, "开启输入框选中文字语法检测")
		qrImage = appkit.NewImageView(padX, 12, qrSize, qrSize, "")
		btnSave := appkit.NewButton(winW-2*100-padX-10, 12, 100, 28, "Save", 1001, true)
		btnCancel := appkit.NewButton(winW-100-padX, 12, 100, 28, "Cancel", 1002, false)
		win.AddSubview(btnTabBasic)
		win.AddSubview(btnTabLLM)
		win.AddSubview(lblKey)
		win.AddSubview(lblProvider)
		win.AddSubview(lblBase)
		win.AddSubview(lblModel)
		win.AddSubview(lblProxy)
		win.AddSubview(fieldKey)
		win.AddSubview(popupProvider)
		win.AddSubview(fieldBase)
		win.AddSubview(popupModel)
		win.AddSubview(fieldProxy)
		win.AddSubview(lblEmail)
		win.AddSubview(fieldEmail)
		win.AddSubview(lblPassword)
		win.AddSubview(fieldPassword)
		win.AddSubview(lblBalance)
		win.AddSubview(btnLogin)
		win.AddSubview(btnPortal)
		win.AddSubview(btnQR)
		win.AddSubview(lblHint)
		win.AddSubview(qrImage)
		win.AddSubview(lblBubbleShow)
		win.AddSubview(fieldBubbleShow)
		win.AddSubview(lblSelDebounce)
		win.AddSubview(fieldSelDebounce)
		win.AddSubview(cbClipboard)
		win.AddSubview(cbTyped)
		win.AddSubview(cbSelection)
		win.AddSubview(btnSave)
		win.AddSubview(btnCancel)
		win.MakeFirstResponder(fieldKey)
	}
	fieldKey.SetString(cfg.APIKey)
	fieldProxy.SetString(cfg.ProxyURL)
	// 功能开关与时长回显（毫秒 → 秒）。
	cbClipboard.SetState(cfg.ClipboardMonitor)
	cbTyped.SetState(cfg.TypedCheck)
	cbSelection.SetState(cfg.SelectionCheck)
	fieldBubbleShow.SetString(fmt.Sprintf("%g", float64(cfg.BubbleShowMS)/1000))
	fieldSelDebounce.SetString(fmt.Sprintf("%g", float64(cfg.SelectDebounceMS)/1000))
	// 按当前 BaseURL 回显厂商：命中内置厂商则 URL 固定（只读）、模型限定为该厂商模型；
	// 未命中但存在内置登录态（远程/自建内置网关）则同样选中内置项；
	// 其余（自建中转网关）选中 Custom，URL 与模型均为自由输入。
	if p, idx, ok := config.FindProvider(cfg.BaseURL); ok {
		popupProvider.SelectIndex(idx)
		fieldBase.SetString(p.BaseURL)
		fieldBase.SetEditable(false)
		rebuildModelPopup(p.Models, cfg.Model)
	} else if cfg.BytedpRefreshToken != "" {
		popupProvider.SelectIndex(0)
		fieldBase.SetString(cfg.BaseURL)
		fieldBase.SetEditable(true)
		popupModel.RemoveAllItems()
		popupModel.SetString(cfg.Model)
	} else {
		popupProvider.SelectIndex(customIdx())
		fieldBase.SetString(cfg.BaseURL)
		fieldBase.SetEditable(true)
		popupModel.RemoveAllItems()
		popupModel.SetString(cfg.Model)
	}
	if config.IsBuiltin(cfg) {
		builtinBase = cfg.BaseURL // 内置网关实际地址（portal URL/登录接口基准）
	}
	lastProviderIdx = popupProvider.Index() // Show 已手动填充，防止轮询误触发联动
	// 内置登录态回显：refresh token 存在即已登录，回显邮箱与余额。
	fieldEmail.SetString(cfg.BytedpEmail)
	loggedIn = cfg.BytedpEmail != "" && cfg.BytedpRefreshToken != ""
	if loggedIn {
		lblBalance.SetString(formatBalance())
	}
	updateVisibility()
	// 每次 Show 重新居中到鼠标所在屏幕：懒创建的窗口会记住旧位置，
	// 多显示器下必须跟随用户当前工作的屏幕。
	win.CenterOnMouseScreen()
	win.Show()
	wasShown = true
}

// DumpWindowState 打印设置窗口真实屏幕状态（临时诊断用）。必须在主线程调用。
func DumpWindowState() {
	if win == nil {
		log.Println("[dbg-window] win==nil")
		return
	}
	win.DumpWindowState()
}

// Activate 把已可见的设置窗口带到前台并置为 key（不重填表单、不重新居中）。
// 窗口已显示但被其他应用遮挡/失焦时，点设置按钮（气泡/状态栏）或快捷键
// 应重新激活窗口而非无动作，故调用方在 IsShown 为 true 时走本方法而非 Show。
// 必须在主线程调用。
func Activate() {
	if win != nil {
		win.Show() // 激活本应用 + makeKeyAndOrderFront
	}
}

// Close 关闭配置窗口。若 close 未生效（如进行中的文本编辑会话），强制 orderOut。
func Close() {
	if win != nil {
		win.Close()
		if win.Visible() {
			win.OrderOut() // 兜底：保证窗口离屏，进程能正常退出
		}
	}
	if winQR != nil && winQR.Visible() {
		winQR.OrderOut() // 二维码放大窗口一并关闭
	}
	wasShown = false
	appkit.Deactivate() // 归还焦点：防止按键被无窗口的激活状态吞掉
}

// RecheckFocus 检测窗口被用户点 X 关闭（不经过 Close）的情况并归还焦点。
// 必须在主循环定期调用。
func RecheckFocus() {
	if wasShown && !IsShown() {
		wasShown = false
		appkit.Deactivate()
	}
}

// OnProviderChanged 处理厂商下拉框选择变化事件（EventProviderChanged 分发）。
// 与 PollProviderSelection 共用 syncProvider：事件驱动 + 主循环轮询双保险，
// 保证选中厂商时 Base URL 与模型下拉框必然联动。必须在主线程调用。
func OnProviderChanged() {
	syncProvider()
}

// PollProviderSelection 主循环轮询厂商下拉框选中项：手动泵事件的环境下
// 下拉框 action 事件可能未派发，直接对比下标检测变化兜底联动（幂等）。
// 窗口未显示时跳过（避免空闲轮询开销）。必须在主线程调用。
func PollProviderSelection() {
	if !IsShown() {
		return
	}
	syncProvider()
}

// syncProvider 厂商下拉框选中项变化时联动：选中内置厂商 → Base URL 固定为
// 厂商端点（只读）、模型下拉框重建为该厂商模型并选中默认模型；选中 Custom →
// URL 恢复可编辑、模型自由输入（保留已输入内容）。按 lastProviderIdx 去重。
func syncProvider() {
	if popupProvider == nil {
		return
	}
	idx := popupProvider.Index()
	if idx == lastProviderIdx {
		return
	}
	lastProviderIdx = idx
	if idx >= 0 && idx < len(config.Providers) {
		p := config.Providers[idx]
		fieldBase.SetString(p.BaseURL)
		fieldBase.SetEditable(false)
		rebuildModelPopup(p.Models, "") // 切厂商后选中该厂商默认模型
		updateVisibility()              // 内置/其他厂商切换四行与登录区显隐
		return
	}
	// Custom：URL 与模型均为自由输入，保留模型框已输入内容。
	s := popupModel.String()
	popupModel.RemoveAllItems()
	popupModel.SetString(s)
	fieldBase.SetEditable(true)
	updateVisibility()
}

// rebuildModelPopup 用厂商模型重建模型下拉框：keep 命中列表项则保持选中，
// keep 非空但未命中（自定义模型名）则回显 keep，keep 为空选中默认模型（第一项）。
func rebuildModelPopup(models []string, keep string) {
	popupModel.RemoveAllItems()
	for _, m := range models {
		popupModel.AddItem(m)
	}
	for i, m := range models {
		if m == keep {
			popupModel.SelectIndex(i)
			return
		}
	}
	if keep != "" {
		popupModel.SetString(keep)
		return
	}
	popupModel.SelectIndex(0)
}

// Apply 读取表单内容写入 cfg。
// 功能开关：读取三个复选控件（与当前页签无关，基础配置页签修改后保存）。
// 厂商下拉框：选中内置厂商时 BaseURL 固定取内置端点、不覆盖 API Key/Proxy
// 与 Bytedp* 登录字段（内置模式表单仅登录区，四行已隐藏）；选中其他内置厂商
// 时 BaseURL 固定取厂商端点（URL 框只读），Model 取模型下拉框文本（可选中
// 厂商模型或输入自定义模型名）；选中 Custom 时 BaseURL 取 URL 文本框、Model
// 取模型下拉框文本。
func Apply(cfg *config.Config) {
	cfg.ClipboardMonitor = cbClipboard.State()
	cfg.TypedCheck = cbTyped.State()
	cfg.SelectionCheck = cbSelection.State()
	// 时长输入框（秒，支持小数）：无效/非正输入保持原值不变。
	if v, err := strconv.ParseFloat(strings.TrimSpace(fieldBubbleShow.String()), 64); err == nil && v > 0 {
		cfg.BubbleShowMS = int(v * 1000)
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(fieldSelDebounce.String()), 64); err == nil && v > 0 {
		cfg.SelectDebounceMS = int(v * 1000)
	}
	idx := popupProvider.Index()
	if idx >= 0 && idx < len(config.Providers) {
		if idx == 0 { // 内置 LLM：不覆盖 API Key/Proxy/Bytedp*，网关地址保持现状
			// （远程/自建内置网关时 cfg.BaseURL 已指向实际地址，不应重置为默认端点）
			if !config.IsBuiltin(cfg) {
				cfg.BaseURL = config.Providers[0].BaseURL
			}
			return
		}
		cfg.BaseURL = config.Providers[idx].BaseURL
		cfg.APIKey = fieldKey.String()
		cfg.ProxyURL = fieldProxy.String()
		cfg.Model = popupModel.String()
		return
	}
	cfg.APIKey = fieldKey.String()
	cfg.ProxyURL = fieldProxy.String()
	cfg.Model = popupModel.String()
	cfg.BaseURL = fieldBase.String()
}

// ---------- 内置 LLM 登录区（main 事件分发调用） ----------

// IsBuiltinMode 当前厂商下拉框是否选中内置 LLM（供 main 决定弹设置窗与客户端构造）。
// 必须在主线程调用。
func IsBuiltinMode() bool {
	return popupProvider != nil && popupProvider.Index() == 0
}

// LoginForm 返回登录表单内容（邮箱/密码）。必须在主线程调用。
func LoginForm() (email, password string) {
	if fieldEmail == nil || fieldPassword == nil {
		return "", ""
	}
	return fieldEmail.String(), fieldPassword.String()
}

// SetLoggedIn 登录成功回显：记录登录态、回显邮箱、刷新余额显示、按钮切「退出登录」。
// 必须在主线程调用。
func SetLoggedIn(email string, balance int) {
	loggedIn = true
	balanceCents = balance
	if fieldEmail != nil {
		fieldEmail.SetString(email)
	}
	if lblBalance != nil {
		lblBalance.SetString(formatBalance())
	}
	if fieldPassword != nil {
		fieldPassword.SetString("")
	}
	updateVisibility()
}

// SetBalance 更新余额显示（登录后查询/计费后回写）。必须在主线程调用。
func SetBalance(cents int) {
	balanceCents = cents
	if lblBalance != nil && loggedIn {
		lblBalance.SetString(formatBalance())
	}
}

// SetHint 设置提示行文本（登录错误/状态文案）。必须在主线程调用。
func SetHint(text string) {
	if lblHint != nil {
		lblHint.SetString(text)
	}
}

// SetLoggedOut 退出登录后恢复未登录 UI。必须在主线程调用。
func SetLoggedOut() {
	loggedIn = false
	balanceCents = 0
	if fieldPassword != nil {
		fieldPassword.SetString("")
	}
	updateVisibility()
}

// PortalURL 返回内置门户页 URL（内置网关实际地址去 /v1 后缀 + /portal）。
func PortalURL() string {
	base := builtinBase
	if base == "" {
		if len(config.Providers) == 0 {
			return ""
		}
		base = config.Providers[0].BaseURL
	}
	return strings.TrimSuffix(base, "/v1") + "/portal"
}

// OnQR 链接/二维码切换（EventQR 分发）：切到二维码模式时显示行内 80x80 二维码
// 并弹出放大窗口；切回链接模式时隐藏二维码并关闭放大窗口。必须在主线程调用。
func OnQR() {
	qrMode = !qrMode
	if qrMode {
		if qrImage != nil {
			qrImage.SetText(PortalURL())
			qrImage.SetHidden(false)
		}
		showQRZoom()
	} else {
		if qrImage != nil {
			qrImage.SetHidden(true)
		}
		if winQR != nil && winQR.Visible() {
			winQR.OrderOut()
		}
	}
	updateVisibility()
}

// showQRZoom 弹出二维码放大窗口（懒创建，200x200 大二维码，title bar X 关闭）。
func showQRZoom() {
	if winQR == nil {
		winQR = appkit.NewWindow(260, 300, "扫码打开门户")
		qrZoomImg = appkit.NewImageView(30, 70, 200, 200, PortalURL())
		winQR.AddSubview(qrZoomImg)
	} else {
		qrZoomImg.SetText(PortalURL())
	}
	winQR.CenterOnMouseScreen()
	winQR.Show()
}

// formatBalance 分 → 「余额 x.xx 元」。
func formatBalance() string {
	return fmt.Sprintf("余额 %.2f 元", float64(balanceCents)/100)
}

// updateVisibility 按当前页签、厂商与登录态切换控件显隐：
// LLM 页签 → Provider 行恒显，内置厂商时四行隐藏、登录区显示；
// 基础页签 → 仅显示三个功能开关。页签按钮高亮由 SetState 驱动。
// 必须在主线程调用。
func updateVisibility() {
	llm := curTab == tabLLM
	builtin := IsBuiltinMode()
	four := llm && !builtin // 四行（API Key/BaseURL/Model/Proxy）
	login := llm && builtin // 登录区
	basic := !llm           // 功能开关

	// 页签高亮。
	btnTabBasic.SetState(basic)
	btnTabLLM.SetState(llm)

	// Provider 行：仅 LLM 页签显示（内置/其他厂商/Custom 均需可切换）。
	lblProvider.SetHidden(!llm)
	popupProvider.SetHidden(!llm)

	// 四行：LLM 页签且非内置模式。
	lblKey.SetHidden(!four)
	fieldKey.SetHidden(!four)
	lblBase.SetHidden(!four)
	fieldBase.SetHidden(!four)
	lblModel.SetHidden(!four)
	popupModel.SetHidden(!four)
	lblProxy.SetHidden(!four)
	fieldProxy.SetHidden(!four)

	// 登录区：LLM 页签且内置模式。
	lblEmail.SetHidden(!login)
	fieldEmail.SetHidden(!login)
	lblPassword.SetHidden(!login || loggedIn)
	fieldPassword.SetHidden(!login || loggedIn)
	lblBalance.SetHidden(!login || !loggedIn)
	btnLogin.SetHidden(!login)
	btnPortal.SetHidden(!login)
	btnQR.SetHidden(!login)
	lblHint.SetHidden(!login)
	qrImage.SetHidden(!login || !qrMode)

	// 功能开关与时长：基础页签。
	cbClipboard.SetHidden(!basic)
	cbTyped.SetHidden(!basic)
	cbSelection.SetHidden(!basic)
	lblBubbleShow.SetHidden(!basic)
	fieldBubbleShow.SetHidden(!basic)
	lblSelDebounce.SetHidden(!basic)
	fieldSelDebounce.SetHidden(!basic)

	if login {
		if loggedIn {
			btnLogin.SetTitle("退出登录")
		} else {
			btnLogin.SetTitle("登录")
		}
		if qrMode {
			btnQR.SetTitle("链接")
		} else {
			btnQR.SetTitle("二维码")
		}
	}
}

// OnTabChanged 处理页签点击事件（EventTabBasic/EventTabLLM 分发）：
// 切换当前页签并刷新两组控件显隐。必须在主线程调用。
func OnTabChanged(basic bool) {
	if basic {
		curTab = tabBasic
	} else {
		curTab = tabLLM
	}
	updateVisibility()
}
