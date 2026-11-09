//go:build windows

// Package bubble 的 Windows 实现：WS_POPUP 分层气泡窗口（WS_EX_TOPMOST |
// TOOLWINDOW | NOACTIVATE | LAYERED）。点气泡按钮不夺焦点，Apply 的粘贴兜底
// Ctrl+V 直达目标应用焦点控件。公开 API 与 darwin 版一致。
// 所有函数必须在主线程（已 LockOSThread 的 goroutine）调用。
package bubble

/*
#cgo LDFLAGS: -luser32 -lgdi32

#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0601
#endif
#include <windows.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <wchar.h>

// appkit 包的导出符号：气泡按钮点击按控件 ID 置事件位（与 darwin 映射一致）。
extern void appkit_set_flags(int id);

// appkit 包的深色自绘绘制函数（设置窗口与气泡共用同一实现）。
extern void appkit_draw_item(HWND item, int id, UINT state, HDC dc, const RECT *rc);

// 布局常量（与 darwin 版 NS_* 一致）。
#define BUB_PAD_X 12.0
#define BUB_PAD_Y 8.0
#define BUB_MAX_WIDTH 360.0
#define BUB_LABEL_MAX_W (BUB_MAX_WIDTH - 2 * BUB_PAD_X - (BUB_ROW_BTN_W + BUB_ROW_GAP2)) // 标签断行上限
#define BUB_GAP 8.0
#define BUB_ROW_H 24.0
#define BUB_TAB_H 22.0
#define BUB_ROW_GAP 6.0
#define BUB_TAB_GAP 6.0
#define BUB_ROW_BTN_W 54.0   // 行内 Apply/Copy/Copied 按钮宽
#define BUB_ROW_GAP2 4.0     // 英文行与中文行间距
#define BUB_MIN_W 240.0
#define BUB_LABEL_ID_EN 10000
#define BUB_LABEL_ID_ZH 10001
// 气泡描边与圆角（描边色与 appkit UI_BORDER 一致）。
#define BUB_BORDER RGB(0x3A, 0x3A, 0x44)
#define BUB_RADIUS 14.0

static HINSTANCE g_hinst;
static HWND g_panel;     // 气泡窗口（bubble_init 创建，进程内唯一）
static HWND g_label_en;  // 英文行常驻文本标签（owner-draw：基础色 + 变化词红色）
static HWND g_label_zh;  // 中文行常驻文本标签（单行模式隐藏，恒白字）
static HFONT g_font;
static HBRUSH g_bg_brush; // 与窗口底色一致的深色刷（自绘标签背景用）

// 标签文字配色（与 darwin 端一致）。
#define BUB_GREEN RGB(0x4D, 0xD9, 0x63)
#define BUB_DIFF_RED RGB(0xFF, 0x52, 0x52)

// 差异区间（UTF-16 码元单位，与 wchar_t* 偏移一致）。
typedef struct { long start; long len; } bubble_diff_t;

static int g_green;                // 0=白色文字，1=绿色（无纠正反馈）
static bubble_diff_t g_diffs[128]; // 变化词区间（纠正文短，容量充足）
static int g_diff_count;

static void bubble_draw_label(HDC dc, const RECT *rc, int is_en); // 自绘标签（wndproc 引用）

// 鼠标进出跟踪：WM_MOUSEMOVE+TrackMouseEvent 在 C 回调里置标志（不进 Go）。
static volatile int g_mouse_inside;
static volatile int g_mouse_flags;

static int bubble_dpi(void) {
    HDC dc = GetDC(NULL);
    int dpi = GetDeviceCaps(dc, LOGPIXELSY);
    ReleaseDC(NULL, dc);
    if (dpi <= 0) dpi = 96;
    return dpi;
}

// bubble_wndproc: 按钮点击置事件位（转发 appkit 映射），鼠标进出置标志，
// 标签绘制用深色背景 + 白/绿文字。
static LRESULT CALLBACK bubble_wndproc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
    switch (msg) {
    case WM_NCHITTEST: {
        // 气泡拖动：背景与文本标签返回 HTCAPTION（系统原生拖动）；
        // 按钮等子控件命中自身区域时保持 HTCLIENT，正常派发点击。
        LRESULT r = DefWindowProcW(hwnd, msg, wParam, lParam);
        if (r == HTCLIENT) {
            POINT pt = {(short)LOWORD(lParam), (short)HIWORD(lParam)};
            ScreenToClient(hwnd, &pt);
            HWND child = ChildWindowFromPointEx(hwnd, pt, CWP_SKIPINVISIBLE | CWP_SKIPDISABLED);
            if (child == NULL || child == hwnd) return HTCAPTION; // 背景区域
            int id = GetDlgCtrlID(child);
            if (id == BUB_LABEL_ID_EN || id == BUB_LABEL_ID_ZH) return HTCAPTION; // 文本行
        }
        return r;
    }
    case WM_COMMAND:
        appkit_set_flags(LOWORD(wParam));
        break;
    case WM_MOUSEMOVE: {
        if (!g_mouse_inside) {
            g_mouse_inside = 1;
            g_mouse_flags |= 1; // MouseEnter
        }
        TRACKMOUSEEVENT tme;
        memset(&tme, 0, sizeof(tme));
        tme.cbSize = sizeof(tme);
        tme.dwFlags = TME_LEAVE;
        tme.hwndTrack = hwnd;
        TrackMouseEvent(&tme);
        break;
    }
    case WM_MOUSELEAVE:
        g_mouse_inside = 0;
        g_mouse_flags |= 2; // MouseLeave
        break;
    case WM_DRAWITEM: {
        // 气泡内按钮深色自绘：转发 appkit 共享绘制函数（圆角/悬停/选中态）；
        // 文本标签自绘（基础色 + 变化词红色）。
        DRAWITEMSTRUCT *dis = (DRAWITEMSTRUCT *)lParam;
        if (dis->CtlID == BUB_LABEL_ID_EN || dis->CtlID == BUB_LABEL_ID_ZH) {
            bubble_draw_label(dis->hDC, &dis->rcItem, dis->CtlID == BUB_LABEL_ID_EN);
            return TRUE;
        }
        appkit_draw_item(dis->hwndItem, (int)dis->CtlID, dis->itemState, dis->hDC, &dis->rcItem);
        return TRUE;
    }
    case WM_PAINT: {
        // 圆角描边（父窗口先绘制，子控件后绘制，边缘无重叠）。
        PAINTSTRUCT ps;
        HDC dc = BeginPaint(hwnd, &ps);
        RECT rc;
        GetClientRect(hwnd, &rc);
        HPEN pen = CreatePen(PS_SOLID, 1, BUB_BORDER);
        HPEN oldp = (HPEN)SelectObject(dc, pen);
        HGDIOBJ oldb = SelectObject(dc, GetStockObject(NULL_BRUSH));
        int r = (int)(BUB_RADIUS * 2);
        RoundRect(dc, 0, 0, rc.right, rc.bottom, r, r);
        SelectObject(dc, oldb);
        SelectObject(dc, oldp);
        DeleteObject(pen);
        EndPaint(hwnd, &ps);
        return 0;
    }
    }
    return DefWindowProcW(hwnd, msg, wParam, lParam);
}

// bubble_line_height: 当前字体的行高（测量与自绘共用，保证断行一致）。
static int bubble_line_height(HDC dc) {
    TEXTMETRICW tm;
    GetTextMetricsW(dc, &tm);
    return tm.tmHeight + tm.tmExternalLeading;
}

// bubble_line_cb: 逐行回调（paint 时绘制该行；measure 时传 NULL）。
// s16/e16 为该行在 text 中的 UTF-16 起止偏移（含词间空白），y 为行顶像素。
typedef void (*bubble_line_cb)(HDC dc, const wchar_t *text, int s16, int e16, int y, void *ctx);

// bubble_wrap_lines: 将 text 按词断行（行宽 ≤ maxw，'\n' 强制换行），逐行调用 cb。
// measure 与 paint 共用同一断行规则，保证测量尺寸与实际绘制一致。
// out_max_w 返回最大行宽，返回值为总高（像素）。
static int bubble_wrap_lines(HDC dc, const wchar_t *text, int maxw,
                             bubble_line_cb cb, void *ctx, int *out_max_w) {
    int lh = bubble_line_height(dc);
    SIZE sp;
    GetTextExtentPoint32W(dc, L" ", 1, &sp);
    int spaceW = sp.cx;
    int total = (int)wcslen(text);
    int y = 0, maxLineW = 0;

    int p = 0; // 段落起点（以 '\n' 分隔）
    while (p < total) {
        int pend = p;
        while (pend < total && text[pend] != L'\n') pend++;
        int s = p;
        while (s < pend && iswspace(text[s])) s++;
        if (s >= pend) { // 空段（连续换行）：占一行高
            if (cb) cb(dc, text, p, pend, y, ctx);
            y += lh;
            p = pend + 1;
            continue;
        }
        int lineStart = s, lineW = 0;
        while (s < pend) {
            while (s < pend && iswspace(text[s])) s++;
            if (s >= pend) break;
            int e = s;
            while (e < pend && !iswspace(text[e])) e++;
            SIZE ws;
            GetTextExtentPoint32W(dc, text + s, e - s, &ws);
            if (lineW > 0 && lineW + spaceW + ws.cx > maxw) { // 放不下：换行
                if (cb) cb(dc, text, lineStart, s, y, ctx);
                if (lineW > maxLineW) maxLineW = lineW;
                y += lh;
                lineStart = s;
                lineW = 0;
            }
            if (ws.cx <= maxw) { // 常规词：整词入行
                if (lineW > 0) lineW += spaceW;
                lineW += ws.cx;
                s = e;
                continue;
            }
            // 单词比 maxw 还宽：按字符硬断成多段（与 darwin 端 AppKit word wrap
            // 对超长词的处理一致），段结束即断行，段间不合并。
            while (s < e) {
                int k = 1; // 贪心取 ≤ maxw 的最长前缀（至少 1 码元）
                SIZE pws;
                while (s + k < e) {
                    GetTextExtentPoint32W(dc, text + s, k + 1, &pws);
                    if (pws.cx > maxw) break;
                    k++;
                }
                // 前缀尾部不能拆散代理对（高代理必须与低代理同段）。
                if (s + k < e) {
                    wchar_t c1 = text[s + k - 1];
                    wchar_t c2 = text[s + k];
                    if (c1 >= 0xD800 && c1 <= 0xDBFF && c2 >= 0xDC00 && c2 <= 0xDFFF) k--;
                }
                if (k < 1) k = 1;
                GetTextExtentPoint32W(dc, text + s, k, &pws);
                if (lineW > 0 && lineW + spaceW + pws.cx > maxw) { // 段也放不下：先换行
                    if (cb) cb(dc, text, lineStart, s, y, ctx);
                    if (lineW > maxLineW) maxLineW = lineW;
                    y += lh;
                    lineStart = s;
                    lineW = 0;
                }
                if (lineW > 0) lineW += spaceW;
                lineW += pws.cx;
                s += k;
                if (cb) cb(dc, text, lineStart, s, y, ctx); // 段结束即断行
                if (lineW > maxLineW) maxLineW = lineW;
                y += lh;
                lineStart = s;
                lineW = 0;
            }
        }
        if (cb) cb(dc, text, lineStart, pend, y, ctx);
        if (lineW > maxLineW) maxLineW = lineW;
        y += lh;
        p = pend + 1;
    }
    if (out_max_w != NULL) *out_max_w = maxLineW;
    return y;
}

// 行绘制上下文：green=基础色绿色，diffs=应用变化词标红（仅英文行）。
typedef struct { int green; int diffs; } bubble_line_ctx;

// bubble_paint_line: 自绘一行——逐词取色（红=变化词，绿/白=基础色）后 TextOutW。
// 词间空白按单空格绘制（与 bubble_wrap_lines 的宽度累加规则一致）。
static void bubble_paint_line(HDC dc, const wchar_t *text, int s16, int e16, int y, void *ctx) {
    bubble_line_ctx *lc = (bubble_line_ctx *)ctx;
    int x = 0;
    int s = s16;
    while (s < e16) {
        while (s < e16 && iswspace(text[s])) s++;
        if (s >= e16) break;
        int e = s;
        while (e < e16 && !iswspace(text[e])) e++;
        // 词 text[s..e) 是否与任一变化区间相交（相交即红）。
        int red = 0;
        if (lc->diffs && !lc->green) {
            for (int i = 0; i < g_diff_count; i++) {
                long ds = g_diffs[i].start, de = ds + g_diffs[i].len;
                if ((long)s < de && (long)e > ds) {
                    red = 1;
                    break;
                }
            }
        }
        SetTextColor(dc, red ? BUB_DIFF_RED : (lc->green ? BUB_GREEN : RGB(0xFF, 0xFF, 0xFF)));
        TextOutW(dc, x, y, text + s, e - s);
        SIZE ws;
        GetTextExtentPoint32W(dc, text + s, e - s, &ws);
        x += ws.cx;
        s = e;
        if (s < e16) { // 行内还有后续词：按单空格宽度推进（与断行规则一致）
            SIZE ss;
            GetTextExtentPoint32W(dc, L" ", 1, &ss);
            x += ss.cx;
        }
    }
}

// bubble_draw_label: owner-draw 标签自绘——填充深色背景后逐词换行绘制。
// is_en=1 为英文行（基础色 g_green + 变化词标红），0 为中文行（恒白字）。
static void bubble_draw_label(HDC dc, const RECT *rc, int is_en) {
    HWND label = is_en ? g_label_en : g_label_zh;
    int n = GetWindowTextLengthW(label);
    wchar_t *buf = (wchar_t *)malloc(sizeof(wchar_t) * (size_t)(n + 1));
    if (buf == NULL) return;
    GetWindowTextW(label, buf, n + 1);

    FillRect(dc, rc, g_bg_brush);
    SetBkMode(dc, TRANSPARENT);
    HFONT oldf = (HFONT)SelectObject(dc, g_font);
    bubble_line_ctx ctx = {is_en ? g_green : 0, is_en};
    // 断行上限固定为 BUB_LABEL_MAX_W（与测量一致），不能用 rc 宽（ew+4）：
    // 两者不一致时超长词硬断边界不同，绘制行数会多于测量行数导致底部裁切。
    bubble_wrap_lines(dc, buf, (int)BUB_LABEL_MAX_W, bubble_paint_line, &ctx, NULL);
    SelectObject(dc, oldf);
    free(buf);
}

// bubble_init: 创建气泡窗口、深色背景与常驻标签（仅调用一次）。
static void bubble_init(void) {
    if (g_panel != NULL) return;
    g_hinst = GetModuleHandleW(NULL);

    WNDCLASSEXW wc;
    memset(&wc, 0, sizeof(wc));
    wc.cbSize = sizeof(wc);
    wc.lpfnWndProc = bubble_wndproc;
    wc.hInstance = g_hinst;
    wc.hCursor = LoadCursorW(NULL, (LPCWSTR)(ULONG_PTR)IDC_ARROW);
    // 深色背景 RGB(0x1F,0x1F,0x26)（对应 darwin 的 0.12/0.12/0.15 @0.92）。
    g_bg_brush = CreateSolidBrush(RGB(0x1F, 0x1F, 0x26));
    wc.hbrBackground = g_bg_brush;
    wc.lpszClassName = L"ElfinBubble";
    RegisterClassExW(&wc);

    g_panel = CreateWindowExW(
        WS_EX_TOPMOST | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE | WS_EX_LAYERED,
        L"ElfinBubble", L"",
        WS_POPUP,
        0, 0, 10, 10, NULL, NULL, g_hinst, NULL);

    // 常驻双行标签（13pt Segoe UI，按实际 DPI 缩放）：英文行在上、中文行在下。
    g_font = CreateFontW(-MulDiv(13, bubble_dpi(), 72), 0, 0, 0, FW_NORMAL, 0, 0, 0,
        DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
        CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Segoe UI");
    g_label_en = CreateWindowExW(0, L"STATIC", L"",
        WS_CHILD | SS_OWNERDRAW, 0, 0, 10, 10, g_panel, (HMENU)(INT_PTR)BUB_LABEL_ID_EN, g_hinst, NULL);
    g_label_zh = CreateWindowExW(0, L"STATIC", L"",
        WS_CHILD | SS_OWNERDRAW, 0, 0, 10, 10, g_panel, (HMENU)(INT_PTR)BUB_LABEL_ID_ZH, g_hinst, NULL);
    SendMessageW(g_label_en, WM_SETFONT, (WPARAM)g_font, TRUE);
    SendMessageW(g_label_zh, WM_SETFONT, (WPARAM)g_font, TRUE);
}

// bubble_content_view: 返回气泡窗口句柄（供 Go 侧 appkit.NewHandle 添加按钮）。
static void *bubble_content_view(void) {
    return g_panel;
}

// bubble_measure: 用与自绘一致的按词断行规则测量多行文本尺寸（maxw 为最大
// 行宽；行内按钮占用右侧宽度，调用方传入收窄后的 maxw，与 darwin 的
// preferredMaxLayoutWidth 一致），out_w/out_h 返回像素尺寸。
static void bubble_measure(const wchar_t *text, int maxw, double *out_w, double *out_h) {
    HDC dc = GetDC(g_label_en);
    if (dc == NULL) {
        *out_w = 0;
        *out_h = 0;
        return;
    }
    HFONT oldf = (HFONT)SelectObject(dc, g_font);
    int mw = 0;
    int h = bubble_wrap_lines(dc, text, maxw, NULL, NULL, &mw);
    SelectObject(dc, oldf);
    ReleaseDC(g_label_en, dc);
    *out_w = (double)mw;
    *out_h = (double)h;
}

// bubble_show: 在 UIA 坐标 (ax_x, ax_y, ax_w, ax_h) 的输入框上方显示文本气泡。
// 双行模式(Correct tab): text 为英文行、text2 为中文行;单行模式 text2 为空。
// 屏幕坐标原点主屏左上、y 向下（与 UIA 一致，无需 y 翻转）。
// out_w/out_h 返回气泡尺寸（供 Go 侧摆放按钮），
// out_en_h/out_en_w 返回英文行高宽、out_zh_h/out_zh_w 返回中文行高宽
// （供 Go 侧摆放行内 Apply 按钮与顶部 tab 栏）。
static void bubble_show(const char *text, const char *text2, int green, const bubble_diff_t *diffs, int diff_count,
                        double ax_x, double ax_y, double ax_w, double ax_h,
                        double *out_w, double *out_h, double *out_en_h, double *out_en_w,
                        double *out_zh_h, double *out_zh_w) {
    if (g_panel == NULL) return;

    int n = MultiByteToWideChar(CP_UTF8, 0, text, -1, NULL, 0);
    if (n <= 0) return;
    wchar_t *wt = (wchar_t *)malloc(sizeof(wchar_t) * (size_t)n);
    if (wt == NULL) return;
    MultiByteToWideChar(CP_UTF8, 0, text, -1, wt, n);

    int two = text2 != NULL && text2[0] != '\0';
    wchar_t *wt2 = NULL;
    if (two) {
        int n2 = MultiByteToWideChar(CP_UTF8, 0, text2, -1, NULL, 0);
        if (n2 > 0) {
            wt2 = (wchar_t *)malloc(sizeof(wchar_t) * (size_t)n2);
            if (wt2 != NULL) {
                MultiByteToWideChar(CP_UTF8, 0, text2, -1, wt2, n2);
            }
        }
        if (wt2 == NULL) two = 0;
    }

    // 记录自绘状态：基础色与变化词区间（后续 InvalidateRect 触发 WM_DRAWITEM）。
    g_green = green;
    g_diff_count = diff_count;
    if (g_diff_count > 128) g_diff_count = 128;
    fprintf(stderr, "[bubble] show diffs=%d:", g_diff_count);
    for (int i = 0; i < g_diff_count; i++) {
        g_diffs[i] = diffs[i];
        fprintf(stderr, " [%ld,%ld)", g_diffs[i].start, g_diffs[i].start + g_diffs[i].len);
    }
    fprintf(stderr, "\n");

    // 行宽 = 文本宽 + 行内按钮宽 + 间距（标签最大宽已收窄保证不超 BUB_MAX_WIDTH）。
    int maxw = (int)BUB_LABEL_MAX_W;
    double ew = 0, eh = 0, zw = 0, zh = 0;
    bubble_measure(wt, maxw, &ew, &eh);
    if (two) bubble_measure(wt2, maxw, &zw, &zh);
    double w = ew + BUB_ROW_BTN_W + BUB_ROW_GAP2 + 2 * BUB_PAD_X;
    if (two && zw + BUB_ROW_BTN_W + BUB_ROW_GAP2 + 2 * BUB_PAD_X > w) {
        w = zw + BUB_ROW_BTN_W + BUB_ROW_GAP2 + 2 * BUB_PAD_X;
    }
    if (w < BUB_MIN_W) w = BUB_MIN_W;
    if (w < 40) w = 40;
    if (w > BUB_MAX_WIDTH) w = BUB_MAX_WIDTH; // 硬钳制：防御超长不可断文本撑爆气泡
    double h = BUB_TAB_H + BUB_TAB_GAP + eh + (two ? BUB_ROW_GAP2 + zh : 0)
        + BUB_ROW_GAP + BUB_ROW_H + 2 * BUB_PAD_Y;

    // 气泡位置：输入框上方 8px。左上原点 y 向下：气泡底 = ax_y - GAP。
    double x = ax_x;
    double y = ax_y - h - BUB_GAP;

    // 所在屏幕工作区（多显示器下按输入框位置取屏，优于 darwin 的主屏约定）。
    POINT pt = {(LONG)ax_x, (LONG)ax_y};
    HMONITOR mon = MonitorFromPoint(pt, MONITOR_DEFAULTTONEAREST);
    MONITORINFO mi;
    memset(&mi, 0, sizeof(mi));
    mi.cbSize = sizeof(mi);
    GetMonitorInfoW(mon, &mi);
    RECT work = mi.rcWork;

    // 上方放不下（贴近屏幕顶部）→ 翻到输入框下方；横向越界钳制回屏内。
    if (y < work.top + 4) {
        y = ax_y + ax_h + BUB_GAP;
    }
    if (x + w > work.right - 4) {
        x = work.right - w - 4;
    }
    if (x < work.left + 4) x = work.left + 4;

    // 标签：位于按钮行上方、tab 栏下方（左上原点：英文行在上、中文行在下）。
    SetWindowTextW(g_label_en, wt);
    free(wt);
    MoveWindow(g_label_en, (int)BUB_PAD_X, (int)(BUB_TAB_H + BUB_TAB_GAP),
        (int)ew + 4, (int)eh, TRUE);
    InvalidateRect(g_label_en, NULL, TRUE);
    ShowWindow(g_label_en, SW_SHOW); // 英文行恒显示（标签创建时无 WS_VISIBLE，必须显式 SHOW）
    if (two) {
        SetWindowTextW(g_label_zh, wt2);
        MoveWindow(g_label_zh, (int)BUB_PAD_X, (int)(BUB_TAB_H + BUB_TAB_GAP + eh + BUB_ROW_GAP2),
            (int)zw + 4, (int)zh, TRUE);
        InvalidateRect(g_label_zh, NULL, TRUE);
        ShowWindow(g_label_zh, SW_SHOW);
    } else {
        ShowWindow(g_label_zh, SW_HIDE);
    }
    if (wt2 != NULL) free(wt2);

    SetWindowPos(g_panel, HWND_TOPMOST, (int)x, (int)y, (int)w, (int)h,
        SWP_NOACTIVATE | SWP_SHOWWINDOW);

    // 圆角区域：SetWindowRgn 成功即由系统接管 region 所有权，失败才自行释放。
    HRGN rgn = CreateRoundRectRgn(0, 0, (int)w + 1, (int)h + 1,
        (int)(BUB_RADIUS * 2), (int)(BUB_RADIUS * 2));
    if (SetWindowRgn(g_panel, rgn, TRUE) == 0) {
        DeleteObject(rgn);
    }

    if (out_w != NULL) *out_w = w;
    if (out_h != NULL) *out_h = h;
    if (out_en_h != NULL) *out_en_h = eh;
    if (out_en_w != NULL) *out_en_w = ew;
    if (out_zh_h != NULL) *out_zh_h = zh;
    if (out_zh_w != NULL) *out_zh_w = zw;
}

// bubble_hide: 隐藏气泡并重置鼠标跟踪状态（窗口隐藏后 TME_LEAVE 可能不派发，
// 残留"鼠标在内"标志会导致下次弹出后输入活动永不淡出，与 darwin 对齐）。
static void bubble_hide(void) {
    if (g_panel == NULL) return;
    ShowWindow(g_panel, SW_HIDE);
    g_mouse_inside = 0;
    g_mouse_flags = 0;
}

// bubble_visible: 气泡当前是否可见。
static int bubble_visible(void) {
    if (g_panel == NULL) return 0;
    return IsWindowVisible(g_panel) ? 1 : 0;
}

// bubble_set_alpha: 设置气泡整体透明度（淡出动画由 Go 侧驱动）。
// WS_EX_LAYERED + SetLayeredWindowAttributes：Win8+ 子控件随统一 alpha 渲染。
static void bubble_set_alpha(double a) {
    if (g_panel == NULL) return;
    if (a < 0) a = 0;
    if (a > 1) a = 1;
    SetLayeredWindowAttributes(g_panel, 0, (BYTE)(a * 255), LWA_ALPHA);
}

// bubble_mouse_inside: 鼠标当前是否位于气泡内。
static int bubble_mouse_inside(void) {
    return g_mouse_inside;
}

// bubble_mouse_flags: 返回并清除鼠标进出事件（bit0=进入，bit1=离开）。
static int bubble_mouse_flags(void) {
    int f = g_mouse_flags;
    g_mouse_flags = 0;
    return f;
}
*/
import "C"

import (
	"fmt"
	"unsafe"

	"elfin-grammar/internal/appkit"
	"elfin-grammar/internal/ax"
)

// 气泡控件（进程内唯一，Init 创建）。
var (
	btnTabCorrect *appkit.Button
	btnTabAnalyze *appkit.Button
	tabList       []*appkit.Button
	btnApplyEn    *appkit.Button // 英文行行内按钮（Apply/Copy/Copied 动态）
	btnApplyZh    *appkit.Button // 中文行行内按钮（Apply/Copy/Copied 动态）
	btnCopy       *appkit.Button
	btnSettings   *appkit.Button
	btnList       []*appkit.Button
	btnClose      *appkit.Button // 右上角关闭按钮
	// tabWidths 与 tabList 一一对应；btnWidths 与 btnList 一一对应。
	tabWidths = []float64{64, 70} // Correct/Analyze（Translate 已合并进 Correct）
	btnWidths = []float64{48, 72} // Copy/Settings（Apply 已改为行内按钮）
	// 最近一次 Show 的各行高宽（摆行内按钮与 tab 栏用）。
	enH, enW  float64
	zhH, zhW  float64
	bubbleW   float64      // 最近一次 Show 的气泡宽度（摆关闭按钮用）
	activeTab = TabCorrect // 当前选中 tab
)

// Init 初始化气泡窗口（分层弹出窗口 + 常驻双行标签 + 顶部 tab 栏 + 行内按钮 + 底部按钮行）。
// 必须在主线程调用，进程内仅调用一次。
func Init() {
	C.bubble_init()
	content := appkit.NewHandle(unsafe.Pointer(C.bubble_content_view()))
	btnTabCorrect = appkit.NewButton(0, 0, 64, tabH, "Correct", tagTabCorrect, false)
	btnTabAnalyze = appkit.NewButton(0, 0, 70, tabH, "Analyze", tagTabAnalyze, false)
	tabList = []*appkit.Button{btnTabCorrect, btnTabAnalyze}
	for _, tb := range tabList {
		tb.SetBezelStyle(15) // 对应 darwin NSBezelStyleInline：页签风格，SetState 驱动选中高亮
		tb.SetFontSize(11)
		content.AddSubview(tb)
	}
	// 行内按钮：紧跟各文本行右侧（标题由 Show 的 TabOptions 动态指定）。
	btnApplyEn = appkit.NewButton(0, 0, rowBtnW, btnRowH, "Apply", tagApplyEn, false)
	btnApplyZh = appkit.NewButton(0, 0, rowBtnW, btnRowH, "Apply", tagApplyZh, false)
	content.AddSubview(btnApplyEn)
	content.AddSubview(btnApplyZh)
	btnCopy = appkit.NewButton(0, 0, 48, btnRowH, "Copy", tagCopy, false)
	btnSettings = appkit.NewButton(0, 0, 72, btnRowH, "Settings", tagSettings, false)
	btnList = []*appkit.Button{btnCopy, btnSettings}
	for _, b := range btnList {
		content.AddSubview(b)
	}
	btnClose = appkit.NewButton(0, 0, closeW, tabH, "✕", tagClose, false)
	btnClose.SetBezelStyle(15) // 与 tab 行风格统一
	btnClose.SetFontSize(13)
	content.AddSubview(btnClose)
}

// Show 在输入框上方显示文本气泡（顶部 tab 栏 + 内容 + 底部按钮行）。
// r 为输入框在 UIA 坐标系中的位置。en/zh 为英文行/中文行文本（zh 为空即单行模式）。
// green 为 true 时英文行文字用绿色（表示无纠正的肯定反馈）；
// diffs 为英文行的变化词区间（UTF-16 码元单位），非空时对应文字标红。
// opts.EnTitle/ZhTitle 控制行内按钮标题（空=隐藏）。必须在主线程调用。
func Show(en, zh string, r ax.Rect, opts TabOptions, green bool, diffs []Span) {
	CancelFade() // 新内容出现时恢复完全不透明
	fmt.Printf("[bubble] Show en=%q zh=%q green=%v diffs=%v\n", en, zh, green, diffs)
	cEn := C.CString(en)
	defer C.free(unsafe.Pointer(cEn))
	cZh := C.CString(zh)
	defer C.free(unsafe.Pointer(cZh))
	cDiffs := make([]C.bubble_diff_t, len(diffs))
	for i, d := range diffs {
		cDiffs[i] = C.bubble_diff_t{start: C.long(d.Start), len: C.long(d.Length)}
	}
	var diffPtr *C.bubble_diff_t
	if len(cDiffs) > 0 {
		diffPtr = &cDiffs[0]
	}
	var w, h, eh, ew, zhh, zhw C.double
	C.bubble_show(cEn, cZh, C.int(b2i(green)), diffPtr, C.int(len(cDiffs)),
		C.double(r.X), C.double(r.Y), C.double(r.W), C.double(r.H),
		&w, &h, &eh, &ew, &zhh, &zhw)
	enH, enW = float64(eh), float64(ew)
	zhH, zhW = float64(zhh), float64(zhw)
	bubbleW = float64(w)
	activeTab = opts.Active
	layoutTabs(opts.Visible)
	layoutButtons()
	layoutRowButtons(opts.EnTitle, opts.ZhTitle)
}

// b2i 将布尔转为 C 侧 int 标志。
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// layoutTabs 在内容标签上方摆放 tab 栏：控制可见性与选中高亮。
// 坐标与 darwin 一致为 AppKit 风格（父容器左下原点、y 向上），
// appkit 的 Windows 实现统一翻转。
func layoutTabs(visible int) {
	y := btnPadY + btnRowH + btnGap + zhH + rowGap2 + enH + tabGap
	x := btnPadX
	for i, tb := range tabList {
		mask := 1 << i
		vis := visible&mask != 0
		tb.SetHidden(!vis)
		tb.SetState(activeTab == mask)
		if vis {
			tb.SetFrame(x, y, tabWidths[i], tabH)
			x += tabWidths[i] + tabGap
		}
	}
	// 关闭按钮固定右上角，与 tab 行同高（x 由气泡实际宽度推算）。
	btnClose.SetFrame(bubbleW-btnPadX-closeW, y, closeW, tabH)
}

// layoutButtons 按 btnWidths 在底部一行摆放按钮（间距 btnGap）。
func layoutButtons() {
	x := btnPadX
	for i, b := range btnList {
		b.SetFrame(x, btnPadY, btnWidths[i], btnRowH)
		x += btnWidths[i] + btnGap
	}
}

// layoutRowButtons 摆放行内按钮：紧跟各文本行右侧、垂直居中，标题为空则隐藏。
func layoutRowButtons(enTitle, zhTitle string) {
	zhY := btnPadY + btnRowH + btnGap
	enY := zhY + zhH + rowGap2
	btnApplyEn.SetTitle(enTitle)
	btnApplyEn.SetHidden(enTitle == "")
	btnApplyEn.SetFrame(btnPadX+enW+rowGap2, enY+(enH-btnRowH)/2, rowBtnW, btnRowH)
	btnApplyZh.SetTitle(zhTitle)
	btnApplyZh.SetHidden(zhTitle == "")
	btnApplyZh.SetFrame(btnPadX+zhW+rowGap2, zhY+(zhH-btnRowH)/2, rowBtnW, btnRowH)
}

// SetRowButtonTitle 更新行内按钮标题（剪贴板场景 Copy/Copied 动态切换）。
// which: 0=英文行，1=中文行。必须在主线程调用。
func SetRowButtonTitle(which int, title string) {
	b := btnApplyEn
	if which == 1 {
		b = btnApplyZh
	}
	if title == "" {
		b.SetHidden(true)
		return
	}
	b.SetTitle(title)
	b.SetHidden(false)
}

// SetActiveTab 更新选中 tab 的高亮（不改变显示内容）。必须在主线程调用。
func SetActiveTab(t int) {
	activeTab = t
	for i, tb := range tabList {
		tb.SetState(t == 1<<i)
	}
}

// ActiveTab 返回当前选中 tab。必须在主线程调用。
func ActiveTab() int {
	return activeTab
}

// setAlpha 设置气泡整体透明度（见 bubble_common.go 的淡出状态机）。必须在主线程调用。
func setAlpha(a float64) {
	C.bubble_set_alpha(C.double(a))
}

// hideWindow 隐藏气泡（见 bubble_common.go）。必须在主线程调用。
func hideWindow() {
	C.bubble_hide()
}

// ConsumeMouseEvents 返回并清除鼠标进出事件。必须在主线程调用。
func ConsumeMouseEvents() MouseEvent {
	return MouseEvent(C.bubble_mouse_flags())
}

// MouseInside 返回鼠标当前是否位于气泡内。必须在主线程调用。
func MouseInside() bool {
	return C.bubble_mouse_inside() != 0
}

// Visible 返回气泡当前是否可见。必须在主线程调用。
func Visible() bool {
	return C.bubble_visible() != 0
}
