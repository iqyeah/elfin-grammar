//go:build darwin

package ax

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation

#include <ApplicationServices/ApplicationServices.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// ax_is_trusted: prompt != 0 时传入 kAXTrustedCheckOptionPrompt 触发系统授权弹窗。
static int ax_is_trusted(int prompt) {
    if (prompt) {
        CFStringRef key = CFSTR("AXTrustedCheckOptionPrompt");
        CFDictionaryRef opts = CFDictionaryCreate(NULL,
            (const void **)&key, (const void **)&kCFBooleanTrue, 1,
            &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
        if (opts == NULL) return 0;
        Boolean trusted = AXIsProcessTrustedWithOptions(opts);
        CFRelease(opts);
        return trusted ? 1 : 0;
    }
    return AXIsProcessTrustedWithOptions(NULL) ? 1 : 0;
}

// ax_copy_string: 读字符串属性，返回 malloc 的 UTF-8（调用者 free）。失败返回 NULL。
static char *ax_copy_string(AXUIElementRef ref, CFStringRef attr) {
    CFTypeRef val = NULL;
    if (AXUIElementCopyAttributeValue(ref, attr, &val) != kAXErrorSuccess || val == NULL) {
        return NULL;
    }
    char *out = NULL;
    if (CFGetTypeID(val) == CFStringGetTypeID()) {
        CFStringRef s = (CFStringRef)val;
        CFIndex len = CFStringGetLength(s);
        CFIndex maxLen = CFStringGetMaximumSizeForEncoding(len, kCFStringEncodingUTF8) + 1;
        out = (char *)malloc(maxLen);
        if (out != NULL && !CFStringGetCString(s, out, maxLen, kCFStringEncodingUTF8)) {
            free(out);
            out = NULL;
        }
    }
    CFRelease(val);
    return out;
}

// ax_copy_parent: 取父元素（Copy 引用，调用者 ax_release）。
// 防御：服务端状态竞态下属性值可能不是 AXUIElement，类型不符按无父元素处理。
static AXUIElementRef ax_copy_parent(AXUIElementRef ref) {
    AXUIElementRef parent = NULL;
    if (AXUIElementCopyAttributeValue(ref, kAXParentAttribute, (CFTypeRef *)&parent) != kAXErrorSuccess ||
        parent == NULL) {
        return NULL;
    }
    if (CFGetTypeID(parent) != AXUIElementGetTypeID()) {
        CFRelease(parent);
        return NULL;
    }
    return parent;
}

// ax_copy_value_text: 优先 kAXValueAttribute，空则退 kAXSelectedTextAttribute。
static char *ax_copy_value_text(AXUIElementRef ref) {
    char *s = ax_copy_string(ref, kAXValueAttribute);
    if (s != NULL && s[0] != '\0') return s;
    free(s);
    return ax_copy_string(ref, kAXSelectedTextAttribute);
}

// ax_get_sel_range: 读 kAXSelectedTextRangeAttribute（CFRange，UTF-16 码元单位）。
static int ax_get_sel_range(AXUIElementRef ref, long *loc, long *len) {
    CFTypeRef val = NULL;
    if (AXUIElementCopyAttributeValue(ref, kAXSelectedTextRangeAttribute, &val) != kAXErrorSuccess || val == NULL) {
        return 0;
    }
    int ok = 0;
    if (CFGetTypeID(val) == AXValueGetTypeID()) {
        CFRange r = {0, 0};
        if (AXValueGetValue((AXValueRef)val, kAXValueTypeCFRange, &r)) {
            *loc = (long)r.location;
            *len = (long)r.length;
            ok = 1;
        }
    }
    CFRelease(val);
    return ok;
}

// ax_set_sel_range: 设置选中范围（CFRange，UTF-16 码元单位）。
static int ax_set_sel_range(AXUIElementRef ref, long loc, long len) {
    CFRange r = {loc, len};
    AXValueRef v = AXValueCreate(kAXValueTypeCFRange, &r);
    if (v == NULL) return 0;
    AXError err = AXUIElementSetAttributeValue(ref, kAXSelectedTextRangeAttribute, v);
    CFRelease(v);
    return err == kAXErrorSuccess;
}

// ax_set_value: 设置 kAXValueAttribute 文本。
static int ax_set_value(AXUIElementRef ref, const char *text) {
    CFStringRef s = CFStringCreateWithCString(NULL, text, kCFStringEncodingUTF8);
    if (s == NULL) return 0;
    AXError err = AXUIElementSetAttributeValue(ref, kAXValueAttribute, s);
    CFRelease(s);
    return err == kAXErrorSuccess;
}

// ax_find_text_ancestor: 从 ref 起向上（含自身，最多 16 层）定位读取目标：
// 优先 AXTextField/AXTextArea（输入框）；未命中但存在 AXWebArea（浏览器网页正文）
// 时以其为目标并置 *is_web=1。找到时 *out 为 Copy 引用（调用者 ax_release）返回 1。
// 网页元素嵌套较深（选中文本落在 AXStaticText 内多级），故层数上限从 4 放宽到 16。
static int ax_find_text_ancestor(AXUIElementRef ref, AXUIElementRef *out, int *is_web) {
    AXUIElementRef web = NULL;
    AXUIElementRef cur = ref;
    CFRetain(cur);
    for (int i = 0; i < 16; i++) {
        char *role = ax_copy_string(cur, kAXRoleAttribute);
        int isText = role != NULL &&
            (strcmp(role, "AXTextField") == 0 || strcmp(role, "AXTextArea") == 0);
        if (isText) {
            free(role);
            if (web != NULL) CFRelease(web);
            *out = cur;
            *is_web = 0;
            return 1;
        }
        if (web == NULL && role != NULL && strcmp(role, "AXWebArea") == 0) {
            web = cur;
            CFRetain(web);
        }
        free(role);
        AXUIElementRef parent = ax_copy_parent(cur);
        CFRelease(cur);
        cur = NULL; // 释放后置空：parent 为空时循环后的释放不得重复（防 double release）
        if (parent == NULL) break;
        cur = parent;
    }
    if (cur != NULL) CFRelease(cur); // 仅循环跑满 16 层（未 break）时 cur 仍持有引用
    if (web != NULL) {
        *out = web;
        *is_web = 1;
        return 1;
    }
    return 0;
}

// ax_app_title: 向上找 AXApplication 读 kAXTitleAttribute。
static char *ax_app_title(AXUIElementRef ref) {
    AXUIElementRef cur = ref;
    CFRetain(cur);
    for (int i = 0; i < 16; i++) {
        char *role = ax_copy_string(cur, kAXRoleAttribute);
        int isApp = role != NULL && strcmp(role, "AXApplication") == 0;
        free(role);
        if (isApp) {
            char *t = ax_copy_string(cur, kAXTitleAttribute);
            CFRelease(cur);
            return t;
        }
        AXUIElementRef parent = ax_copy_parent(cur);
        CFRelease(cur);
        if (parent == NULL) return NULL;
        cur = parent;
    }
    CFRelease(cur);
    return NULL;
}

// ax_get_frame: 读 kAXPositionAttribute / kAXSizeAttribute（AX 坐标：原点主屏左上，y 向下）。
static int ax_get_frame(AXUIElementRef ref, double *x, double *y, double *w, double *h) {
    CFTypeRef posVal = NULL, sizeVal = NULL;
    CGPoint p = {0, 0};
    CGSize s = {0, 0};
    int ok = 0;
    if (AXUIElementCopyAttributeValue(ref, kAXPositionAttribute, &posVal) != kAXErrorSuccess ||
        AXUIElementCopyAttributeValue(ref, kAXSizeAttribute, &sizeVal) != kAXErrorSuccess) {
        goto done;
    }
    if (CFGetTypeID(posVal) != AXValueGetTypeID() || CFGetTypeID(sizeVal) != AXValueGetTypeID()) {
        goto done;
    }
    if (!AXValueGetValue((AXValueRef)posVal, kAXValueTypeCGPoint, &p) ||
        !AXValueGetValue((AXValueRef)sizeVal, kAXValueTypeCGSize, &s)) {
        goto done;
    }
    *x = p.x; *y = p.y; *w = s.width; *h = s.height;
    ok = 1;
done:
    if (posVal) CFRelease(posVal);
    if (sizeVal) CFRelease(sizeVal);
    return ok;
}

// ax_snapshot: 一次采集的元素信息。所有 char* 为 malloc 的 UTF-8（ax_snapshot_free 释放）。
typedef struct {
    char *role;
    char *subrole;
    char *text;
    char *sel;   // kAXSelectedTextAttribute：当前选中文本（可能为空/NULL）
    char *app;
    int has_frame;
    double x, y, w, h;
    int has_sel_range; // 是否成功读取选中范围
    long sel_loc, sel_len;
} ax_snapshot;

static void ax_snapshot_free(ax_snapshot *s) {
    if (s == NULL) return;
    free(s->role);
    free(s->subrole);
    free(s->text);
    free(s->sel);
    free(s->app);
    free(s);
}

static ax_snapshot *ax_snapshot_element(AXUIElementRef ref) {
    ax_snapshot *s = (ax_snapshot *)calloc(1, sizeof(ax_snapshot));
    if (s == NULL) return NULL;
    s->role = ax_copy_string(ref, kAXRoleAttribute);
    s->subrole = ax_copy_string(ref, kAXSubroleAttribute);
    s->app = ax_app_title(ref);
    s->has_frame = ax_get_frame(ref, &s->x, &s->y, &s->w, &s->h);
    if (s->role != NULL && strcmp(s->role, "AXWebArea") == 0) {
        // 网页正文：无 kAXValueAttribute（输入框语义），仅读选中文本。
        s->sel = ax_copy_string(ref, kAXSelectedTextAttribute);
    } else {
        s->text = ax_copy_value_text(ref);
        s->sel = ax_copy_string(ref, kAXSelectedTextAttribute);
    }
    s->has_sel_range = ax_get_sel_range(ref, &s->sel_loc, &s->sel_len);
    return s;
}

// ---------- 轻量快照（高频轮询路径）：只读 text/sel/role/subrole ----------
typedef struct {
    char *role;
    char *subrole;
    char *text;
    char *sel;
} ax_light;

static void ax_light_free(ax_light *s) {
    if (s == NULL) return;
    free(s->role);
    free(s->subrole);
    free(s->text);
    free(s->sel);
    free(s);
}

static ax_light *ax_snapshot_light(AXUIElementRef ref) {
    ax_light *s = (ax_light *)calloc(1, sizeof(ax_light));
    if (s == NULL) return NULL;
    s->role = ax_copy_string(ref, kAXRoleAttribute);
    s->subrole = ax_copy_string(ref, kAXSubroleAttribute);
    if (s->role != NULL && strcmp(s->role, "AXWebArea") == 0) {
        // 网页正文：无 kAXValueAttribute（输入框语义），仅读选中文本。
        s->sel = ax_copy_string(ref, kAXSelectedTextAttribute);
    } else {
        s->text = ax_copy_value_text(ref);
        s->sel = ax_copy_string(ref, kAXSelectedTextAttribute);
    }
    return s;
}

// ---------- 重量上下文（触发展示时才读一次）：app 标题/frame/选中范围 ----------
typedef struct {
    char *app;
    int has_frame;
    double x, y, w, h;
    int has_sel_range;
    long sel_loc, sel_len;
} ax_context;

static void ax_context_free(ax_context *s) {
    if (s == NULL) return;
    free(s->app);
    free(s);
}

// ax_mouse_pos: 读全局鼠标位置（CG 坐标：原点主屏左上，y 向下，与 AX 坐标一致）。
// 网页正文无输入框 frame，以鼠标当前位置作为气泡锚点。
static void ax_mouse_pos(double *x, double *y) {
    CGEventRef e = CGEventCreate(NULL);
    if (e == NULL) return;
    CGPoint p = CGEventGetLocation(e);
    CFRelease(e);
    *x = p.x;
    *y = p.y;
}

static ax_context *ax_snapshot_context(AXUIElementRef ref) {
    ax_context *s = (ax_context *)calloc(1, sizeof(ax_context));
    if (s == NULL) return NULL;
    s->app = ax_app_title(ref);
    char *role = ax_copy_string(ref, kAXRoleAttribute);
    int isWeb = role != NULL && strcmp(role, "AXWebArea") == 0;
    free(role);
    if (isWeb) {
        // 网页无输入框：气泡锚点用鼠标位置（选中操作终点附近）。
        ax_mouse_pos(&s->x, &s->y);
        s->w = 1;
        s->h = 1;
        s->has_frame = 1;
    } else {
        s->has_frame = ax_get_frame(ref, &s->x, &s->y, &s->w, &s->h);
    }
    s->has_sel_range = ax_get_sel_range(ref, &s->sel_loc, &s->sel_len);
    return s;
}

// ax_focused_element: 系统级元素 -> kAXFocusedUIElementAttribute。
// 成功时 *out 为 Copy 引用（调用者 ax_release）并返回 1；否则返回 0。
static int ax_focused_element(AXUIElementRef *out) {
    AXUIElementRef sys = AXUIElementCreateSystemWide();
    if (sys == NULL) return 0;
    AXUIElementRef focused = NULL;
    AXError err = AXUIElementCopyAttributeValue(sys, kAXFocusedUIElementAttribute,
        (CFTypeRef *)&focused);
    CFRelease(sys);
    if (err != kAXErrorSuccess || focused == NULL) return 0;
    // 防御：属性值类型不符（服务端状态竞态）按无焦点处理。
    if (CFGetTypeID(focused) != AXUIElementGetTypeID()) {
        CFRelease(focused);
        return 0;
    }
    *out = focused;
    return 1;
}

static void ax_release(AXUIElementRef ref) {
    if (ref) CFRelease(ref);
}
*/
import "C"

import (
	"strings"
	"unicode/utf16"
	"unsafe"
)

// Trusted 检查当前进程是否已获得辅助功能权限。
func Trusted() bool {
	return C.ax_is_trusted(0) != 0
}

// PromptTrust 触发系统授权弹窗（若尚未授权）。
func PromptTrust() {
	C.ax_is_trusted(1)
}

// MousePos 返回全局鼠标位置（CG 坐标：原点主屏左上，y 向下，与 AX 坐标一致）。
// 剪贴板触发无输入框锚点，以鼠标位置作为气泡锚点。
// 不依赖辅助功能权限，可在任意主线程时机调用。
func MousePos() (x, y float64) {
	var cx, cy C.double
	C.ax_mouse_pos(&cx, &cy)
	return float64(cx), float64(cy)
}

// ReadFocused 读取当前焦点输入框的文本、应用名与位置。
// 必须在主线程（已 LockOSThread 的 goroutine）中调用。
func ReadFocused() Result {
	res := Result{Skip: SkipNotText}

	var focused C.AXUIElementRef
	if C.ax_focused_element(&focused) == 0 {
		return res
	}
	defer C.ax_release(focused)

	var elem C.AXUIElementRef
	var isWeb C.int
	if C.ax_find_text_ancestor(focused, &elem, &isWeb) == 0 {
		return res
	}
	defer C.ax_release(elem)

	snap := C.ax_snapshot_element(elem)
	if snap == nil {
		res.Skip = SkipNoAccess
		return res
	}
	defer C.ax_snapshot_free(snap)

	res.App = goString(snap.app)
	sub := goString(snap.subrole)
	role := goString(snap.role)
	if sub == "AXSecureTextField" || role == "AXSecureTextField" {
		res.Skip = SkipSecure
		return res
	}
	if snap.has_frame != 0 {
		res.Rect = Rect{
			X: float64(snap.x), Y: float64(snap.y),
			W: float64(snap.w), H: float64(snap.h),
		}
	}
	res.Text = goString(snap.text)
	res.Selection = goString(snap.sel)
	res.IsWeb = isWeb != 0
	if snap.has_sel_range != 0 {
		res.SelRange = Range{Has: true, Loc: int(snap.sel_loc), Len: int(snap.sel_len)}
	}
	if res.Text == "" {
		if res.IsWeb {
			res.Skip = SkipNone // 网页无输入框值：选中为空时由 poll 静默跳过
			return res
		}
		res.Skip = SkipNoAccess
		return res
	}
	res.Skip = SkipNone
	return res
}

// ReadFocusedLight 轻量读取焦点输入框文本与选中（仅 6-8 次 AX IPC，不读 app 标题与位置）。
// 高频轮询路径：位置/应用名等展示上下文由 ReadFocusedContext 在判定通过时补充。
// 必须在主线程（已 LockOSThread 的 goroutine）中调用。
func ReadFocusedLight() LightResult {
	res := LightResult{Skip: SkipNotText}

	var focused C.AXUIElementRef
	if C.ax_focused_element(&focused) == 0 {
		return res
	}
	defer C.ax_release(focused)

	var elem C.AXUIElementRef
	var isWeb C.int
	if C.ax_find_text_ancestor(focused, &elem, &isWeb) == 0 {
		return res
	}
	defer C.ax_release(elem)

	snap := C.ax_snapshot_light(elem)
	if snap == nil {
		res.Skip = SkipNoAccess
		return res
	}
	defer C.ax_light_free(snap)

	sub := goString(snap.subrole)
	role := goString(snap.role)
	if sub == "AXSecureTextField" || role == "AXSecureTextField" {
		res.Skip = SkipSecure
		return res
	}
	res.Text = goString(snap.text)
	res.Selection = goString(snap.sel)
	res.Role = role
	res.IsWeb = isWeb != 0
	if res.Text == "" {
		if res.IsWeb {
			res.Skip = SkipNone // 网页无输入框值：选中为空时由 poll 静默跳过
			return res
		}
		res.Skip = SkipNoAccess
		return res
	}
	res.Skip = SkipNone
	return res
}

// ReadFocusedContext 读取展示上下文：应用名、输入框位置与选中范围。
// 仅在判定通过、准备投递纠正时调用一次（低频，人打字节奏下可忽略）。
// 必须在主线程（已 LockOSThread 的 goroutine）中调用。
func ReadFocusedContext() Context {
	var ctx Context

	var focused C.AXUIElementRef
	if C.ax_focused_element(&focused) == 0 {
		return ctx
	}
	defer C.ax_release(focused)

	var elem C.AXUIElementRef
	var isWeb C.int
	if C.ax_find_text_ancestor(focused, &elem, &isWeb) == 0 {
		return ctx
	}
	defer C.ax_release(elem)

	snap := C.ax_snapshot_context(elem)
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
	if snap.has_sel_range != 0 {
		ctx.SelRange = Range{Has: true, Loc: int(snap.sel_loc), Len: int(snap.sel_len)}
	}
	return ctx
}

// goString 把 C 字符串拷贝为 Go string。不释放：snapshot 字段由 ax_snapshot_free 统一释放。
func goString(p *C.char) string {
	if p == nil {
		return ""
	}
	return C.GoString(p)
}

// ReplaceFocused 用 corrected 替换焦点输入框中 original 对应的区域：
// selRange.Has 且当前文本该区间仍等于 original 时用选中范围，否则替换
// original 在字段文本中的最后一次出现。写回优先 AX set value，成功返回
// applied=true；失败则尝试选中定位区间并返回 needPaste=true（由调用方
// 模拟 Cmd+V 粘贴，粘贴会替换选中区间，保证替换边界）。
func ReplaceFocused(original, corrected string, selRange Range) (applied, needPaste bool) {
	var focused C.AXUIElementRef
	if C.ax_focused_element(&focused) == 0 {
		return false, false
	}
	defer C.ax_release(focused)

	var elem C.AXUIElementRef
	var isWeb C.int
	if C.ax_find_text_ancestor(focused, &elem, &isWeb) == 0 {
		return false, false
	}
	defer C.ax_release(elem)

	p := C.ax_copy_value_text(elem)
	if p == nil {
		return false, false
	}
	cur := C.GoString(p)
	C.free(unsafe.Pointer(p))
	if cur == "" || original == "" {
		return false, false
	}

	// 定位替换区间（UTF-16 码元单位，与 AX 选中范围一致）。
	loc16, len16 := -1, -1
	if selRange.Has {
		if sub, ok := utf16Slice(cur, selRange.Loc, selRange.Len); ok && sub == original {
			loc16, len16 = selRange.Loc, selRange.Len
		}
	}
	if loc16 < 0 {
		idx := strings.LastIndex(cur, original)
		if idx < 0 {
			return false, false
		}
		loc16 = len(utf16.Encode([]rune(cur[:idx])))
		len16 = len(utf16.Encode([]rune(original)))
	}

	byteIdx, ok := utf16ToByte(cur, loc16)
	if !ok || byteIdx+len(original) > len(cur) || cur[byteIdx:byteIdx+len(original)] != original {
		return false, false
	}

	// 优先 AX set value。
	newText := cur[:byteIdx] + corrected + cur[byteIdx+len(original):]
	cs := C.CString(newText)
	ok = C.ax_set_value(elem, cs) != 0
	C.free(unsafe.Pointer(cs))
	if ok {
		// caret 移到替换文本末尾。
		caret := loc16 + len(utf16.Encode([]rune(corrected)))
		C.ax_set_sel_range(elem, C.long(caret), 0)
		return true, false
	}

	// 写回失败：选中定位区间，交由调用方粘贴兜底。
	C.ax_set_sel_range(elem, C.long(loc16), C.long(len16))
	return false, true
}
