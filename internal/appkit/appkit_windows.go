//go:build windows

// Package appkit 的 Windows 实现：Win32 桥接（窗口/控件创建、文本读写、按钮回调、
// 全局快捷键、托盘、剪贴板），公开 API 与 darwin 版一致。
// 所有函数必须在主线程（已 LockOSThread 的 goroutine）调用。
package appkit

/*
#cgo LDFLAGS: -lcomctl32 -luser32 -lgdi32 -lshell32 -lole32 -loleaut32 -ldwmapi -luxtheme

#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0601
#endif
#include <windows.h>
#include <commctrl.h>
#include <shellapi.h>
#include <dwmapi.h>
#include <uxtheme.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>

// ---------- 深色主题配色（与气泡底色 RGB(0x1F,0x1F,0x26) 统一） ----------
#define UI_BG         RGB(0x1F,0x1F,0x26)  // 窗口/气泡背景
#define UI_SURFACE    RGB(0x2A,0x2A,0x33)  // 控件面（按钮/输入框）
#define UI_SURFACE_HI RGB(0x35,0x35,0x40)  // 悬停提亮
#define UI_BORDER     RGB(0x3A,0x3A,0x44)  // 描边
#define UI_TEXT       RGB(0xE8,0xE8,0xEC)  // 正文
#define UI_TEXT_DIM   RGB(0x9A,0x9A,0xA6)  // 次要文字
#define UI_ACCENT     RGB(0x3A,0xA6,0x4F)  // 主色绿（主按钮/选中页签）
#define UI_ACCENT_HI  RGB(0x46,0xC4,0x5F)  // 主色绿悬停
#define UI_DANGER     RGB(0xE0,0x5A,0x5A)  // 危险悬停（关闭按钮）

#ifndef DWMWA_USE_IMMERSIVE_DARK_MODE
#define DWMWA_USE_IMMERSIVE_DARK_MODE 20
#endif

// appkit_draw_item: 深色自绘控件统一绘制入口（定义在控件绘制节，bubble 窗口 extern 引用）。
void appkit_draw_item(HWND item, int id, UINT state, HDC dc, const RECT *rc);

// ---------- 事件标志位（与 darwin 位定义一致，见 appkit_common.go） ----------
static volatile int g_event_flags;

// 各按钮/下拉框的控件 ID（与 darwin 侧 BTN_TAG_* 一致；windows 上作为控件 ID）。
enum {
    BTN_TAG_SAVE = 1001, BTN_TAG_CANCEL = 1002,
    BTN_TAG_APPLY = 2001, BTN_TAG_TRANSLATE = 2002, BTN_TAG_COPY = 2003,
    BTN_TAG_ANALYZE = 2004, BTN_TAG_SETTINGS = 2005,
    // 行内按钮（英文行/中文行的 Apply/Copy/Copied 动态按钮）：bit23-24 事件位。
    BTN_TAG_APPLY_EN = 2006, BTN_TAG_APPLY_ZH = 2007,
    BTN_TAG_POPUP_MODEL = 3001, BTN_TAG_POPUP_PROVIDER = 3002,
    BTN_TAG_STATUS_SETTINGS = 4001, BTN_TAG_STATUS_EXIT = 4002, BTN_TAG_STATUS_GUIDE = 4003,
    BTN_TAG_TAB_CORRECT = 5001, BTN_TAG_TAB_TRANSLATE = 5002, BTN_TAG_TAB_ANALYZE = 5003,
    BTN_TAG_CLOSE = 5004,
    BTN_TAG_LOGIN = 6001, BTN_TAG_LOGOUT = 6002, BTN_TAG_PORTAL = 6003, BTN_TAG_QR = 6004,
    // 设置窗口页签（bit21-22 事件位）：基础配置/LLM。
    BTN_TAG_TAB_BASIC = 6005, BTN_TAG_TAB_LLM = 6006,
    // 复选开关控件 ID 段（自绘复选框不自动切换，点击经 WM_COMMAND 翻转）。
    BTN_TAG_CHK_BASE = 6100, BTN_TAG_CHK_END = 6163,
};

// 跨包导出符号（bubble 窗口/runloop 消息泵的 C 前导 extern 引用）。
// appkit_set_flags: 按控件 ID 设置事件位（与 darwin 的 appkit_on_button 同映射）。
void appkit_set_flags(int id) {
    if (id == BTN_TAG_SAVE) {
        g_event_flags |= 1;
    } else if (id == BTN_TAG_CANCEL) {
        g_event_flags |= 2;
    } else if (id >= BTN_TAG_APPLY && id <= BTN_TAG_SETTINGS) {
        g_event_flags |= 1 << (id - BTN_TAG_APPLY + 3);
    } else if (id == BTN_TAG_APPLY_EN) {
        g_event_flags |= 1 << 23; // EventApplyEn：英文行按钮
    } else if (id == BTN_TAG_APPLY_ZH) {
        g_event_flags |= 1 << 24; // EventApplyZh：中文行按钮
    } else if (id == BTN_TAG_POPUP_MODEL) {
        g_event_flags |= 256;
    } else if (id == BTN_TAG_POPUP_PROVIDER) {
        g_event_flags |= 2048;
    } else if (id == BTN_TAG_STATUS_SETTINGS) {
        g_event_flags |= 512;
    } else if (id == BTN_TAG_STATUS_EXIT) {
        g_event_flags |= 1024;
    } else if (id == BTN_TAG_STATUS_GUIDE) {
        g_event_flags |= 1 << 20; // EventStatusGuide：打开在线手册
    } else if (id >= BTN_TAG_TAB_CORRECT && id <= BTN_TAG_CLOSE) {
        g_event_flags |= 1 << (id - BTN_TAG_TAB_CORRECT + 12);
    } else if (id >= BTN_TAG_LOGIN && id <= BTN_TAG_QR) {
        g_event_flags |= 1 << (id - BTN_TAG_LOGIN + 16);
    } else if (id == BTN_TAG_TAB_BASIC) {
        g_event_flags |= 1 << 21; // EventTabBasic：切到「基础配置」页签
    } else if (id == BTN_TAG_TAB_LLM) {
        g_event_flags |= 1 << 22; // EventTabLLM：切到「LLM」页签
    }
}

// appkit_handle_thread_message: runloop 消息泵把线程队列消息转交本模块。
// 目前仅 WM_HOTKEY（RegisterHotKey 的快捷键消息投递到线程队列）。
void appkit_handle_thread_message(const MSG *msg) {
    if (msg->message == WM_HOTKEY) {
        g_event_flags |= 4; // EventHotkey
    }
}

// ---------- 全局状态 ----------
static HINSTANCE g_hinst;
static HWND g_msg_hwnd;        // message-only 窗口：托盘/热键宿主
static HWND g_prev_foreground; // 设置窗口 Show 前的上一个前台窗口（Deactivate 归还焦点）
static HFONT g_font_cache[64]; // SetFontSize 的字体缓存（按 pt 取整索引）
static HBRUSH g_ui_bg_brush;     // 深色背景刷（窗口类/CTLCOLOR 共用）
static HBRUSH g_ui_surface_brush; // 深色控件面刷（输入框/下拉列表）

// ---------- 控件勾选态表（自绘按钮无 BS_AUTOCHECKBOX 自动切换，C 侧手动维护） ----------
// 页签选中态（BM_SETCHECK 对自绘按钮无效）与复选框勾选态统一经此表维护。
typedef struct btn_state_node {
    HWND hwnd;
    int checked;
    struct btn_state_node *next;
} btn_state_node;
static btn_state_node *g_btn_states;

static btn_state_node *appkit_find_state(HWND hwnd) {
    btn_state_node *n;
    for (n = g_btn_states; n != NULL; n = n->next) {
        if (n->hwnd == hwnd) return n;
    }
    return NULL;
}

static int appkit_get_state(HWND hwnd) {
    btn_state_node *n = appkit_find_state(hwnd);
    return n != NULL ? n->checked : 0;
}

static void appkit_set_state(HWND hwnd, int on) {
    btn_state_node *n = appkit_find_state(hwnd);
    if (n == NULL) {
        n = (btn_state_node *)calloc(1, sizeof(btn_state_node));
        if (n == NULL) return;
        n->hwnd = hwnd;
        n->next = g_btn_states;
        g_btn_states = n;
    }
    n->checked = on ? 1 : 0;
    InvalidateRect(hwnd, NULL, TRUE);
}

static int appkit_dpi(void) {
    HDC dc = GetDC(NULL);
    int dpi = GetDeviceCaps(dc, LOGPIXELSY);
    ReleaseDC(NULL, dc);
    if (dpi <= 0) dpi = 96;
    return dpi;
}

// appkit_scale_for_dpi: 逻辑尺寸 → 当前 DPI 像素（二维码位图生成用）。
static int appkit_scale_for_dpi(double logical) {
    return (int)(logical * appkit_dpi() / 96.0 + 0.5);
}

// appkit_enable_dpi: Per-Monitor V2 感知（多显示器不同缩放比下最清晰），
// 老系统动态加载失败时回退系统级 SetProcessDPIAware。
static void appkit_enable_dpi(void) {
    typedef BOOL (WINAPI *fn_t)(HANDLE);
    fn_t fn = (fn_t)(void *)GetProcAddress(GetModuleHandleW(L"user32.dll"),
        "SetProcessDpiAwarenessContext");
    if (fn != NULL && fn((HANDLE)(INT_PTR)-4)) { // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
        return;
    }
    SetProcessDPIAware();
}

// ---------- 设置窗口 WndProc ----------
static LRESULT CALLBACK appkit_wndproc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
    switch (msg) {
    case WM_COMMAND: {
        int id = LOWORD(wParam);
        // 自绘复选框：BN_CLICKED 翻转勾选态（无事件位上报，Save 时读 state）。
        if (id >= BTN_TAG_CHK_BASE && id <= BTN_TAG_CHK_END) {
            if (HIWORD(wParam) == BN_CLICKED) {
                HWND chk = GetDlgItem(hwnd, id);
                if (chk != NULL) {
                    appkit_set_state(chk, !appkit_get_state(chk));
                }
            }
            break;
        }
        // 下拉框仅在选中项真正变化时上报（CBN_SELCHANGE）；
        // 按钮的 BN_CLICKED 与 tab 的 BS_AUTOCHECKBOX 点击都走同一映射。
        if (id == BTN_TAG_POPUP_MODEL || id == BTN_TAG_POPUP_PROVIDER) {
            if (HIWORD(wParam) == CBN_SELCHANGE) {
                appkit_set_flags(id);
            }
        } else {
            appkit_set_flags(id);
        }
        break;
    }
    case WM_CLOSE:
        // 用户点标题栏 X：隐藏窗口（settings 的 RecheckFocus 检测到不可见后 Deactivate）。
        ShowWindow(hwnd, SW_HIDE);
        return 0;
    case WM_CTLCOLORSTATIC: {
        // 标签：深底浅字。
        HDC dc = (HDC)wParam;
        SetBkMode(dc, TRANSPARENT);
        SetTextColor(dc, UI_TEXT);
        return (LRESULT)g_ui_bg_brush;
    }
    case WM_CTLCOLOREDIT:
    case WM_CTLCOLORLISTBOX: {
        // 输入框/下拉列表：深色面 + 浅字。
        HDC dc = (HDC)wParam;
        SetBkColor(dc, UI_SURFACE);
        SetTextColor(dc, UI_TEXT);
        return (LRESULT)g_ui_surface_brush;
    }
    case WM_DRAWITEM: {
        // 自绘按钮/复选框：统一走共享绘制函数（bubble 窗口复用同一实现）。
        DRAWITEMSTRUCT *dis = (DRAWITEMSTRUCT *)lParam;
        appkit_draw_item(dis->hwndItem, (int)dis->CtlID, dis->itemState, dis->hDC, &dis->rcItem);
        return TRUE;
    }
    }
    return DefWindowProcW(hwnd, msg, wParam, lParam);
}

// ---------- 字符串互转 ----------
static char *appkit_wcs_to_utf8(const wchar_t *w, int n) {
    if (w == NULL || n <= 0) return NULL;
    int m = WideCharToMultiByte(CP_UTF8, 0, w, n, NULL, 0, NULL, NULL);
    if (m <= 0) return NULL;
    char *out = (char *)malloc((size_t)m + 1);
    if (out == NULL) return NULL;
    WideCharToMultiByte(CP_UTF8, 0, w, n, out, m, NULL, NULL);
    out[m] = '\0';
    return out;
}

static wchar_t *appkit_utf8_to_wcs(const char *s) {
    if (s == NULL) return NULL;
    int n = MultiByteToWideChar(CP_UTF8, 0, s, -1, NULL, 0);
    if (n <= 0) return NULL;
    wchar_t *out = (wchar_t *)malloc(sizeof(wchar_t) * (size_t)n);
    if (out == NULL) return NULL;
    MultiByteToWideChar(CP_UTF8, 0, s, -1, out, n);
    return out;
}

// ---------- 模态确认对话框（首次启动免责声明） ----------
// appkit_confirm_dialog: TaskDialogIndirect 模态确认框（阻塞直到用户点击）。
// 与 darwin 版 API 一致：accept/decline 分别为第一/第二按钮文案。
// 返回 1=第一按钮（accept），0=第二按钮（decline）。
static int appkit_confirm_dialog(const char *title, const char *message,
                                 const char *accept, const char *decline) {
    wchar_t *wt = appkit_utf8_to_wcs(title);
    wchar_t *wm = appkit_utf8_to_wcs(message);
    wchar_t *wa = appkit_utf8_to_wcs(accept);
    wchar_t *wd = appkit_utf8_to_wcs(decline);
    if (wt == NULL || wm == NULL || wa == NULL || wd == NULL) {
        free(wt); free(wm); free(wa); free(wd);
        return 0;
    }
    TASKDIALOG_BUTTON btns[2] = {
        { 1001, wa },
        { 1002, wd },
    };
    TASKDIALOGCONFIG tc;
    memset(&tc, 0, sizeof(tc));
    tc.cbSize = sizeof(tc);
    tc.hwndParent = NULL;
    tc.pszWindowTitle = wt;
    tc.pszContent = wm;
    tc.pszMainIcon = TD_WARNING_ICON;
    // TDF_ALLOW_DIALOG_CANCELLATION：允许 Esc/标题栏 X 关闭（视同拒绝）。
    tc.dwFlags = TDF_ALLOW_DIALOG_CANCELLATION;
    tc.cButtons = 2;
    tc.pButtons = btns;
    // 默认焦点放在「退出」（回车不误同意）。
    tc.nDefaultButton = 1002;
    int clicked = 0;
    HRESULT hr = TaskDialogIndirect(&tc, &clicked, NULL, NULL);
    free(wt); free(wm); free(wa); free(wd);
    return (hr == S_OK && clicked == 1001) ? 1 : 0;
}

// ---------- 自绘控件绘制（设置窗口与气泡共用） ----------
// 深色主题下按钮/复选框统一自绘：主按钮（Save/Apply）恒主色绿；页签为分段样式
// （选中态主色绿填充）；关闭按钮悬停红色；其余为深色面按钮。复选框绘制
// 圆角方框 + 勾号（勾选态经 C 侧状态表维护，BS_OWNERDRAW 不自动切换）。
static void appkit_draw_checkbox(HWND item, UINT state, HDC dc, const RECT *rc) {
    int checked = appkit_get_state(item);
    int hot = (state & ODS_HOTLIGHT) != 0;
    int box = appkit_scale_for_dpi(16.0);

    HBRUSH bg = CreateSolidBrush(UI_BG);
    FillRect(dc, rc, bg);
    DeleteObject(bg);

    int bx = rc->left;
    int by = rc->top + (rc->bottom - rc->top - box) / 2;
    HBRUSH fill = CreateSolidBrush(checked ? UI_ACCENT : UI_SURFACE);
    HBRUSH oldb = (HBRUSH)SelectObject(dc, fill);
    HPEN pen = CreatePen(PS_SOLID, 1, checked ? UI_ACCENT : (hot ? UI_TEXT_DIM : UI_BORDER));
    HPEN oldp = (HPEN)SelectObject(dc, pen);
    RoundRect(dc, bx, by, bx + box, by + box, 6, 6);
    SelectObject(dc, oldp);
    DeleteObject(pen);
    SelectObject(dc, oldb);
    DeleteObject(fill);

    if (checked) {
        HPEN cp = CreatePen(PS_SOLID, 2, RGB(0xFF, 0xFF, 0xFF));
        HPEN op = (HPEN)SelectObject(dc, cp);
        POINT pts[3];
        pts[0].x = bx + 3; pts[0].y = by + 8;
        pts[1].x = bx + 7; pts[1].y = by + 12;
        pts[2].x = bx + 13; pts[2].y = by + 4;
        Polyline(dc, pts, 3);
        SelectObject(dc, op);
        DeleteObject(cp);
    }

    SetBkMode(dc, TRANSPARENT);
    SetTextColor(dc, UI_TEXT);
    HFONT f = (HFONT)SendMessageW(item, WM_GETFONT, 0, 0);
    HGDIOBJ of = SelectObject(dc, f != NULL ? f : g_font_cache[0]);
    RECT tr = *rc;
    tr.left = bx + box + 6;
    wchar_t title[256];
    GetWindowTextW(item, title, 256);
    DrawTextW(dc, title, -1, &tr, DT_LEFT | DT_VCENTER | DT_SINGLELINE | DT_NOPREFIX);
    SelectObject(dc, of);
}

static void appkit_draw_button(HWND item, int id, UINT state, HDC dc, const RECT *rc) {
    int hot = (state & ODS_HOTLIGHT) != 0;
    int pressed = (state & ODS_SELECTED) != 0;
    int checked = appkit_get_state(item);
    int tab = (id >= BTN_TAG_TAB_CORRECT && id <= BTN_TAG_CLOSE);
    int primary = (id == BTN_TAG_SAVE || id == BTN_TAG_APPLY);
    int danger = (id == BTN_TAG_CLOSE);

    COLORREF fill = UI_SURFACE;
    COLORREF border = UI_BORDER;
    COLORREF text = UI_TEXT;
    if (tab && checked) {       // 选中页签：主色绿填充
        fill = hot ? UI_ACCENT_HI : UI_ACCENT;
        border = fill;
        text = RGB(0xFF, 0xFF, 0xFF);
    } else if (tab) {           // 未选中页签：透明底（悬停提亮）
        fill = hot ? UI_SURFACE_HI : UI_BG;
        border = hot ? UI_BORDER : UI_BG;
    } else if (primary) {       // 主按钮
        fill = hot ? UI_ACCENT_HI : UI_ACCENT;
        border = fill;
        text = RGB(0xFF, 0xFF, 0xFF);
    } else if (danger && hot) { // 关闭按钮悬停
        fill = UI_DANGER;
        border = UI_DANGER;
        text = RGB(0xFF, 0xFF, 0xFF);
    } else if (hot) {
        fill = UI_SURFACE_HI;
    }
    if (pressed) { // 按下态：面色回退一档
        if (fill == UI_SURFACE_HI) fill = UI_SURFACE;
        if (fill == UI_ACCENT_HI) fill = UI_ACCENT;
    }

    HBRUSH fillb = CreateSolidBrush(fill);
    HBRUSH oldb = (HBRUSH)SelectObject(dc, fillb);
    HPEN pen = CreatePen(PS_SOLID, 1, border);
    HPEN oldp = (HPEN)SelectObject(dc, pen);
    int r = appkit_scale_for_dpi(6.0);
    RoundRect(dc, rc->left, rc->top, rc->right, rc->bottom, r * 2, r * 2);
    SelectObject(dc, oldp);
    DeleteObject(pen);
    SelectObject(dc, oldb);
    DeleteObject(fillb);

    SetBkMode(dc, TRANSPARENT);
    SetTextColor(dc, text);
    HFONT f = (HFONT)SendMessageW(item, WM_GETFONT, 0, 0);
    HGDIOBJ of = SelectObject(dc, f != NULL ? f : g_font_cache[0]);
    RECT tr = *rc;
    wchar_t title[256];
    GetWindowTextW(item, title, 256);
    DrawTextW(dc, title, -1, &tr, DT_CENTER | DT_VCENTER | DT_SINGLELINE | DT_NOPREFIX);
    SelectObject(dc, of);
}

// appkit_draw_item: 深色自绘控件统一绘制入口（bubble 窗口 wndproc extern 引用）。
void appkit_draw_item(HWND item, int id, UINT state, HDC dc, const RECT *rc) {
    if (item == NULL || dc == NULL || rc == NULL) return;
    if (id >= BTN_TAG_CHK_BASE && id <= BTN_TAG_CHK_END) {
        appkit_draw_checkbox(item, state, dc, rc);
    } else {
        appkit_draw_button(item, id, state, dc, rc);
    }
}

// ---------- 控件定位（AppKit 左下原点 → Win32 客户区左上原点） ----------
// 控件逻辑坐标与 darwin 一致（父容器左下原点、y 向上），统一在此翻转。
static void appkit_position_control(HWND hwnd, double x, double y, double w, double h) {
    HWND parent = GetParent(hwnd);
    if (parent == NULL) return;
    RECT rc;
    GetClientRect(parent, &rc);
    int wx = (int)x;
    int wy = rc.bottom - (int)(y + h);
    MoveWindow(hwnd, wx, wy, (int)w, (int)h, TRUE);
}

static void appkit_set_font(HWND hwnd, HFONT font) {
    SendMessageW(hwnd, WM_SETFONT, (WPARAM)font, TRUE);
}

// ---------- 控件创建 ----------
// 创建时不可见、位于 (0,0)；AddSubview（SetParent）时按逻辑坐标定位显示。
static HWND appkit_create_textfield(double x, double y, double w, double h,
                                    const char *text, int is_secure) {
    wchar_t *wt = appkit_utf8_to_wcs(text);
    // 无 CLIENTEDGE 凹陷边框：v6 主题下 EDIT 自带扁平 1px 描边，配合深色 CTLCOLOR。
    HWND hwnd = CreateWindowExW(0, L"EDIT", wt ? wt : L"",
        WS_CHILD | WS_TABSTOP | ES_AUTOHSCROLL | (is_secure ? ES_PASSWORD : 0),
        0, 0, (int)w, (int)h, g_msg_hwnd, NULL, g_hinst, NULL);
    if (wt != NULL) free(wt);
    appkit_set_font(hwnd, g_font_cache[0]);
    return hwnd;
}

static HWND appkit_create_label(double x, double y, double w, double h, const char *text) {
    wchar_t *wt = appkit_utf8_to_wcs(text);
    HWND hwnd = CreateWindowExW(0, L"STATIC", wt ? wt : L"",
        WS_CHILD | SS_RIGHT | SS_CENTERIMAGE, 0, 0, (int)w, (int)h,
        g_msg_hwnd, NULL, g_hinst, NULL);
    if (wt != NULL) free(wt);
    appkit_set_font(hwnd, g_font_cache[0]);
    return hwnd;
}

static HWND appkit_create_button(double x, double y, double w, double h,
                                 const char *title, int tag, int is_default) {
    wchar_t *wt = appkit_utf8_to_wcs(title);
    // BS_OWNERDRAW：深色自绘（WM_DRAWITEM 由父窗口转发 appkit_draw_item）；
    // comctl32 v6 下自绘按钮自动获得 ODS_HOTLIGHT 悬停态。
    HWND hwnd = CreateWindowExW(0, L"BUTTON", wt ? wt : L"",
        WS_CHILD | WS_TABSTOP | BS_OWNERDRAW | (is_default ? BS_DEFPUSHBUTTON : BS_PUSHBUTTON),
        0, 0, (int)w, (int)h, g_msg_hwnd, (HMENU)(INT_PTR)tag, g_hinst, NULL);
    if (wt != NULL) free(wt);
    appkit_set_font(hwnd, g_font_cache[0]);
    return hwnd;
}

// 下拉框：Provider（只读）用 CBS_DROPDOWNLIST，其余用 CBS_DROPDOWN 可编辑。
// 高度参数是下拉展开高度；定位（翻转）仍用逻辑 h。
// DarkMode_Explorer 主题使下拉箭头/滚动条深色化（老系统调用失败静默忽略）。
static HWND appkit_create_popup(double x, double y, double w, double h, long tag) {
    DWORD style = WS_CHILD | WS_VSCROLL | WS_TABSTOP |
        (tag == BTN_TAG_POPUP_PROVIDER ? CBS_DROPDOWNLIST : CBS_DROPDOWN);
    HWND hwnd = CreateWindowExW(0, L"COMBOBOX", L"",
        style, 0, 0, (int)w, (int)(h * 6), g_msg_hwnd, (HMENU)(INT_PTR)tag, g_hinst, NULL);
    SetWindowTheme(hwnd, L"DarkMode_Explorer", NULL);
    appkit_set_font(hwnd, g_font_cache[0]);
    return hwnd;
}

static void appkit_add_subview(HWND parent, HWND child,
                               double x, double y, double w, double h) {
    SetParent(child, parent);
    appkit_position_control(child, x, y, w, h);
    ShowWindow(child, SW_SHOW);
}

static void appkit_control_set_frame(HWND child, double x, double y, double w, double h) {
    appkit_position_control(child, x, y, w, h);
}

static void appkit_control_set_hidden(HWND child, int hidden) {
    ShowWindow(child, hidden ? SW_HIDE : SW_SHOW);
}

static void appkit_textfield_get(HWND hwnd, char *out, int cap) {
    wchar_t buf[8192];
    int n = GetWindowTextW(hwnd, buf, 8192);
    if (n <= 0) {
        out[0] = '\0';
        return;
    }
    int m = WideCharToMultiByte(CP_UTF8, 0, buf, n, NULL, 0, NULL, NULL);
    if (m <= 0) {
        out[0] = '\0';
        return;
    }
    if (m >= cap) m = cap - 1;
    WideCharToMultiByte(CP_UTF8, 0, buf, n, out, m, NULL, NULL);
    out[m] = '\0';
}

static void appkit_textfield_set(HWND hwnd, const char *s) {
    wchar_t *ws = appkit_utf8_to_wcs(s);
    if (ws != NULL) {
        SetWindowTextW(hwnd, ws);
        free(ws);
    }
}

static void appkit_textfield_set_editable(HWND hwnd, int editable) {
    SendMessageW(hwnd, EM_SETREADONLY, editable ? FALSE : TRUE, 0);
}

static void appkit_popup_add_item(HWND hwnd, const char *title) {
    wchar_t *wt = appkit_utf8_to_wcs(title);
    if (wt != NULL) {
        SendMessageW(hwnd, CB_ADDSTRING, 0, (LPARAM)wt);
        free(wt);
    }
}

static void appkit_popup_remove_all(HWND hwnd) {
    SendMessageW(hwnd, CB_RESETCONTENT, 0, 0);
}

// 下拉框只读化：CBS_DROPDOWN → CBS_DROPDOWNLIST（样式位切换后强制重绘）。
static void appkit_popup_set_editable(HWND hwnd, int editable) {
    LONG style = (LONG)GetWindowLongPtrW(hwnd, GWL_STYLE);
    LONG newstyle = editable ? ((style & ~(LONG)0x3) | CBS_DROPDOWN)
                             : ((style & ~(LONG)0x3) | CBS_DROPDOWNLIST);
    if (newstyle != style) {
        SetWindowLongPtrW(hwnd, GWL_STYLE, newstyle);
        SetWindowPos(hwnd, NULL, 0, 0, 0, 0,
            SWP_NOMOVE | SWP_NOSIZE | SWP_NOZORDER | SWP_FRAMECHANGED);
    }
}

static int appkit_popup_index(HWND hwnd) {
    return (int)SendMessageW(hwnd, CB_GETCURSEL, 0, 0);
}

static void appkit_popup_select(HWND hwnd, int idx) {
    SendMessageW(hwnd, CB_SETCURSEL, (WPARAM)idx, 0);
}

// 下拉框文本：DROPDOWN 取编辑框内容（含手输），DROPDOWNLIST 取选中项文本。
static void appkit_popup_get(HWND hwnd, char *out, int cap) {
    LONG style = (LONG)GetWindowLongPtrW(hwnd, GWL_STYLE);
    if ((style & 0x3) == CBS_DROPDOWN) {
        appkit_textfield_get(hwnd, out, cap);
        return;
    }
    int idx = appkit_popup_index(hwnd);
    if (idx < 0) {
        out[0] = '\0';
        return;
    }
    int n = (int)SendMessageW(hwnd, CB_GETLBTEXTLEN, (WPARAM)idx, 0);
    if (n <= 0) {
        out[0] = '\0';
        return;
    }
    wchar_t *buf = (wchar_t *)malloc(sizeof(wchar_t) * (size_t)(n + 1));
    if (buf == NULL) {
        out[0] = '\0';
        return;
    }
    SendMessageW(hwnd, CB_GETLBTEXT, (WPARAM)idx, (LPARAM)buf);
    int m = WideCharToMultiByte(CP_UTF8, 0, buf, n, NULL, 0, NULL, NULL);
    if (m >= cap) m = cap - 1;
    WideCharToMultiByte(CP_UTF8, 0, buf, n, out, m, NULL, NULL);
    out[m] = '\0';
    free(buf);
}

// 下拉框回显文本：可编辑直接 WM_SETTEXT；只读在列表内匹配选中（无匹配忽略）。
static void appkit_popup_set(HWND hwnd, const char *s) {
    LONG style = (LONG)GetWindowLongPtrW(hwnd, GWL_STYLE);
    wchar_t *ws = appkit_utf8_to_wcs(s);
    if (ws == NULL) return;
    if ((style & 0x3) == CBS_DROPDOWN) {
        SetWindowTextW(hwnd, ws);
    } else {
        int idx = (int)SendMessageW(hwnd, CB_SELECTSTRING, (WPARAM)-1, (LPARAM)ws);
        (void)idx;
    }
    free(ws);
}

static void appkit_button_set_frame(HWND hwnd, double x, double y, double w, double h) {
    appkit_position_control(hwnd, x, y, w, h);
}

static void appkit_button_set_title(HWND hwnd, const char *title) {
    wchar_t *wt = appkit_utf8_to_wcs(title);
    if (wt != NULL) {
        SetWindowTextW(hwnd, wt);
        free(wt);
    }
}

// 标签对齐：SS_RIGHT/SS_CENTER/SS_LEFT（2 右/1 中/0 左）。
static void appkit_label_set_alignment(HWND hwnd, long align) {
    LONG s = (LONG)GetWindowLongPtrW(hwnd, GWL_STYLE);
    LONG ns = s & ~(LONG)(SS_RIGHT | SS_CENTER | SS_LEFT);
    if (align == 2) ns |= SS_RIGHT;
    else if (align == 1) ns |= SS_CENTER;
    else ns |= SS_LEFT;
    if (ns != s) {
        SetWindowLongPtrW(hwnd, GWL_STYLE, ns);
        SetWindowPos(hwnd, NULL, 0, 0, 0, 0,
            SWP_NOMOVE | SWP_NOSIZE | SWP_NOZORDER | SWP_FRAMECHANGED);
    }
}

static void appkit_button_set_state(HWND hwnd, int on) {
    // 自绘按钮无 BM_SETCHECK 语义：勾选态统一走 C 侧状态表（自绘时读取）。
    appkit_set_state(hwnd, on);
}

// 页签风格（对应 darwin NSBezelStyleInline=15）：切换为自绘分段样式
// （BS_PUSHLIKE 保留自绘位，SetState 驱动选中高亮）。其余值忽略。
static void appkit_button_set_bezel_style(HWND hwnd, long style) {
    if (style != 15) return;
    LONG s = (LONG)GetWindowLongPtrW(hwnd, GWL_STYLE);
    LONG ns = (s & ~(LONG)(BS_TYPEMASK | BS_PUSHBUTTON)) | BS_AUTOCHECKBOX | BS_PUSHLIKE | BS_OWNERDRAW;
    if (ns != s) {
        SetWindowLongPtrW(hwnd, GWL_STYLE, ns);
        SetWindowPos(hwnd, NULL, 0, 0, 0, 0,
            SWP_NOMOVE | SWP_NOSIZE | SWP_NOZORDER | SWP_FRAMECHANGED);
    }
}

static void appkit_button_set_font_size(HWND hwnd, double pt) {
    int idx = (int)(pt + 0.5);
    if (idx < 0 || idx >= 64) return;
    if (g_font_cache[idx] == NULL) {
        int px = -MulDiv(idx, appkit_dpi(), 72);
        g_font_cache[idx] = CreateFontW(px, 0, 0, 0, FW_NORMAL, 0, 0, 0,
            DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
            CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Segoe UI");
        if (g_font_cache[idx] == NULL) return;
    }
    appkit_set_font(hwnd, g_font_cache[idx]);
}

// ---------- 复选开关 ----------
// 设置窗口功能开关：BS_OWNERDRAW 自绘（不自动切换勾选态），点击经 WM_COMMAND
// 翻转 C 侧状态表，Save 时读 state。控件 ID 自动分配（CHK 段，供 WM_COMMAND 识别）。
static int g_chk_id_next = BTN_TAG_CHK_BASE;

static HWND appkit_create_checkbox(double x, double y, double w, double h, const char *title) {
    wchar_t *wt = appkit_utf8_to_wcs(title);
    int id = g_chk_id_next++;
    if (id > BTN_TAG_CHK_END) id = BTN_TAG_CHK_BASE;
    HWND hwnd = CreateWindowExW(0, L"BUTTON", wt ? wt : L"",
        WS_CHILD | WS_TABSTOP | BS_OWNERDRAW,
        0, 0, (int)w, (int)h, g_msg_hwnd, (HMENU)(INT_PTR)id, g_hinst, NULL);
    if (wt != NULL) free(wt);
    appkit_set_font(hwnd, g_font_cache[0]);
    return hwnd;
}

static int appkit_checkbox_state(HWND hwnd) {
    return appkit_get_state(hwnd);
}

static void appkit_checkbox_set_state(HWND hwnd, int on) {
    appkit_set_state(hwnd, on);
}

// ---------- 窗口 ----------
static HWND appkit_create_window(double w, double h, const char *title) {
    wchar_t *wt = appkit_utf8_to_wcs(title);
    RECT rc = {0, 0, (int)w, (int)h};
    AdjustWindowRectEx(&rc, WS_OVERLAPPED | WS_CAPTION | WS_SYSMENU | WS_MINIMIZEBOX,
        FALSE, 0);
    HWND hwnd = CreateWindowExW(0, L"ElfinWindow", wt ? wt : L"",
        WS_OVERLAPPED | WS_CAPTION | WS_SYSMENU | WS_MINIMIZEBOX,
        CW_USEDEFAULT, CW_USEDEFAULT, rc.right - rc.left, rc.bottom - rc.top,
        NULL, NULL, g_hinst, NULL);
    if (wt != NULL) free(wt);
    // 深色标题栏（Win10 1809+；老系统调用失败静默忽略，回退经典标题栏）。
    BOOL dark = TRUE;
    DwmSetWindowAttribute(hwnd, DWMWA_USE_IMMERSIVE_DARK_MODE, &dark, sizeof(dark));
    return hwnd;
}

static void appkit_window_show(HWND hwnd) {
    // 记录 Show 前的上一个前台窗口，供 Deactivate 归还焦点。
    HWND fg = GetForegroundWindow();
    if (fg != NULL && fg != hwnd) {
        g_prev_foreground = fg;
    }
    ShowWindow(hwnd, SW_SHOW);
    SetForegroundWindow(hwnd);
    SetFocus(hwnd);
}

// appkit_center_on_mouse_screen: 居中到鼠标所在屏幕的工作区（无匹配退回主屏）。
static void appkit_center_on_mouse_screen(HWND hwnd) {
    POINT p;
    GetCursorPos(&p);
    HMONITOR mon = MonitorFromPoint(p, MONITOR_DEFAULTTONEAREST);
    MONITORINFO mi;
    memset(&mi, 0, sizeof(mi));
    mi.cbSize = sizeof(mi);
    GetMonitorInfoW(mon, &mi);
    RECT work = mi.rcWork;
    RECT rc;
    GetWindowRect(hwnd, &rc);
    int w = rc.right - rc.left;
    int h = rc.bottom - rc.top;
    int x = work.left + ((work.right - work.left) - w) / 2;
    int y = work.top + ((work.bottom - work.top) - h) / 2;
    SetWindowPos(hwnd, NULL, x, y, 0, 0, SWP_NOSIZE | SWP_NOZORDER);
}

static void appkit_window_close(HWND hwnd) {
    ShowWindow(hwnd, SW_HIDE);
}

static void appkit_window_orderout(HWND hwnd) {
    ShowWindow(hwnd, SW_HIDE);
}

static int appkit_window_visible(HWND hwnd) {
    return IsWindowVisible(hwnd) ? 1 : 0;
}

static void appkit_window_release(HWND hwnd) {
    DestroyWindow(hwnd);
}

// appkit_app_deactivate: 把焦点还给设置窗口显示前的应用。
static void appkit_app_deactivate(void) {
    if (g_prev_foreground != NULL && IsWindow(g_prev_foreground)) {
        SetForegroundWindow(g_prev_foreground);
    }
    g_prev_foreground = NULL;
}

static void appkit_make_first_responder(HWND parent, HWND field) {
    (void)parent;
    SetFocus(field);
}

// ---------- 全局快捷键（Ctrl+Shift+, 打开设置） ----------
static int appkit_register_hotkey(void) {
    return RegisterHotKey(NULL, 1, MOD_CONTROL | MOD_SHIFT, VK_OEM_COMMA) ? 1 : 0;
}

// ---------- 托盘（Shell_NotifyIcon + TrackPopupMenu） ----------
#define WM_APP_TRAYICON (WM_APP + 1)

static HICON g_tray_icon;

// appkit_tray_icon_from_bgra: 用内嵌 BGRA 像素流（packaging/gen_icons.py 生成的
// 对勾气泡剪影，32x32 自顶向下）建 32bpp ARGB DIB 并创建 HICON，
// 与 darwin 版内嵌 NSImage 同思路，无需外部资源文件。
static HICON appkit_tray_icon_from_bgra(const unsigned char *bgra, int w, int h) {
    if (bgra == NULL || w <= 0 || h <= 0) return NULL;
    BITMAPV5HEADER bi;
    memset(&bi, 0, sizeof(bi));
    bi.bV5Size = sizeof(bi);
    bi.bV5Width = w;
    bi.bV5Height = -h; // 自顶向下
    bi.bV5Planes = 1;
    bi.bV5BitCount = 32;
    bi.bV5Compression = BI_RGB;
    BYTE *bits = NULL;
    HDC hdc = GetDC(NULL);
    HBITMAP color = CreateDIBSection(hdc, (BITMAPINFO *)&bi, DIB_RGB_COLORS,
        (void **)&bits, NULL, 0);
    ReleaseDC(NULL, hdc);
    if (color == NULL || bits == NULL) {
        if (color != NULL) DeleteObject(color);
        return NULL;
    }
    memcpy(bits, bgra, (size_t)w * h * 4);
    ICONINFO ii;
    memset(&ii, 0, sizeof(ii));
    ii.fIcon = TRUE;
    ii.hbmColor = color;
    ii.hbmMask = CreateBitmap(w, h, 1, 1, NULL);
    HICON icon = CreateIconIndirect(&ii);
    DeleteObject(color);
    if (ii.hbmMask != NULL) DeleteObject(ii.hbmMask);
    return icon;
}

// appkit_status_init: 托盘图标 + 右键菜单（Settings/User Guide/Exit）。
static int appkit_status_init(const unsigned char *bgra, int len) {
    if (g_tray_icon != NULL) return 1;
    if (bgra == NULL || len != 32 * 32 * 4) return 0;
    g_tray_icon = appkit_tray_icon_from_bgra(bgra, 32, 32);
    if (g_tray_icon == NULL) return 0;
    NOTIFYICONDATAW nid;
    memset(&nid, 0, sizeof(nid));
    nid.cbSize = sizeof(nid);
    nid.hWnd = g_msg_hwnd;
    nid.uID = 1;
    nid.uFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP;
    nid.uCallbackMessage = WM_APP_TRAYICON;
    nid.hIcon = g_tray_icon;
    wcscpy(nid.szTip, L"elfin-grammar");
    return Shell_NotifyIconW(NIM_ADD, &nid) ? 1 : 0;
}

// appkit_msg_wndproc: message-only 窗口过程（托盘/热键宿主）。
static LRESULT CALLBACK appkit_msg_wndproc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
    switch (msg) {
    case WM_APP_TRAYICON: {
        if (lParam == WM_RBUTTONUP || lParam == WM_LBUTTONUP) {
            HMENU menu = CreatePopupMenu();
            AppendMenuW(menu, MF_STRING, BTN_TAG_STATUS_SETTINGS, L"Settings");
            AppendMenuW(menu, MF_STRING, BTN_TAG_STATUS_GUIDE, L"User Guide");
            AppendMenuW(menu, MF_STRING, BTN_TAG_STATUS_EXIT, L"Exit");
            POINT pt;
            GetCursorPos(&pt);
            SetForegroundWindow(hwnd); // TrackPopupMenu 前置要求
            int cmd = TrackPopupMenu(menu,
                TPM_RIGHTBUTTON | TPM_RETURNCMD | TPM_NONOTIFY,
                pt.x, pt.y, 0, hwnd, NULL);
            DestroyMenu(menu);
            PostMessageW(hwnd, WM_NULL, 0, 0); // 已知菜单关闭流程
            if (cmd != 0) {
                appkit_set_flags(cmd);
            }
        }
        break;
    }
    }
    return DefWindowProcW(hwnd, msg, wParam, lParam);
}

// ---------- 剪贴板 ----------
// g_pb_self_written: 本进程最近一次写入剪贴板的文本。读取时若与之一致则拦截，
// 防止自身写入（Apply 兜底粘贴等）回环触发剪贴板监视（与 darwin 相同机制）。
static char *g_pb_self_written;

static void appkit_copy_to_pasteboard(const char *s) {
    if (s == NULL) return;
    if (!OpenClipboard(g_msg_hwnd)) return;
    EmptyClipboard();
    wchar_t *ws = appkit_utf8_to_wcs(s);
    if (ws != NULL) {
        size_t bytes = (wcslen(ws) + 1) * sizeof(wchar_t);
        HGLOBAL hmem = GlobalAlloc(GMEM_MOVEABLE, bytes);
        if (hmem != NULL) {
            void *dst = GlobalLock(hmem);
            if (dst != NULL) {
                memcpy(dst, ws, bytes);
                GlobalUnlock(hmem);
                SetClipboardData(CF_UNICODETEXT, hmem);
            } else {
                GlobalFree(hmem);
            }
        }
        free(ws);
    }
    CloseClipboard();
    free(g_pb_self_written);
    g_pb_self_written = _strdup(s);
}

// appkit_pasteboard_change_count: 返回剪贴板序列号（任何写入自增）。
static long appkit_pasteboard_change_count(void) {
    return (long)GetClipboardSequenceNumber();
}

// appkit_pasteboard_string: 读取剪贴板纯文本（malloc 的 UTF-8，调用者 free）。
// 非文本内容与自身最近一次写入返回 NULL（防自身写入回环触发）。
static char *appkit_pasteboard_string(void) {
    if (!IsClipboardFormatAvailable(CF_UNICODETEXT)) return NULL;
    if (!OpenClipboard(g_msg_hwnd)) return NULL;
    char *out = NULL;
    HANDLE h = GetClipboardData(CF_UNICODETEXT);
    if (h != NULL) {
        const wchar_t *ws = (const wchar_t *)GlobalLock(h);
        if (ws != NULL) {
            out = appkit_wcs_to_utf8(ws, (int)wcslen(ws));
            GlobalUnlock(h);
        }
    }
    CloseClipboard();
    if (out != NULL && g_pb_self_written != NULL &&
        strcmp(out, g_pb_self_written) == 0) {
        free(out);
        out = NULL;
    }
    return out;
}

// appkit_paste_text: 写剪贴板 + SendInput Ctrl+V（配合 AX 预先选中的区间替换）。
static void appkit_paste_text(const char *s) {
    appkit_copy_to_pasteboard(s);
    INPUT inputs[4];
    memset(inputs, 0, sizeof(inputs));
    inputs[0].type = INPUT_KEYBOARD;
    inputs[0].ki.wVk = VK_CONTROL;
    inputs[1].type = INPUT_KEYBOARD;
    inputs[1].ki.wVk = 'V';
    inputs[2].type = INPUT_KEYBOARD;
    inputs[2].ki.wVk = 'V';
    inputs[2].ki.dwFlags = KEYEVENTF_KEYUP;
    inputs[3].type = INPUT_KEYBOARD;
    inputs[3].ki.wVk = VK_CONTROL;
    inputs[3].ki.dwFlags = KEYEVENTF_KEYUP;
    SendInput(4, inputs, sizeof(INPUT));
}

// ---------- 打开 URL 与二维码（内置 LLM 登录区） ----------
// appkit_open_url: ShellExecute 在默认浏览器打开 URL（注册/友情捐献链接）。
static void appkit_open_url(const char *url) {
    if (url == NULL || url[0] == '\0') return;
    wchar_t *wurl = appkit_utf8_to_wcs(url);
    if (wurl == NULL) return;
    ShellExecuteW(NULL, L"open", wurl, NULL, NULL, SW_SHOWNORMAL);
    free(wurl);
}

// ---------- 图像视图（二维码，ElfinImage 子窗口） ----------
typedef struct {
    int w, h;
    BYTE *pixels; // 32bpp BGRA，自上而下
} elfin_image;

static void appkit_image_view_set_pixels(HWND hwnd, const void *pixels, int pw, int ph);

static int g_image_class_registered;

static LRESULT CALLBACK appkit_image_wndproc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
    switch (msg) {
    case WM_PAINT: {
        PAINTSTRUCT ps;
        HDC dc = BeginPaint(hwnd, &ps);
        elfin_image *img = (elfin_image *)GetWindowLongPtrW(hwnd, GWLP_USERDATA);
        RECT rc;
        GetClientRect(hwnd, &rc);
        HBRUSH bg = CreateSolidBrush(UI_BG);
        FillRect(dc, &rc, bg);
        DeleteObject(bg);
        if (img != NULL && img->pixels != NULL && img->w > 0 && img->h > 0) {
            BITMAPINFO bi;
            memset(&bi, 0, sizeof(bi));
            bi.bmiHeader.biSize = sizeof(BITMAPINFOHEADER);
            bi.bmiHeader.biWidth = img->w;
            bi.bmiHeader.biHeight = -img->h; // 自顶向下
            bi.bmiHeader.biPlanes = 1;
            bi.bmiHeader.biBitCount = 32;
            bi.bmiHeader.biCompression = BI_RGB;
            SetStretchBltMode(dc, COLORONCOLOR);
            StretchDIBits(dc, 0, 0, rc.right - rc.left, rc.bottom - rc.top,
                0, 0, img->w, img->h, img->pixels, &bi, DIB_RGB_COLORS, SRCCOPY);
        }
        EndPaint(hwnd, &ps);
        return 0;
    }
    case WM_NCDESTROY: {
        elfin_image *img = (elfin_image *)GetWindowLongPtrW(hwnd, GWLP_USERDATA);
        if (img != NULL) {
            free(img->pixels);
            free(img);
        }
        break;
    }
    }
    return DefWindowProcW(hwnd, msg, wParam, lParam);
}

static void appkit_ensure_image_class(void) {
    if (g_image_class_registered) return;
    WNDCLASSEXW wc;
    memset(&wc, 0, sizeof(wc));
    wc.cbSize = sizeof(wc);
    wc.lpfnWndProc = appkit_image_wndproc;
    wc.hInstance = g_hinst;
    wc.hbrBackground = NULL;
    wc.lpszClassName = L"ElfinImage";
    RegisterClassExW(&wc);
    g_image_class_registered = 1;
}

// appkit_create_image_view: 创建二维码子窗口并缓存像素位图。
static HWND appkit_create_image_view(double x, double y, double w, double h,
                                     const void *pixels, int pw, int ph) {
    appkit_ensure_image_class();
    HWND hwnd = CreateWindowExW(0, L"ElfinImage", L"",
        WS_CHILD, 0, 0, (int)w, (int)h, g_msg_hwnd, NULL, g_hinst, NULL);
    if (hwnd != NULL) {
        appkit_image_view_set_pixels(hwnd, pixels, pw, ph);
    }
    return hwnd;
}

// appkit_image_view_set_pixels: 替换图像像素并重绘（Go 侧生成 RGBA 后传入）。
static void appkit_image_view_set_pixels(HWND hwnd, const void *pixels, int pw, int ph) {
    if (hwnd == NULL) return;
    elfin_image *img = (elfin_image *)GetWindowLongPtrW(hwnd, GWLP_USERDATA);
    if (img == NULL) {
        img = (elfin_image *)calloc(1, sizeof(elfin_image));
        if (img == NULL) return;
        SetWindowLongPtrW(hwnd, GWLP_USERDATA, (LONG_PTR)img);
    }
    free(img->pixels);
    img->pixels = NULL;
    img->w = pw;
    img->h = ph;
    if (pixels != NULL && pw > 0 && ph > 0) {
        size_t bytes = (size_t)pw * (size_t)ph * 4;
        img->pixels = (BYTE *)malloc(bytes);
        if (img->pixels != NULL) {
            memcpy(img->pixels, pixels, bytes);
        }
    }
    InvalidateRect(hwnd, NULL, TRUE);
}

// ---------- 全局输入活动监控（低级键盘/鼠标钩子） ----------
static volatile int g_input_flag;
static HHOOK g_kb_hook;
static HHOOK g_ms_hook;

static LRESULT CALLBACK appkit_kb_proc(int code, WPARAM wParam, LPARAM lParam) {
    (void)wParam;
    (void)lParam;
    if (code == HC_ACTION) {
        g_input_flag = 1;
    }
    return CallNextHookEx(g_kb_hook, code, wParam, lParam);
}

static LRESULT CALLBACK appkit_ms_proc(int code, WPARAM wParam, LPARAM lParam) {
    (void)lParam;
    if (code == HC_ACTION) {
        switch (wParam) {
        case WM_LBUTTONDOWN:
        case WM_RBUTTONDOWN:
        case WM_MBUTTONDOWN:
            g_input_flag = 1;
            break;
        }
    }
    return CallNextHookEx(g_ms_hook, code, wParam, lParam);
}

// 低级钩子回调在注册线程的 PeekMessage/DispatchMessage 期间执行（runloop 泵驱动）。
static int appkit_init_input_monitor(void) {
    if (g_kb_hook != NULL) return 1;
    g_kb_hook = SetWindowsHookExW(WH_KEYBOARD_LL, appkit_kb_proc, g_hinst, 0);
    g_ms_hook = SetWindowsHookExW(WH_MOUSE_LL, appkit_ms_proc, g_hinst, 0);
    return (g_kb_hook != NULL || g_ms_hook != NULL) ? 1 : 0;
}

// appkit_input_activity: 返回并清除输入活动标志。
static int appkit_input_activity(void) {
    int f = g_input_flag;
    g_input_flag = 0;
    return f;
}

// ---------- 事件轮询 ----------
static int appkit_poll_events(void) {
    int f = g_event_flags;
    g_event_flags = 0;
    return f;
}

// ---------- 应用初始化 ----------
static void appkit_init_application(void) {
    if (g_msg_hwnd != NULL) return;
    g_hinst = GetModuleHandleW(NULL);
    appkit_enable_dpi();
    INITCOMMONCONTROLSEX icc;
    icc.dwSize = sizeof(icc);
    icc.dwICC = ICC_STANDARD_CLASSES;
    InitCommonControlsEx(&icc);

    // 默认 GUI 字体：13pt Segoe UI（按实际 DPI 缩放，与气泡一致）。
    g_font_cache[0] = CreateFontW(-MulDiv(13, appkit_dpi(), 72), 0, 0, 0,
        FW_NORMAL, 0, 0, 0,
        DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
        CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Segoe UI");

    // 深色画刷：窗口类背景与 CTLCOLOR 共用。
    g_ui_bg_brush = CreateSolidBrush(UI_BG);
    g_ui_surface_brush = CreateSolidBrush(UI_SURFACE);

    // 设置窗口类（深色背景）。
    WNDCLASSEXW wc;
    memset(&wc, 0, sizeof(wc));
    wc.cbSize = sizeof(wc);
    wc.lpfnWndProc = appkit_wndproc;
    wc.hInstance = g_hinst;
    wc.hCursor = LoadCursorW(NULL, (LPCWSTR)(ULONG_PTR)IDC_ARROW);
    wc.hbrBackground = g_ui_bg_brush;
    wc.lpszClassName = L"ElfinWindow";
    RegisterClassExW(&wc);

    // message-only 窗口类（托盘/热键宿主）
    wc.lpfnWndProc = appkit_msg_wndproc;
    wc.hbrBackground = NULL;
    wc.lpszClassName = L"ElfinMsgWindow";
    RegisterClassExW(&wc);

    g_msg_hwnd = CreateWindowExW(0, L"ElfinMsgWindow", L"",
        WS_OVERLAPPED, 0, 0, 0, 0, HWND_MESSAGE, NULL, g_hinst, NULL);
}
*/
import "C"

import (
	"unsafe"

	"github.com/skip2/go-qrcode"
)

// Handle 是 Win32 窗口/控件的句柄封装。
// x/y/w/h 保存 AppKit 风格逻辑坐标（父容器左下原点、y 向上），
// 与 darwin 版调用方（settings/bubble 布局代码）的坐标语义一致，
// 由 C 侧 appkit_position_control 统一翻转为 Win32 左上原点。
type Handle struct {
	hwnd   C.HWND
	x, y   C.double
	w, h   C.double
	parent *Handle
	hidden bool
}

// Window 是配置窗口句柄。
type Window Handle

// TextField 是文本框句柄。
type TextField Handle

// Label 是标签句柄。
type Label Handle

// Button 是按钮句柄。
type Button Handle

// Popup 是下拉框句柄。
type Popup Handle

// ImageView 是图像视图句柄（内置 LLM 登录区二维码展示；windows stub 无实际图像）。
type ImageView Handle

// Checkbox 是复选开关句柄（设置窗口功能开关）。
type Checkbox Handle

// cString 把 Go string 转为 C 字符串（调用方负责 free）。
func cString(s string) *C.char {
	return C.CString(s)
}

// toGo 把 malloc 的 C 字符串转为 Go string 并释放。
func toGo(p *C.char) string {
	if p == nil {
		return ""
	}
	s := C.GoString(p)
	C.free(unsafe.Pointer(p))
	return s
}

// InitApplication 初始化 Win32 环境：DPI 感知、公共控件、窗口类与
// message-only 宿主窗口（托盘/热键）。进程内仅一次，必须在主线程调用。
func InitApplication() {
	C.appkit_init_application()
}

// Deactivate 归还焦点：把前台窗口还给设置窗口显示前的应用。
// 必须在主线程调用。
func Deactivate() {
	C.appkit_app_deactivate()
}

// ConfirmDialog 弹出模态确认对话框（阻塞直到用户点击）。
// title 为标题，message 为正文，accept/decline 分别为第一/第二按钮文案。
// 返回 true 表示点击 accept。必须在主线程调用。
func ConfirmDialog(title, message, accept, decline string) bool {
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	cm := C.CString(message)
	defer C.free(unsafe.Pointer(cm))
	ca := C.CString(accept)
	defer C.free(unsafe.Pointer(ca))
	cd := C.CString(decline)
	defer C.free(unsafe.Pointer(cd))
	return C.appkit_confirm_dialog(ct, cm, ca, cd) != 0
}

// RegisterHotkey 注册全局快捷键 Ctrl+Shift+, 打开设置窗口。
// 返回是否注册成功（抢占失败时失败）。必须在主线程调用。
func RegisterHotkey() bool {
	return C.appkit_register_hotkey() != 0
}

// InitStatusBar 创建托盘图标与右键菜单（Settings/Exit）。
// 菜单项点击通过事件标志位 EventStatusSettings/EventStatusExit 上报
// （由 PollEvents 轮询分发）。进程内仅一次，必须在主线程调用。
func InitStatusBar() bool {
	bgra := statusIconBGRA32()
	if len(bgra) == 0 {
		return false
	}
	return C.appkit_status_init((*C.uchar)(unsafe.Pointer(&bgra[0])), C.int(len(bgra))) != 0
}

// InitInputMonitor 安装全局输入活动监控（低级键盘/鼠标钩子：任意按键/
// 鼠标按下置标志）。钩子回调由 runloop 消息泵驱动。必须在主线程调用。
func InitInputMonitor() bool {
	return C.appkit_init_input_monitor() != 0
}

// ConsumeInputActivity 返回并清除输入活动标志（任意按键/鼠标点击）。必须在主线程调用。
func ConsumeInputActivity() bool {
	return C.appkit_input_activity() != 0
}

// PollEvents 轮询并清除按钮/快捷键事件标志。必须在主线程调用。
func PollEvents() EventFlags {
	return EventFlags(C.appkit_poll_events())
}

// PumpEvents 恒返回 false：Windows 的消息派发全部由 runloop 的
// PeekMessage/DispatchMessage 泵完成（与 darwin 的手动 nextEvent+sendEvent 对应），
// main.go 的调用方式保持不变（泵后 PumpEvents 空转）。
func PumpEvents() bool {
	return false
}

// NewHandle 包装外部已创建的窗口句柄（如 bubble 的气泡窗口），
// 使其可复用 AddSubview 等方法。必须在主线程调用。
func NewHandle(ptr unsafe.Pointer) *Window {
	return &Window{hwnd: C.HWND(ptr)}
}

// NewWindow 创建带标题栏的标准窗口（未显示）。w/h 为客户区尺寸
// （与 darwin 的 NSWindow 内容区域一致）。必须在主线程调用。
func NewWindow(w, h float64, title string) *Window {
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	hwnd := C.appkit_create_window(C.double(w), C.double(h), ct)
	win := &Window{hwnd: hwnd, w: C.double(w), h: C.double(h)}
	return win
}

// Show 显示窗口并激活（置前台）。窗口已可见但失焦时可再次调用重新激活。
// 必须在主线程调用。
func (w *Window) Show() {
	C.appkit_window_show(w.hwnd)
}

// CenterOnMouseScreen 将窗口居中到鼠标所在屏幕的工作区（无匹配时退回主屏）。
// 必须在主线程调用。
func (w *Window) CenterOnMouseScreen() {
	C.appkit_center_on_mouse_screen(w.hwnd)
}

// Close 关闭窗口（隐藏）。必须在主线程调用。
func (w *Window) Close() {
	C.appkit_window_close(w.hwnd)
}

// OrderOut 强制将窗口移出屏幕（close 未生效时的兜底）。必须在主线程调用。
func (w *Window) OrderOut() {
	C.appkit_window_orderout(w.hwnd)
}

// Visible 查询窗口是否可见。必须在主线程调用。
func (w *Window) Visible() bool {
	return C.appkit_window_visible(w.hwnd) != 0
}

// DumpWindowState 打印窗口真实屏幕状态（临时诊断用）。必须在主线程调用。
func (w *Window) DumpWindowState() {
	// Windows 版仅打印可见性（无 z-order 概念），保留接口供诊断调用。
	println("[dbg-window] visible=", w.Visible())
}

// Release 释放窗口句柄。
func (w *Window) Release() {
	C.appkit_window_release(w.hwnd)
	w.hwnd = nil
}

// AddSubview 将控件加入容器（SetParent + 按逻辑坐标定位显示）。
// 必须在主线程调用。
func (w *Window) AddSubview(view any) {
	parent := (*Handle)(w)
	switch v := view.(type) {
	case *TextField:
		h := (*Handle)(v)
		h.parent = parent
		C.appkit_add_subview(parent.hwnd, h.hwnd, h.x, h.y, h.w, h.h)
	case *Label:
		h := (*Handle)(v)
		h.parent = parent
		C.appkit_add_subview(parent.hwnd, h.hwnd, h.x, h.y, h.w, h.h)
	case *Button:
		h := (*Handle)(v)
		h.parent = parent
		C.appkit_add_subview(parent.hwnd, h.hwnd, h.x, h.y, h.w, h.h)
	case *Popup:
		h := (*Handle)(v)
		h.parent = parent
		C.appkit_add_subview(parent.hwnd, h.hwnd, h.x, h.y, h.w, h.h)
	case *ImageView:
		h := (*Handle)(v)
		if h.hwnd == nil { // stub 无实际控件，跳过
			return
		}
		h.parent = parent
		C.appkit_add_subview(parent.hwnd, h.hwnd, h.x, h.y, h.w, h.h)
	case *Checkbox:
		h := (*Handle)(v)
		h.parent = parent
		C.appkit_add_subview(parent.hwnd, h.hwnd, h.x, h.y, h.w, h.h)
	}
}

// MakeFirstResponder 设置初始焦点控件。必须在主线程调用。
func (w *Window) MakeFirstResponder(f *TextField) {
	C.appkit_make_first_responder(w.hwnd, f.hwnd)
}

// NewTextField 创建可编辑文本框。isSecure 为密码式（星号）输入。
func NewTextField(x, y, w, h float64, text string, isSecure bool) *TextField {
	ct := C.CString(text)
	defer C.free(unsafe.Pointer(ct))
	var sec C.int
	if isSecure {
		sec = 1
	}
	hwnd := C.appkit_create_textfield(C.double(x), C.double(y), C.double(w), C.double(h), ct, sec)
	return &TextField{hwnd: hwnd, x: C.double(x), y: C.double(y), w: C.double(w), h: C.double(h)}
}

// String 读取文本框内容。必须在主线程调用。
func (f *TextField) String() string {
	var buf [8192]C.char
	C.appkit_textfield_get(f.hwnd, &buf[0], 8192)
	return C.GoString(&buf[0])
}

// SetString 设置文本框内容。必须在主线程调用。
func (f *TextField) SetString(s string) {
	ct := C.CString(s)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_textfield_set(f.hwnd, ct)
}

// SetEditable 设置文本框是否可编辑：false 时 Base URL 由厂商固定（只读）。必须在主线程调用。
func (f *TextField) SetEditable(editable bool) {
	var e C.int
	if editable {
		e = 1
	}
	C.appkit_textfield_set_editable(f.hwnd, e)
}

// SetHidden 设置控件是否隐藏（内置 LLM 登录区随厂商切换显隐）。必须在主线程调用。
func (f *TextField) SetHidden(hidden bool) {
	f.hidden = hidden
	var h C.int
	if hidden {
		h = 1
	}
	C.appkit_control_set_hidden(f.hwnd, h)
}

// NewLabel 创建不可编辑右对齐标签。
func NewLabel(x, y, w, h float64, text string) *Label {
	ct := C.CString(text)
	defer C.free(unsafe.Pointer(ct))
	hwnd := C.appkit_create_label(C.double(x), C.double(y), C.double(w), C.double(h), ct)
	return &Label{hwnd: hwnd, x: C.double(x), y: C.double(y), w: C.double(w), h: C.double(h)}
}

// SetHidden 设置控件是否隐藏（内置 LLM 登录区随厂商切换显隐）。必须在主线程调用。
func (l *Label) SetHidden(hidden bool) {
	l.hidden = hidden
	var h C.int
	if hidden {
		h = 1
	}
	C.appkit_control_set_hidden(l.hwnd, h)
}

// SetAlignment 设置标签对齐：0 左对齐 / 1 居中 / 2 右对齐。必须在主线程调用。
func (l *Label) SetAlignment(align int) {
	C.appkit_label_set_alignment(l.hwnd, C.long(align))
}

// SetString 设置标签文本（提示行/余额回显）。必须在主线程调用。
func (l *Label) SetString(s string) {
	ct := C.CString(s)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_textfield_set(l.hwnd, ct)
}

// NewPopup 创建下拉框：Provider（tag=TagPopupProvider）为只读列表，
// 其余（模型下拉框）可编辑（可选预设项也可直接输入）。
// tag 区分事件来源：TagPopupProvider → EventProviderChanged，
// TagPopupModel → EventModelChanged（轮询 PollEvents 派发）。必须在主线程调用。
func NewPopup(x, y, w, h float64, tag int64) *Popup {
	hwnd := C.appkit_create_popup(C.double(x), C.double(y), C.double(w), C.double(h), C.long(tag))
	return &Popup{hwnd: hwnd, x: C.double(x), y: C.double(y), w: C.double(w), h: C.double(h)}
}

// AddItem 向下拉框追加选项。必须在主线程调用。
func (p *Popup) AddItem(title string) {
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_popup_add_item(p.hwnd, ct)
}

// RemoveAllItems 清空下拉框全部选项。必须在主线程调用。
func (p *Popup) RemoveAllItems() {
	C.appkit_popup_remove_all(p.hwnd)
}

// SetEditable 设置下拉框是否可输入：false 时转为只读列表（厂商下拉框）。
// 必须在主线程调用。
func (p *Popup) SetEditable(editable bool) {
	var e C.int
	if editable {
		e = 1
	}
	C.appkit_popup_set_editable(p.hwnd, e)
}

// Index 返回当前选中项下标（-1 表示无选中）。必须在主线程调用。
func (p *Popup) Index() int {
	return int(C.appkit_popup_index(p.hwnd))
}

// SelectIndex 选中指定下标项。必须在主线程调用。
func (p *Popup) SelectIndex(i int) {
	C.appkit_popup_select(p.hwnd, C.int(i))
}

// String 读取下拉框文本（可编辑框含手输内容，只读框为选中项）。必须在主线程调用。
func (p *Popup) String() string {
	var buf [8192]C.char
	C.appkit_popup_get(p.hwnd, &buf[0], 8192)
	return C.GoString(&buf[0])
}

// SetString 设置下拉框文本（可编辑框直接回显；只读框在列表内匹配选中）。
// 必须在主线程调用。
func (p *Popup) SetString(s string) {
	ct := C.CString(s)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_popup_set(p.hwnd, ct)
}

// SetHidden 设置控件是否隐藏（内置 LLM 登录区随厂商切换显隐）。必须在主线程调用。
func (p *Popup) SetHidden(hidden bool) {
	p.hidden = hidden
	var h C.int
	if hidden {
		h = 1
	}
	C.appkit_control_set_hidden(p.hwnd, h)
}

// qrPixels 用 skip2/go-qrcode 生成 text 的二维码位图，最近邻放大到
// size×size 像素（含安静区，白色背景），返回 32bpp BGRA 字节（自上而下）。
// 编码失败（文本异常/超长）返回 nil，控件显示深色空底。
func qrPixels(text string, size int) []byte {
	if text == "" || size <= 0 {
		return nil
	}
	q, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return nil
	}
	bm := q.Bitmap() // 含 4 模块安静区
	dim := len(bm)
	if dim == 0 {
		return nil
	}
	mod := size / dim
	if mod < 1 {
		mod = 1
	}
	total := mod * dim
	off := (size - total) / 2
	out := make([]byte, size*size*4)
	// 白色底 + 不透明。
	for i := 0; i < len(out); i += 4 {
		out[i], out[i+1], out[i+2], out[i+3] = 255, 255, 255, 255
	}
	for y := 0; y < dim; y++ {
		py := off + y*mod
		if py >= size {
			break
		}
		for x := 0; x < dim; x++ {
			if !bm[y][x] {
				continue
			}
			px := off + x*mod
			if px >= size {
				break
			}
			for dy := 0; dy < mod && py+dy < size; dy++ {
				row := (py + dy) * size * 4
				for dx := 0; dx < mod && px+dx < size; dx++ {
					i := row + (px+dx)*4
					out[i], out[i+1], out[i+2] = 0, 0, 0
				}
			}
		}
	}
	return out
}

// qrPixelSize 返回二维码位图边长（按控件逻辑宽度与当前 DPI 缩放，下限 8）。
func qrPixelSize(logical float64) int {
	px := int(C.appkit_scale_for_dpi(C.double(logical)))
	if px < 8 {
		px = 8
	}
	return px
}

// NewImageView 创建二维码图像视图：Go 侧生成 QR 位图，C 侧 ElfinImage
// 子窗口缓存并绘制（GDI StretchDIBits）。必须在主线程调用。
func NewImageView(x, y, w, h float64, text string) *ImageView {
	v := &ImageView{hwnd: nil, x: C.double(x), y: C.double(y), w: C.double(w), h: C.double(h)}
	px := qrPixelSize(w)
	pix := qrPixels(text, px)
	var cp unsafe.Pointer
	if pix != nil {
		cp = unsafe.Pointer(&pix[0])
	}
	v.hwnd = C.appkit_create_image_view(C.double(x), C.double(y), C.double(w), C.double(h),
		cp, C.int(px), C.int(px))
	return v
}

// SetHidden 设置控件是否隐藏（链接/二维码切换）。必须在主线程调用。
func (v *ImageView) SetHidden(hidden bool) {
	v.hidden = hidden
	if v.hwnd == nil {
		return
	}
	var h C.int
	if hidden {
		h = 1
	}
	C.appkit_control_set_hidden(v.hwnd, h)
}

// SetText 重新生成二维码并替换视图图像。必须在主线程调用。
func (v *ImageView) SetText(text string) {
	if v.hwnd == nil {
		return
	}
	px := qrPixelSize(float64(v.w))
	pix := qrPixels(text, px)
	var cp unsafe.Pointer
	if pix != nil {
		cp = unsafe.Pointer(&pix[0])
	}
	C.appkit_image_view_set_pixels(v.hwnd, cp, C.int(px), C.int(px))
}

// NewCheckbox 创建复选开关（BS_OWNERDRAW 自绘：点击经 WM_COMMAND 翻转
// C 侧状态表，Save 时读 state）。必须在主线程调用。
func NewCheckbox(x, y, w, h float64, title string) *Checkbox {
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	hwnd := C.appkit_create_checkbox(C.double(x), C.double(y), C.double(w), C.double(h), ct)
	return &Checkbox{hwnd: hwnd, x: C.double(x), y: C.double(y), w: C.double(w), h: C.double(h)}
}

// State 返回勾选状态。必须在主线程调用。
func (c *Checkbox) State() bool {
	return C.appkit_checkbox_state(c.hwnd) != 0
}

// SetState 设置勾选状态（Show 回显配置）。必须在主线程调用。
func (c *Checkbox) SetState(on bool) {
	var s C.int
	if on {
		s = 1
	}
	C.appkit_checkbox_set_state(c.hwnd, s)
}

// SetHidden 设置控件是否隐藏（随页签切换显隐）。必须在主线程调用。
func (c *Checkbox) SetHidden(hidden bool) {
	c.hidden = hidden
	var h C.int
	if hidden {
		h = 1
	}
	C.appkit_control_set_hidden(c.hwnd, h)
}

// OpenURL 用 ShellExecute 在默认浏览器打开 URL（注册/友情捐献链接）。必须在主线程调用。
func OpenURL(url string) {
	ct := C.CString(url)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_open_url(ct)
}

// NewButton 创建按钮。tag 区分事件来源（Save/Cancel/气泡按钮/tab/关闭）。
// isDefault 为默认按钮（回车触发）。必须在主线程调用。
func NewButton(x, y, w, h float64, title string, tag int64, isDefault bool) *Button {
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	var d C.int
	if isDefault {
		d = 1
	}
	hwnd := C.appkit_create_button(C.double(x), C.double(y), C.double(w), C.double(h), ct, C.int(tag), d)
	return &Button{hwnd: hwnd, x: C.double(x), y: C.double(y), w: C.double(w), h: C.double(h)}
}

// SetFrame 设置控件位置与尺寸（AppKit 左下原点逻辑坐标）。必须在主线程调用。
func (b *Button) SetFrame(x, y, w, h float64) {
	b.x, b.y, b.w, b.h = C.double(x), C.double(y), C.double(w), C.double(h)
	C.appkit_button_set_frame(b.hwnd, C.double(x), C.double(y), C.double(w), C.double(h))
}

// SetTitle 设置按钮标题（登录/退出登录切换）。必须在主线程调用。
func (b *Button) SetTitle(title string) {
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_button_set_title(b.hwnd, ct)
}

// SetHidden 设置控件是否隐藏（中文场景隐藏 Correct/Analyze tab）。必须在主线程调用。
func (b *Button) SetHidden(hidden bool) {
	b.hidden = hidden
	var h C.int
	if hidden {
		h = 1
	}
	C.appkit_control_set_hidden(b.hwnd, h)
}

// SetState 设置按钮选中态（tab 高亮）。必须在主线程调用。
func (b *Button) SetState(on bool) {
	var s C.int
	if on {
		s = 1
	}
	C.appkit_button_set_state(b.hwnd, s)
}

// SetBezelStyle 设置按钮外观样式：15（对应 darwin NSBezelStyleInline）切换为
// 页签风格（按下样式复选按钮，SetState 驱动选中高亮）。必须在主线程调用。
func (b *Button) SetBezelStyle(style int64) {
	C.appkit_button_set_bezel_style(b.hwnd, C.long(style))
}

// SetFontSize 设置按钮字体大小（pt）。必须在主线程调用。
func (b *Button) SetFontSize(pt float64) {
	C.appkit_button_set_font_size(b.hwnd, C.double(pt))
}

// CopyToPasteboard 将文本写入系统剪贴板（并记录自身写入防回环）。必须在主线程调用。
func CopyToPasteboard(s string) {
	ct := C.CString(s)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_copy_to_pasteboard(ct)
}

// ClipboardChangeCount 返回系统剪贴板序列号（任何剪贴板写入都会使其自增）。
// 剪贴板监视据此检测新复制动作。必须在主线程调用。
func ClipboardChangeCount() int64 {
	return int64(C.appkit_pasteboard_change_count())
}

// ClipboardString 读取系统剪贴板纯文本。必须在主线程调用。
// 非文本内容（图片/文件）与最近一次自身写入的文本返回空（防自身写入回环触发）。
func ClipboardString() string {
	return toGo(C.appkit_pasteboard_string())
}

// PasteText 将文本写入剪贴板并模拟 Ctrl+V 粘贴到当前焦点位置。
// 用于 UIA 写回失败时的兜底（配合预先设置好的选中区间保证替换边界）。
// 必须在主线程调用。
func PasteText(s string) {
	ct := C.CString(s)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_paste_text(ct)
}
