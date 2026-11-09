//go:build darwin

// selrepro 临时诊断：模拟鼠标拖选文本后，生产 ReadFocused 是否上报选中内容（对比键盘式选中）。
// 用法：CGO_ENABLED=1 go run ./cmd/selrepro
package main

/*
#cgo LDFLAGS: -framework AppKit -framework CoreGraphics -framework ApplicationServices -framework CoreFoundation

// CGO 按纯 C 编译，不能包含 AppKit 头文件（含 ObjC 语法），
// 仅声明入口并通过 objc runtime 调用（与 internal/appkit 相同模式）。

#include <objc/runtime.h>
#include <objc/message.h>
#include <Carbon/Carbon.h>
#include <CoreGraphics/CoreGraphics.h>
#include <ApplicationServices/ApplicationServices.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stddef.h>
#include <unistd.h>

extern Boolean NSApplicationLoad(void);

typedef struct { double x, y; } NSPoint_C;
typedef struct { double w, h; } NSSize_C;
typedef struct { NSPoint_C origin; NSSize_C size; } NSRect_C;

static id g_win;
static id g_field;   // NSTextField
static id g_view;    // NSTextView
static id g_cur;     // 当前目标控件

static NSRect_C msg_rect_stret(id obj, const char *selname) {
    NSRect_C r = {{0,0},{0,0}};
#if defined(__x86_64__)
    ((void(*)(void*, id, SEL))objc_msgSend_stret)(&r, obj, sel_registerName(selname));
#else
    r = ((NSRect_C(*)(id, SEL))objc_msgSend)(obj, sel_registerName(selname));
#endif
    return r;
}

static void pump(double s) {
    CFRunLoopRunInMode(kCFRunLoopDefaultMode, s, true);
}

// ---- 事件派发（无 [NSApp run]，必须手动 nextEvent+sendEvent）----
static int pump_events(void) {
    id app = ((id(*)(id, SEL))objc_msgSend)((id)objc_getClass("NSApplication"), sel_registerName("sharedApplication"));
    if (app == NULL) return 0;
    id pool = ((id(*)(id, SEL))objc_msgSend)(
        ((id(*)(id, SEL))objc_msgSend)((id)objc_getClass("NSAutoreleasePool"), sel_registerName("alloc")),
        sel_registerName("init"));
    static id past;
    if (past == NULL) past = ((id(*)(id, SEL))objc_msgSend)((id)objc_getClass("NSDate"), sel_registerName("distantPast"));
    id event = ((id(*)(id, SEL, unsigned long, id, id, char))objc_msgSend)(
        app, sel_registerName("nextEventMatchingMask:untilDate:inMode:dequeue:"),
        ~0UL, past, (id)CFSTR("kCFRunLoopDefaultMode"), 1);
    int handled = 0;
    if (event != NULL) {
        ((void(*)(id, SEL, id))objc_msgSend)(app, sel_registerName("sendEvent:"), event);
        handled = 1;
    }
    ((void(*)(id, SEL))objc_msgSend)(pool, sel_registerName("drain"));
    return handled;
}

// ---- 窗口与控件创建（AppKit）----
static void init_ui(void) {
    NSApplicationLoad();
    id app = ((id(*)(id, SEL))objc_msgSend)((id)objc_getClass("NSApplication"), sel_registerName("sharedApplication"));
    ((void(*)(id, SEL, long))objc_msgSend)(app, sel_registerName("setActivationPolicy:"), 0); // Regular：前台可见
    ((void(*)(id, SEL))objc_msgSend)(app, sel_registerName("finishLaunching"));

    id cls = (id)objc_getClass("NSWindow");
    id win = ((id(*)(id, SEL))objc_msgSend)(cls, sel_registerName("alloc"));
    win = ((id(*)(id, SEL, NSRect_C, unsigned long, unsigned long, char))objc_msgSend)(
        win, sel_registerName("initWithContentRect:styleMask:backing:defer:"),
        (NSRect_C){{100, 100}, {560, 220}}, 1 | 2, 2, 0);
    CFStringRef t = CFStringCreateWithCString(NULL, "selrepro", kCFStringEncodingUTF8);
    ((void(*)(id, SEL, id))objc_msgSend)(win, sel_registerName("setTitle:"), (id)t);
    CFRelease(t);
    ((void(*)(id, SEL))objc_msgSend)(win, sel_registerName("center"));

    // 文本框（NSTextField）
    id fld = ((id(*)(id, SEL, NSRect_C))objc_msgSend)(
        ((id(*)(id, SEL))objc_msgSend)((id)objc_getClass("NSTextField"), sel_registerName("alloc")),
        sel_registerName("initWithFrame:"), (NSRect_C){{20, 150}, {520, 28}});
    CFStringRef s = CFStringCreateWithCString(NULL, "He go to school every day.", kCFStringEncodingUTF8);
    ((void(*)(id, SEL, id))objc_msgSend)(fld, sel_registerName("setStringValue:"), (id)s);
    CFRelease(s);

    // 文本视图（NSTextView，类 TextEdit）
    id view = ((id(*)(id, SEL, NSRect_C))objc_msgSend)(
        ((id(*)(id, SEL))objc_msgSend)((id)objc_getClass("NSTextView"), sel_registerName("alloc")),
        sel_registerName("initWithFrame:"), (NSRect_C){{20, 20}, {520, 110}});
    ((void(*)(id, SEL, id))objc_msgSend)(view, sel_registerName("setString:"), (id)s);

    id content = ((id(*)(id, SEL))objc_msgSend)(win, sel_registerName("contentView"));
    ((void(*)(id, SEL, id))objc_msgSend)(content, sel_registerName("addSubview:"), fld);
    ((void(*)(id, SEL, id))objc_msgSend)(content, sel_registerName("addSubview:"), view);
    ((void(*)(id, SEL, id))objc_msgSend)(win, sel_registerName("makeFirstResponder:"), fld);

    ((void(*)(id, SEL, char))objc_msgSend)(app, sel_registerName("activateIgnoringOtherApps:"), 1);
    ((void(*)(id, SEL, id))objc_msgSend)(win, sel_registerName("makeKeyAndOrderFront:"), NULL);

    g_win = win;
    g_field = fld;
    g_view = view;
    g_cur = fld;
}

// 把当前目标控件文本区域的 CG 全局坐标写回（原点主屏左上）。
static void cur_cg_bounds(double *x1, double *y1, double *x2, double *y2) {
    NSRect_C wf = msg_rect_stret(g_win, "frame");
    NSRect_C ff = msg_rect_stret(g_cur, "frame");
    id cls = (id)objc_getClass("NSScreen");
    id screen = ((id(*)(id, SEL))objc_msgSend)(cls, sel_registerName("mainScreen"));
    NSRect_C sf = msg_rect_stret(screen, "frame");
    double sh = sf.origin.y + sf.size.h;
    double wx = wf.origin.x;
    double wy = sh - (wf.origin.y + wf.size.h);
    double fx = wx + ff.origin.x;
    double fy = wy + ff.origin.y;
    *x1 = fx + ff.size.w * 0.12;
    *y1 = fy + ff.size.h / 2;
    *x2 = fx + ff.size.w * 0.62;
    *y2 = fy + ff.size.h / 2;
}

static void switch_target(int useView) {
    g_cur = useView ? g_view : g_field;
    ((void(*)(id, SEL, id))objc_msgSend)(g_win, sel_registerName("makeFirstResponder:"), g_cur);
}

// ---- 模拟鼠标点击/拖选（CG 坐标，原点主屏左上）----
static void click_at(double x, double y) {
    CGEventRef down = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseDown, CGPointMake(x, y), kCGMouseButtonLeft);
    CGEventPost(kCGHIDEventTap, down);
    CFRelease(down);
    usleep(80000);
    CGEventRef up = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseUp, CGPointMake(x, y), kCGMouseButtonLeft);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(up);
}

static void drag_from_to(double x1, double y1, double x2, double y2) {
    CGEventRef down = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseDown, CGPointMake(x1, y1), kCGMouseButtonLeft);
    CGEventPost(kCGHIDEventTap, down);
    CFRelease(down);
    usleep(100000);
    int steps = 10;
    for (int i = 1; i <= steps; i++) {
        double t = (double)i / (double)steps;
        CGEventRef mv = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseDragged,
            CGPointMake(x1 + (x2 - x1) * t, y1 + (y2 - y1) * t), kCGMouseButtonLeft);
        CGEventPost(kCGHIDEventTap, mv);
        CFRelease(mv);
        usleep(30000);
    }
    usleep(100000);
    CGEventRef up = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseUp, CGPointMake(x2, y2), kCGMouseButtonLeft);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(up);
}

// 键盘式选中：AX 设置焦点元素的选中范围（等价 Shift+方向键）。
static void ax_set_sel_focused(long loc, long len) {
    AXUIElementRef sys = AXUIElementCreateSystemWide();
    if (sys == NULL) return;
    AXUIElementRef f = NULL;
    if (AXUIElementCopyAttributeValue(sys, kAXFocusedUIElementAttribute, (CFTypeRef *)&f) == kAXErrorSuccess && f != NULL) {
        CFRange r = {loc, len};
        AXValueRef v = AXValueCreate(kAXValueTypeCFRange, &r);
        if (v != NULL) {
            AXError err = AXUIElementSetAttributeValue(f, kAXSelectedTextRangeAttribute, v);
            fprintf(stderr, "[ax-set] set(%ld,%ld) err=%d\n", loc, len, (int)err);
            CFRelease(v);
        }
        CFRelease(f);
    }
    CFRelease(sys);
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"time"

	"elfin-grammar/internal/ax"
)

func pumpAll(d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		C.pump(0.02)
		for C.pump_events() != 0 {
		}
	}
}

func dump(tag string) {
	res := ax.ReadFocused()
	fmt.Printf("[%s] app=%q text=%q selection=%q skip=%v rect=(%.0f,%.0f %.0fx%.0f) selRange(%v %d,%d)\n",
		tag, res.App, res.Text, res.Selection, res.Skip,
		res.Rect.X, res.Rect.Y, res.Rect.W, res.Rect.H,
		res.SelRange.Has, res.SelRange.Loc, res.SelRange.Len)
}

func main() {
	runtime.LockOSThread()
	C.init_ui()
	pumpAll(500 * time.Millisecond)

	fmt.Println("=== 目标：NSTextField ===")
	C.switch_target(0)
	pumpAll(200 * time.Millisecond)
	dump("field 初始")

	// 点击文本末尾取消全选（模拟用户单击定位光标）
	var x1, y1, x2, y2 C.double
	C.cur_cg_bounds(&x1, &y1, &x2, &y2)
	C.click_at(x2+150, y1)
	pumpAll(300 * time.Millisecond)
	dump("field 单击后")

	// 鼠标拖选 12% -> 62%
	fmt.Printf("拖选: (%.0f,%.0f)->(%.0f,%.0f)\n", float64(x1), float64(y1), float64(x2), float64(y2))
	C.drag_from_to(x1, y1, x2, y2)
	pumpAll(300 * time.Millisecond)
	dump("field 鼠标拖选后")

	// 键盘式选中：AX 设置 0..15
	C.ax_set_sel_focused(0, 15)
	pumpAll(200 * time.Millisecond)
	dump("field AX 选中后")

	fmt.Println("=== 目标：NSTextView ===")
	C.switch_target(1)
	pumpAll(200 * time.Millisecond)
	dump("view 初始")

	C.cur_cg_bounds(&x1, &y1, &x2, &y2)
	C.click_at(x2+150, y1)
	pumpAll(300 * time.Millisecond)
	dump("view 单击后")

	C.drag_from_to(x1, y1, x2, y2)
	pumpAll(300 * time.Millisecond)
	dump("view 鼠标拖选后")

	C.ax_set_sel_focused(0, 15)
	pumpAll(200 * time.Millisecond)
	dump("view AX 选中后")

	fmt.Println("=== 全部完成，5 秒后退出 ===")
	time.Sleep(5 * time.Second)
}
