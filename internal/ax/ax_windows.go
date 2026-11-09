//go:build windows

// Package ax 的 Windows 实现：UI Automation (UIA) 桥接。
// 读取焦点元素、定位输入框/网页正文、读写值与选中文本，公开 API 与 darwin 版一致。
// Windows 无辅助功能授权概念（UIA 开箱可用），Trusted 恒为 true。
// 所有函数必须在主线程（已 LockOSThread 的 goroutine）调用。
package ax

/*
#cgo LDFLAGS: -lole32 -loleaut32 -luser32 -lgdi32

#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0601
#endif
#include <windows.h>
#include <ole2.h>
#include <uiautomationclient.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// UIA 控件类型与模式 ID（文档化常量；#ifndef 兜底兼容不同版本头文件）。
#ifndef UIA_EditControlTypeId
#define UIA_EditControlTypeId 50004
#endif
#ifndef UIA_DocumentControlTypeId
#define UIA_DocumentControlTypeId 50030
#endif
#ifndef UIA_ValuePatternId
#define UIA_ValuePatternId 10002
#endif
#ifndef UIA_TextPatternId
#define UIA_TextPatternId 10014
#endif

// 静态 GUID 定义（不依赖导入库导出符号）：CoCreateInstance 按注册表
// 加载 UIAutomationCore.dll，无需链接 -luiautomationcore。
static const GUID AX_CLSID_CUIAutomation =
    {0xff48dba4, 0x60ef, 0x4201, {0xaa, 0x87, 0x54, 0x10, 0x3e, 0xef, 0x59, 0x4e}};
static const GUID AX_IID_IUIAutomation =
    {0x30cbe57d, 0xd9d0, 0x452a, {0xab, 0x13, 0x7a, 0xc5, 0xac, 0x48, 0x25, 0xee}};
static const GUID AX_IID_IUIAutomationValuePattern =
    {0xa94cd8b1, 0x0844, 0x4cd6, {0x9d, 0x2d, 0x64, 0x05, 0x37, 0xab, 0x39, 0xe9}};
static const GUID AX_IID_IUIAutomationTextPattern =
    {0x32eba289, 0x3583, 0x42c9, {0x9c, 0x59, 0x3b, 0x6d, 0x9a, 0x1e, 0x9a, 0x6a}};

// ---------- UIA 单例初始化（主线程一次性） ----------
static IUIAutomation *g_automation;
static IUIAutomationTreeWalker *g_walker;

static int ax_ensure(void) {
    if (g_automation != NULL) return 1;
    HRESULT hr = CoInitializeEx(NULL, COINIT_APARTMENTTHREADED);
    if (FAILED(hr) && hr != RPC_E_CHANGED_MODE) return 0;
    hr = CoCreateInstance(&AX_CLSID_CUIAutomation, NULL, CLSCTX_INPROC_SERVER,
        &AX_IID_IUIAutomation, (void **)&g_automation);
    if (FAILED(hr) || g_automation == NULL) return 0;
    if (g_automation->lpVtbl->get_ControlViewWalker(g_automation, &g_walker) != S_OK) {
        g_walker = NULL;
    }
    return 1;
}

// ---------- 焦点元素 ----------
// GetFocusedElement 可能因缓存失效返回 NULL（如桌面无焦点）：限频（5s）
// 重建自动化单例再试一次，避免空闲轮询反复重建。
static DWORD g_last_recreate;

static IUIAutomationElement *ax_focused_element(void) {
    if (!ax_ensure()) return NULL;
    IUIAutomationElement *e = NULL;
    HRESULT hr = g_automation->lpVtbl->GetFocusedElement(g_automation, &e);
    if (SUCCEEDED(hr) && e != NULL) return e;
    DWORD now = GetTickCount();
    if (now - g_last_recreate < 5000) return NULL;
    g_last_recreate = now;
    IUIAutomation *old = g_automation;
    IUIAutomationTreeWalker *oldw = g_walker;
    g_automation = NULL;
    g_walker = NULL;
    if (ax_ensure()) {
        hr = g_automation->lpVtbl->GetFocusedElement(g_automation, &e);
    }
    if (old) old->lpVtbl->Release(old);
    if (oldw) oldw->lpVtbl->Release(oldw);
    if (SUCCEEDED(hr) && e != NULL) return e;
    return NULL;
}

static void ax_release(IUIAutomationElement *e) {
    if (e) e->lpVtbl->Release(e);
}

// ---------- 字符串互转 ----------
static char *ax_strdup8(const char *s) {
    if (s == NULL) return NULL;
    size_t n = strlen(s) + 1;
    char *p = (char *)malloc(n);
    if (p != NULL) memcpy(p, s, n);
    return p;
}

static char *ax_bstr_to_utf8(BSTR b) {
    if (b == NULL) return NULL;
    UINT n = SysStringLen(b);
    if (n == 0) return NULL;
    int m = WideCharToMultiByte(CP_UTF8, 0, b, (int)n, NULL, 0, NULL, NULL);
    if (m <= 0) return NULL;
    char *out = (char *)malloc((size_t)m + 1);
    if (out == NULL) return NULL;
    WideCharToMultiByte(CP_UTF8, 0, b, (int)n, out, m, NULL, NULL);
    out[m] = '\0';
    return out;
}

static BSTR ax_utf8_to_bstr(const char *s) {
    if (s == NULL) return NULL;
    int n = MultiByteToWideChar(CP_UTF8, 0, s, -1, NULL, 0);
    if (n <= 0) return NULL;
    BSTR b = SysAllocStringLen(NULL, (UINT)(n - 1));
    if (b == NULL) return NULL;
    MultiByteToWideChar(CP_UTF8, 0, s, -1, b, n);
    return b;
}

static char *ax_wcs_to_utf8(const wchar_t *w, int n) {
    if (w == NULL || n <= 0) return NULL;
    int m = WideCharToMultiByte(CP_UTF8, 0, w, n, NULL, 0, NULL, NULL);
    if (m <= 0) return NULL;
    char *out = (char *)malloc((size_t)m + 1);
    if (out == NULL) return NULL;
    WideCharToMultiByte(CP_UTF8, 0, w, n, out, m, NULL, NULL);
    out[m] = '\0';
    return out;
}

// ---------- 元素属性 ----------
static int ax_control_type(IUIAutomationElement *e) {
    int t = 0;
    if (e->lpVtbl->get_CurrentControlType(e, &t) != S_OK) return 0;
    return t;
}

static int ax_is_password(IUIAutomationElement *e) {
    BOOL b = FALSE;
    if (e->lpVtbl->get_CurrentIsPassword(e, &b) == S_OK) return b ? 1 : 0;
    return 0;
}

static IUIAutomationElement *ax_parent(IUIAutomationElement *e) {
    if (g_walker == NULL) return NULL;
    IUIAutomationElement *p = NULL;
    g_walker->lpVtbl->GetParentElement(g_walker, e, &p);
    return p;
}

static int ax_has_value_pattern(IUIAutomationElement *e) {
    IUIAutomationValuePattern *vp = NULL;
    HRESULT hr = e->lpVtbl->GetCurrentPatternAs(e, UIA_ValuePatternId,
        &AX_IID_IUIAutomationValuePattern, (void **)&vp);
    if (SUCCEEDED(hr) && vp != NULL) {
        vp->lpVtbl->Release(vp);
        return 1;
    }
    return 0;
}

// ---------- 前台进程名（网页判定：浏览器进程内的 Document 视为网页正文） ----------
static int ax_foreground_is_browser(void) {
    static const wchar_t *BROWSERS[] = {
        L"chrome.exe", L"msedge.exe", L"chromium.exe", L"brave.exe",
        L"firefox.exe", L"opera.exe", L"vivaldi.exe", L"iexplore.exe",
        L"arc.exe", L"tor.exe",
    };
    HWND fg = GetForegroundWindow();
    if (fg == NULL) return 0;
    DWORD pid = 0;
    GetWindowThreadProcessId(fg, &pid);
    if (pid == 0) return 0;
    HANDLE h = OpenProcess(PROCESS_QUERY_INFORMATION, FALSE, pid);
    if (h == NULL) return 0;
    wchar_t path[MAX_PATH];
    DWORD cap = MAX_PATH;
    DWORD n = QueryFullProcessImageNameW(h, 0, path, &cap);
    CloseHandle(h);
    if (n == 0) return 0;
    wchar_t *base = wcsrchr(path, L'\\');
    base = base ? base + 1 : path;
    size_t i;
    for (i = 0; i < sizeof(BROWSERS) / sizeof(BROWSERS[0]); i++) {
        if (_wcsicmp(base, BROWSERS[i]) == 0) return 1;
    }
    return 0;
}

// ---------- 目标定位（与 darwin 的 ax_find_text_ancestor 同语义） ----------
// 从焦点元素向上最多 16 层：优先 Edit（输入框）；支持 ValuePattern 的 Document
// （Notepad/Word 等富文本编辑器）视为可写输入框；其余 Document（浏览器网页正文）
// 置 is_web=1（不可写回，Apply 降级复制）。返回 Copy 语义的引用（调用者 ax_release）。
static IUIAutomationElement *ax_find_target(IUIAutomationElement *ref, int *is_web) {
    IUIAutomationElement *doc = NULL;
    IUIAutomationElement *cur = ref;
    cur->lpVtbl->AddRef(cur);
    int i;
    for (i = 0; i < 16; i++) {
        int t = ax_control_type(cur);
        if (t == UIA_EditControlTypeId) {
            if (doc) doc->lpVtbl->Release(doc);
            *is_web = 0;
            return cur;
        }
        if (t == UIA_DocumentControlTypeId) {
            if (ax_has_value_pattern(cur)) {
                if (doc) doc->lpVtbl->Release(doc);
                *is_web = 0;
                return cur;
            }
            if (doc == NULL) {
                doc = cur;
                doc->lpVtbl->AddRef(doc);
            }
        }
        IUIAutomationElement *p = ax_parent(cur);
        cur->lpVtbl->Release(cur);
        cur = NULL; // 释放后置空：parent 为空时循环后的释放不得重复（防 double release）
        if (p == NULL) break;
        cur = p;
    }
    if (cur != NULL) cur->lpVtbl->Release(cur); // 仅循环跑满 16 层（未 break）时 cur 仍持有引用
    if (doc != NULL) {
        *is_web = ax_foreground_is_browser();
        return doc;
    }
    return NULL;
}

// ---------- 值/选中读取 ----------
static char *ax_value(IUIAutomationElement *e) {
    IUIAutomationValuePattern *vp = NULL;
    if (e->lpVtbl->GetCurrentPatternAs(e, UIA_ValuePatternId,
            &AX_IID_IUIAutomationValuePattern, (void **)&vp) != S_OK || vp == NULL) return NULL;
    BSTR b = NULL;
    HRESULT hr = vp->lpVtbl->get_CurrentValue(vp, &b);
    vp->lpVtbl->Release(vp);
    if (hr != S_OK || b == NULL) return NULL;
    char *out = ax_bstr_to_utf8(b);
    SysFreeString(b);
    return out;
}

// TextPattern 全量读取（无 ValuePattern 的富编辑器回退），截断到 max_chars。
static char *ax_textpattern_full(IUIAutomationElement *e, int max_chars) {
    IUIAutomationTextPattern *tp = NULL;
    if (e->lpVtbl->GetCurrentPatternAs(e, UIA_TextPatternId,
            &AX_IID_IUIAutomationTextPattern, (void **)&tp) != S_OK || tp == NULL) return NULL;
    IUIAutomationTextRange *doc = NULL;
    if (tp->lpVtbl->get_DocumentRange(tp, &doc) != S_OK || doc == NULL) {
        tp->lpVtbl->Release(tp);
        return NULL;
    }
    BSTR b = NULL;
    HRESULT hr = doc->lpVtbl->GetText(doc, max_chars, &b);
    doc->lpVtbl->Release(doc);
    tp->lpVtbl->Release(tp);
    if (hr != S_OK || b == NULL) return NULL;
    char *out = ax_bstr_to_utf8(b);
    SysFreeString(b);
    return out;
}

// 读取目标元素的完整文本：优先 ValuePattern；网页目标不读全量（与 darwin 一致，
// 仅读选中）；无 ValuePattern 的富编辑器退 TextPattern 全量（截断保护）。
static char *ax_value_for(IUIAutomationElement *e, int is_web) {
    char *v = ax_value(e);
    if (v != NULL) return v;
    if (is_web) return NULL;
    return ax_textpattern_full(e, 100000);
}

// ax_selection_hwnd: 经典 EDIT/RICHEDIT 控件（无 TextPattern，如 Win10 记事本）
// 的选区回退：EM_GETSEL + WM_GETTEXT 对标准 Edit 类跨进程有效（USER32 特殊
// 处理缓冲区），按 UTF-16 偏移切出选中文本。SendMessageTimeout 防目标无响应
// 挂死；仅对来自原生 HWND 的 UIA 元素可用。失败路径节流打印诊断（窗口类名
// 非 EDIT/RICHEDIT 的控件不响应 EM_GETSEL，属平台能力边界）。
static char *ax_selection_hwnd(IUIAutomationElement *e) {
    static DWORD g_last_hwnd_diag;
    UIA_HWND hwnd = NULL;
    HRESULT hr_hw = e->lpVtbl->get_CurrentNativeWindowHandle(e, &hwnd);
    if (hr_hw != S_OK || hwnd == NULL) {
        if (GetTickCount() - g_last_hwnd_diag > 10000) {
            g_last_hwnd_diag = GetTickCount();
            fprintf(stderr, "[dbg][win] HWND 回退: NativeWindowHandle hr=0x%08lX（非原生 Win32 控件）\n",
                (unsigned long)hr_hw);
        }
        return NULL;
    }
    DWORD_PTR start = 0, end = 0;
    LRESULT sr = 0;
    BOOL ok = SendMessageTimeoutW((HWND)hwnd, EM_GETSEL, (WPARAM)&start, (LPARAM)&end,
        SMTO_ABORTIFHUNG, 300, &sr);
    if (!ok || sr == 0) {
        if (GetTickCount() - g_last_hwnd_diag > 10000) {
            g_last_hwnd_diag = GetTickCount();
            wchar_t cls[64] = L"?";
            GetClassNameW((HWND)hwnd, cls, 64);
            fprintf(stderr, "[dbg][win] HWND 回退: class=%ls EM_GETSEL ok=%d sr=%lld\n",
                cls, (int)ok, (long long)sr);
        }
        return NULL;
    }
    if (start >= end) return NULL; // start==end 为光标态，无选区
    // 跨进程读文本：GetWindowTextLengthW/WM_GETTEXT 对跨进程 Edit 不可靠
    // （实测 GetWindowTextLengthW 返回 0，与 ValuePattern 读到的文本长度矛盾），
    // 改用 ValuePattern 读全文（BSTR 即 UTF-16，与 EM_GETSEL 偏移同编码），
    // 按 [start,end) 切片转 UTF-8。
    IUIAutomationValuePattern *vp = NULL;
    if (e->lpVtbl->GetCurrentPatternAs(e, UIA_ValuePatternId,
            &AX_IID_IUIAutomationValuePattern, (void **)&vp) != S_OK || vp == NULL) {
        if (GetTickCount() - g_last_hwnd_diag > 10000) {
            g_last_hwnd_diag = GetTickCount();
            fprintf(stderr, "[dbg][win] HWND 回退: 有选区但无 ValuePattern\n");
        }
        return NULL;
    }
    BSTR b = NULL;
    HRESULT hr = vp->lpVtbl->get_CurrentValue(vp, &b);
    vp->lpVtbl->Release(vp);
    if (hr != S_OK || b == NULL) return NULL;
    int total = SysStringLen(b);
    char *out = NULL;
    if ((DWORD_PTR)total >= end) {
        out = ax_wcs_to_utf8(b + start, (int)(end - start));
    }
    SysFreeString(b);
    return out;
}

// 拼接 TextPattern 当前选区文本（多选区拼接），无选区/失败返回 NULL。
// *diag 回传失败原因：0=无 TextPattern（且 HWND 回退失败），1=GetSelection 失败，
// 2=选区为空，3=读到选区文本，4=无 TextPattern 但 EM_GETSEL 回退读到选区。
static char *ax_selection(IUIAutomationElement *e, int *diag) {
    *diag = 0;
    IUIAutomationTextPattern *tp = NULL;
    if (e->lpVtbl->GetCurrentPatternAs(e, UIA_TextPatternId,
            &AX_IID_IUIAutomationTextPattern, (void **)&tp) != S_OK || tp == NULL) {
        char *s = ax_selection_hwnd(e);
        if (s != NULL) *diag = 4;
        return s;
    }
    IUIAutomationTextRangeArray *arr = NULL;
    if (tp->lpVtbl->GetSelection(tp, &arr) != S_OK || arr == NULL) {
        *diag = 1;
        tp->lpVtbl->Release(tp);
        return NULL;
    }
    int n = 0;
    arr->lpVtbl->get_Length(arr, &n);
    char *out = NULL;
    size_t cap = 0, len = 0;
    int i;
    for (i = 0; i < n; i++) {
        IUIAutomationTextRange *r = NULL;
        if (arr->lpVtbl->GetElement(arr, i, &r) != S_OK || r == NULL) continue;
        BSTR b = NULL;
        if (r->lpVtbl->GetText(r, -1, &b) == S_OK && b != NULL) {
            char *s = ax_bstr_to_utf8(b);
            if (s != NULL) {
                size_t sl = strlen(s);
                if (len + sl + 1 > cap) {
                    cap = len + sl + 1;
                    char *nw = (char *)realloc(out, cap);
                    if (nw == NULL) {
                        free(s);
                        SysFreeString(b);
                        r->lpVtbl->Release(r);
                        break;
                    }
                    out = nw;
                }
                memcpy(out + len, s, sl + 1);
                len += sl;
                free(s);
            }
            SysFreeString(b);
        }
        r->lpVtbl->Release(r);
    }
    arr->lpVtbl->Release(arr);
    tp->lpVtbl->Release(tp);
    *diag = (out != NULL) ? 3 : 2;
    return out;
}

// ---------- 快照结构 ----------
typedef struct {
    int is_web;
    int is_password;
    int ctype;     // UIA 控件类型 ID（诊断：Edit=50004/Document=50030/其他值）
    int sel_diag;  // 选中读取诊断码（见 ax_selection）
    char *role;
    char *text;
    char *sel;
} ax_light;

static void ax_light_free(ax_light *s) {
    if (s == NULL) return;
    free(s->role);
    free(s->text);
    free(s->sel);
    free(s);
}

static char *ax_role_name(int t) {
    switch (t) {
    case UIA_EditControlTypeId: return ax_strdup8("Edit");
    case UIA_DocumentControlTypeId: return ax_strdup8("Document");
    default: return ax_strdup8("Other");
    }
}

static ax_light *ax_snapshot_light(IUIAutomationElement *e, int is_web) {
    ax_light *s = (ax_light *)calloc(1, sizeof(ax_light));
    if (s == NULL) return NULL;
    s->ctype = ax_control_type(e);
    s->role = ax_role_name(s->ctype);
    s->is_password = ax_is_password(e);
    s->is_web = is_web;
    if (is_web) {
        s->sel = ax_selection(e, &s->sel_diag);
    } else {
        s->text = ax_value_for(e, 0);
        s->sel = ax_selection(e, &s->sel_diag);
    }
    return s;
}

// ---------- 重量上下文 ----------
typedef struct {
    char *app;
    int has_frame;
    double x, y, w, h;
} ax_context;

static void ax_context_free(ax_context *s) {
    if (s == NULL) return;
    free(s->app);
    free(s);
}

// ax_mouse_pos: 全局鼠标位置（屏幕物理像素，原点主屏左上，y 向下，与 UIA 一致）。
static void ax_mouse_pos(double *x, double *y) {
    POINT p = {0, 0};
    GetCursorPos(&p);
    *x = (double)p.x;
    *y = (double)p.y;
}

static char *ax_app_title(void) {
    HWND fg = GetForegroundWindow();
    if (fg == NULL) return NULL;
    wchar_t buf[512];
    int n = GetWindowTextW(fg, buf, 512);
    if (n <= 0) return NULL;
    return ax_wcs_to_utf8(buf, n);
}

static int ax_get_frame(IUIAutomationElement *e, double *x, double *y, double *w, double *h) {
    RECT r = {0, 0, 0, 0};
    if (e->lpVtbl->get_CurrentBoundingRectangle(e, &r) != S_OK) return 0;
    *x = (double)r.left;
    *y = (double)r.top;
    *w = (double)(r.right - r.left);
    *h = (double)(r.bottom - r.top);
    return 1;
}

static ax_context *ax_snapshot_context(IUIAutomationElement *e, int is_web) {
    ax_context *s = (ax_context *)calloc(1, sizeof(ax_context));
    if (s == NULL) return NULL;
    s->app = ax_app_title();
    if (is_web) {
        // 网页正文无输入框 frame：气泡锚点用鼠标位置（选中操作终点附近）。
        ax_mouse_pos(&s->x, &s->y);
        s->w = 1;
        s->h = 1;
        s->has_frame = 1;
    } else {
        s->has_frame = ax_get_frame(e, &s->x, &s->y, &s->w, &s->h);
    }
    return s;
}

// ---------- 写回 ----------
// mingw-w64 的 uiautomationclient.h 把 IUIAutomationTextRange::FindText 的
// 第 4 参数误写为 WINBOOL（真实 Windows ABI 为 IUIAutomationTextRange *start，
// 与 Windows SDK 一致）。x64 上第 4 参数经 R9 寄存器传递，按 BOOL 编译只会
// 设置低 32 位，DLL 会把 R9 当指针解引用导致崩溃。按 vtable 序数（IUnknown 3
// 槽 + Clone/Compare/CompareEndpoints/ExpandToEnclosingUnit/FindAttribute/FindText，
// 与 mingw 头文件方法顺序一致）手写正确签名调用。
typedef struct AX_TextRangeVtbl {
    HRESULT (*QueryInterface)(IUIAutomationTextRange *, REFIID, void **);
    ULONG (*AddRef)(IUIAutomationTextRange *);
    ULONG (*Release)(IUIAutomationTextRange *);
    HRESULT (*Clone)(IUIAutomationTextRange *, IUIAutomationTextRange **);
    HRESULT (*Compare)(IUIAutomationTextRange *, IUIAutomationTextRange *, WINBOOL *);
    HRESULT (*CompareEndpoints)(IUIAutomationTextRange *, int, IUIAutomationTextRange *, int, int *);
    HRESULT (*ExpandToEnclosingUnit)(IUIAutomationTextRange *, int);
    HRESULT (*FindAttribute)(IUIAutomationTextRange *, int, VARIANT, WINBOOL, IUIAutomationTextRange **);
    HRESULT (*FindText)(IUIAutomationTextRange *, BSTR, WINBOOL, IUIAutomationTextRange *, IUIAutomationTextRange **);
} AX_TextRangeVtbl;

// ax_range_find_text: 正确 ABI 的 FindText（start 传 NULL 从头/尾开始搜索）。
static HRESULT ax_range_find_text(IUIAutomationTextRange *range, BSTR text,
                                  WINBOOL backward, IUIAutomationTextRange **found) {
    AX_TextRangeVtbl *v = (AX_TextRangeVtbl *)range->lpVtbl;
    return v->FindText(range, text, backward, NULL, found);
}

static int ax_set_value(IUIAutomationElement *e, const char *text) {
    IUIAutomationValuePattern *vp = NULL;
    if (e->lpVtbl->GetCurrentPatternAs(e, UIA_ValuePatternId,
            &AX_IID_IUIAutomationValuePattern, (void **)&vp) != S_OK || vp == NULL) return 0;
    BSTR b = ax_utf8_to_bstr(text);
    if (b == NULL) {
        vp->lpVtbl->Release(vp);
        return 0;
    }
    HRESULT hr = vp->lpVtbl->SetValue(vp, b);
    SysFreeString(b);
    vp->lpVtbl->Release(vp);
    return SUCCEEDED(hr) ? 1 : 0;
}

// ax_select_text: 在目标元素内从末尾向前定位 original 并选中（与 darwin 的
// LastIndex 语义一致），供调用方 Ctrl+V 粘贴替换选中区间。成功返回 1。
static int ax_select_text(IUIAutomationElement *e, const char *original) {
    IUIAutomationTextPattern *tp = NULL;
    if (e->lpVtbl->GetCurrentPatternAs(e, UIA_TextPatternId,
            &AX_IID_IUIAutomationTextPattern, (void **)&tp) != S_OK || tp == NULL) return 0;
    IUIAutomationTextRange *doc = NULL;
    if (tp->lpVtbl->get_DocumentRange(tp, &doc) != S_OK || doc == NULL) {
        tp->lpVtbl->Release(tp);
        return 0;
    }
    BSTR b = ax_utf8_to_bstr(original);
    if (b == NULL) {
        doc->lpVtbl->Release(doc);
        tp->lpVtbl->Release(tp);
        return 0;
    }
    IUIAutomationTextRange *found = NULL;
    HRESULT hr = ax_range_find_text(doc, b, TRUE, &found);
    SysFreeString(b);
    doc->lpVtbl->Release(doc);
    tp->lpVtbl->Release(tp);
    if (hr != S_OK || found == NULL) return 0;
    hr = found->lpVtbl->Select(found);
    found->lpVtbl->Release(found);
    return SUCCEEDED(hr) ? 1 : 0;
}
*/
import "C"

import (
	"fmt"
	"strings"
	"time"
	"unsafe"
)

// Trusted 检查辅助功能授权：Windows UI Automation 无需特殊权限，恒为已授权。
func Trusted() bool {
	return true
}

// PromptTrust 触发系统授权弹窗：Windows 无需授权，空操作。
func PromptTrust() {}

// MousePos 返回全局鼠标位置（屏幕物理像素，原点主屏左上，y 向下，与 UIA 坐标一致）。
// 剪贴板触发无输入框锚点，以鼠标位置作为气泡锚点。
// 可在任意主线程时机调用。
func MousePos() (x, y float64) {
	var cx, cy C.double
	C.ax_mouse_pos(&cx, &cy)
	return float64(cx), float64(cy)
}

// goString 把 C 字符串拷贝为 Go string。不释放：snapshot 字段由对应 free 函数统一释放。
func goString(p *C.char) string {
	if p == nil {
		return ""
	}
	return C.GoString(p)
}

// ReadFocused 读取当前焦点输入框的文本、应用名与位置（轻量+重量组合）。
// 必须在主线程（已 LockOSThread 的 goroutine）中调用。
func ReadFocused() Result {
	res := Result{Skip: SkipNotText}
	light := ReadFocusedLight()
	if light.Skip != SkipNone {
		res.Skip = light.Skip
		return res
	}
	ctx := ReadFocusedContext()
	res.Text = light.Text
	res.Selection = light.Selection
	res.App = ctx.App
	res.Rect = ctx.Rect
	res.IsWeb = light.IsWeb
	return res
}

// lastAXDiag 节流 AX 读取诊断打印（10 秒一次）：高频轮询路径上仅打印关键
// 静默分支（焦点读取失败/目标定位失败/选中为空），避免刷屏。用户选中文本
// 后若日志持续无新输出，即可据此缩小问题范围。
var lastAXDiag time.Time

// dbgAXDiag 节流打印 AX 读取诊断。
func dbgAXDiag(format string, args ...any) {
	if time.Since(lastAXDiag) < 10*time.Second {
		return
	}
	lastAXDiag = time.Now()
	fmt.Printf(format, args...)
}

// ReadFocusedLight 轻量读取焦点输入框文本与选中（仅少量 UIA 调用，不读窗口标题与位置）。
// 高频轮询路径：位置/应用名等展示上下文由 ReadFocusedContext 在判定通过时补充。
// 必须在主线程（已 LockOSThread 的 goroutine）中调用。
func ReadFocusedLight() LightResult {
	res := LightResult{Skip: SkipNotText}

	focused := C.ax_focused_element()
	if focused == nil {
		dbgAXDiag("[dbg][win] AX 焦点读取失败（无焦点元素或 UIA 不可用）\n")
		return res
	}
	defer C.ax_release(focused)

	var isWeb C.int
	elem := C.ax_find_target(focused, &isWeb)
	if elem == nil {
		dbgAXDiag("[dbg][win] AX 目标定位失败: 焦点控件类型=%d（无 Edit/Document 祖先）\n",
			int(C.ax_control_type(focused)))
		return res
	}
	defer C.ax_release(elem)

	snap := C.ax_snapshot_light(elem, isWeb)
	if snap == nil {
		res.Skip = SkipNoAccess
		return res
	}
	defer C.ax_light_free(snap)

	if snap.is_password != 0 {
		res.Skip = SkipSecure
		return res
	}
	res.Role = goString(snap.role)
	res.IsWeb = isWeb != 0
	res.Text = goString(snap.text)
	res.Selection = goString(snap.sel)
	if res.Text == "" {
		if res.IsWeb {
			dbgAXDiag("[dbg][win] 网页正文选中为空: role=%s ctype=%d selDiag=%d\n",
				res.Role, int(snap.ctype), int(snap.sel_diag))
			res.Skip = SkipNone // 网页无输入框值：选中为空时由 poll 静默跳过
			return res
		}
		res.Skip = SkipNoAccess
		return res
	}
	res.Skip = SkipNone
	if res.Selection == "" {
		dbgAXDiag("[dbg][win] 选中为空: role=%s ctype=%d isWeb=%v selDiag=%d textLen=%d\n",
			res.Role, int(snap.ctype), false, int(snap.sel_diag), len(res.Text))
	}
	return res
}

// ReadFocusedContext 读取展示上下文：应用名、输入框位置与选中范围。
// 仅在判定通过、准备投递纠正时调用一次（低频，人打字节奏下可忽略）。
// 必须在主线程（已 LockOSThread 的 goroutine）中调用。
func ReadFocusedContext() Context {
	var ctx Context

	focused := C.ax_focused_element()
	if focused == nil {
		return ctx
	}
	defer C.ax_release(focused)

	var isWeb C.int
	elem := C.ax_find_target(focused, &isWeb)
	if elem == nil {
		return ctx
	}
	defer C.ax_release(elem)

	snap := C.ax_snapshot_context(elem, isWeb)
	if snap == nil {
		return ctx
	}
	defer C.ax_context_free(snap)

	ctx.App = goString(snap.app)
	ctx.IsWeb = isWeb != 0
	if snap.has_frame != 0 {
		ctx.Rect = Rect{
			X: float64(snap.x), Y: float64(snap.y),
			W: float64(snap.w), H: float64(snap.h),
		}
	}
	return ctx
}

// ReplaceFocused 用 corrected 替换焦点输入框中 original 对应的区域。
// Windows 的 UIA TextRange 是不透明对象、无数值区间，selRange 不可用，
// 统一替换 original 在字段文本中的最后一次出现。写回优先 ValuePattern
// SetValue，成功返回 applied=true；失败则 TextPattern 从末尾定位选中原文
// 并返回 needPaste=true（由调用方模拟 Ctrl+V 粘贴，粘贴会替换选中区间）。
func ReplaceFocused(original, corrected string, selRange Range) (applied, needPaste bool) {
	if original == "" || corrected == "" {
		return false, false
	}

	focused := C.ax_focused_element()
	if focused == nil {
		return false, false
	}
	defer C.ax_release(focused)

	var isWeb C.int
	elem := C.ax_find_target(focused, &isWeb)
	if elem == nil {
		return false, false
	}
	defer C.ax_release(elem)
	if isWeb != 0 {
		return false, false // 网页正文不可写回（由调用方降级为复制）
	}

	// 优先 ValuePattern 全量写回：读当前值定位原文最后一次出现，拼新文本整体替换。
	if p := C.ax_value(elem); p != nil {
		cur := C.GoString(p)
		C.free(unsafe.Pointer(p))
		if cur != "" {
			idx := strings.LastIndex(cur, original)
			if idx >= 0 {
				newText := cur[:idx] + corrected + cur[idx+len(original):]
				cs := C.CString(newText)
				ok := C.ax_set_value(elem, cs) != 0
				C.free(unsafe.Pointer(cs))
				if ok {
					return true, false
				}
			}
		}
	}

	// 写回失败/不可用：TextPattern 从末尾定位选中原文，交由调用方粘贴兜底。
	cs := C.CString(original)
	ok := C.ax_select_text(elem, cs) != 0
	C.free(unsafe.Pointer(cs))
	return false, ok
}
