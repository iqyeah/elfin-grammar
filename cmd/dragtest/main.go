//go:build darwin

// dragtest 临时诊断：前台显示一个含英文的文本视图，模拟鼠标拖选后保持前台，
// 供同时运行的 elfin-grammar 观察（验证选中触发链路）。
// 用法：CGO_ENABLED=1 go run ./cmd/dragtest
package main

/*
#cgo LDFLAGS: -framework AppKit -framework CoreGraphics -framework CoreFoundation

#include <objc/runtime.h>
#include <objc/message.h>
#include <Carbon/Carbon.h>
#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stddef.h>
#include <unistd.h>

extern Boolean NSApplicationLoad(void);

typedef struct { double x, y; } NSPoint_C;
typedef struct { double w, h; } NSSize_C;
typedef struct { NSPoint_C origin; NSSize_C size; } NSRect_C;

static id g_win;
static id g_view;

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

static void init_ui(void) {
    NSApplicationLoad();
    id app = ((id(*)(id, SEL))objc_msgSend)((id)objc_getClass("NSApplication"), sel_registerName("sharedApplication"));
    ((void(*)(id, SEL, long))objc_msgSend)(app, sel_registerName("setActivationPolicy:"), 0); // Regular：前台
    ((void(*)(id, SEL))objc_msgSend)(app, sel_registerName("finishLaunching"));

    id cls = (id)objc_getClass("NSWindow");
    id win = ((id(*)(id, SEL))objc_msgSend)(cls, sel_registerName("alloc"));
    win = ((id(*)(id, SEL, NSRect_C, unsigned long, unsigned long, char))objc_msgSend)(
        win, sel_registerName("initWithContentRect:styleMask:backing:defer:"),
        (NSRect_C){{100, 100}, {640, 200}}, 1 | 2, 2, 0);
    CFStringRef t = CFStringCreateWithCString(NULL, "dragtest", kCFStringEncodingUTF8);
    ((void(*)(id, SEL, id))objc_msgSend)(win, sel_registerName("setTitle:"), (id)t);
    CFRelease(t);
    ((void(*)(id, SEL))objc_msgSend)(win, sel_registerName("center"));

    id view = ((id(*)(id, SEL, NSRect_C))objc_msgSend)(
        ((id(*)(id, SEL))objc_msgSend)((id)objc_getClass("NSTextView"), sel_registerName("alloc")),
        sel_registerName("initWithFrame:"), (NSRect_C){{20, 20}, {600, 140}});
    CFStringRef s = CFStringCreateWithCString(NULL, "He go to school every day.", kCFStringEncodingUTF8);
    ((void(*)(id, SEL, id))objc_msgSend)(view, sel_registerName("setString:"), (id)s);
    CFRelease(s);
    // 加大字号，便于拖选命中
    id font = ((id(*)(id, SEL, double))objc_msgSend)((id)objc_getClass("NSFont"), sel_registerName("systemFontOfSize:"), 24.0);
    ((void(*)(id, SEL, id))objc_msgSend)(view, sel_registerName("setFont:"), font);

    id content = ((id(*)(id, SEL))objc_msgSend)(win, sel_registerName("contentView"));
    ((void(*)(id, SEL, id))objc_msgSend)(content, sel_registerName("addSubview:"), view);
    ((void(*)(id, SEL, id))objc_msgSend)(win, sel_registerName("makeFirstResponder:"), view);

    ((void(*)(id, SEL, char))objc_msgSend)(app, sel_registerName("activateIgnoringOtherApps:"), 1);
    ((void(*)(id, SEL, id))objc_msgSend)(win, sel_registerName("makeKeyAndOrderFront:"), NULL);

    g_win = win;
    g_view = view;
}

// 文本视图文本行 CG 坐标（原点主屏左上）。
// Cocoa 内容坐标原点在内容视图左下角，且窗口 frame 含标题栏：
// 视图顶部(CG) = 内容底部(CG) - (ff.origin.y + ff.size.h)。
static void view_cg_bounds(double *x1, double *y1, double *x2, double *y2) {
    NSRect_C wf = msg_rect_stret(g_win, "frame");
    NSRect_C ff = msg_rect_stret(g_view, "frame");
    id cls = (id)objc_getClass("NSScreen");
    id screen = ((id(*)(id, SEL))objc_msgSend)(cls, sel_registerName("mainScreen"));
    NSRect_C sf = msg_rect_stret(screen, "frame");
    double sh = sf.origin.y + sf.size.h;
    double wx = wf.origin.x;
    double content_bottom = sh - wf.origin.y;
    double fx = wx + ff.origin.x;
    double view_top = content_bottom - ff.origin.y - ff.size.h;
    *x1 = fx + 20;
    *y1 = view_top + 24;   // 首行文本垂直中心（24pt 字体）
    *x2 = fx + 260;
    *y2 = view_top + 24;
}

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
    usleep(120000);
    int steps = 12;
    for (int i = 1; i <= steps; i++) {
        double t = (double)i / (double)steps;
        CGEventRef mv = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseDragged,
            CGPointMake(x1 + (x2 - x1) * t, y1 + (y2 - y1) * t), kCGMouseButtonLeft);
        CGEventPost(kCGHIDEventTap, mv);
        CFRelease(mv);
        usleep(40000);
    }
    usleep(120000);
    CGEventRef up = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseUp, CGPointMake(x2, y2), kCGMouseButtonLeft);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(up);
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

func main() {
	runtime.LockOSThread()
	C.init_ui()
	pumpAll(800 * time.Millisecond)

	var x1, y1, x2, y2 C.double
	C.view_cg_bounds(&x1, &y1, &x2, &y2)
	// 拖满整句：从行首稍左到远超句末（24pt 字体整句约 290px）
	x2 = x1 + 330
	fmt.Printf("拖选 CG 区间: (%.0f, %.0f) -> (%.0f, %.0f)\n", float64(x1), float64(y1), float64(x2), float64(y2))

	// 保持无选中 5 秒：让 elfin-grammar 先观察到文本（打字触发发生一次）
	fmt.Println("保持无选中 5 秒…")
	pumpAll(5 * time.Second)
	res := ax.ReadFocused()
	fmt.Printf("[第一次选中前] selection=%q selRange(%v %d,%d)\n", res.Selection, res.SelRange.Has, res.SelRange.Loc, res.SelRange.Len)

	// 第一次鼠标拖选整句
	C.drag_from_to(x1, y1, x2, y2)
	pumpAll(500 * time.Millisecond)
	res = ax.ReadFocused()
	fmt.Printf("[第一次选中后] selection=%q selRange(%v %d,%d)\n", res.Selection, res.SelRange.Has, res.SelRange.Loc, res.SelRange.Len)
	fmt.Println("保持第一次选中 4 秒…")
	pumpAll(4 * time.Second)

	// 取消选中（单击行尾）
	C.click_at(x2-40, y1)
	pumpAll(500 * time.Millisecond)
	res = ax.ReadFocused()
	fmt.Printf("[取消选中后] selection=%q\n", res.Selection)

	// 第二次鼠标拖选同一段文本（预期修复后再次触发）
	C.drag_from_to(x1, y1, x2, y2)
	pumpAll(500 * time.Millisecond)
	res = ax.ReadFocused()
	fmt.Printf("[第二次选中后] selection=%q selRange(%v %d,%d)\n", res.Selection, res.SelRange.Has, res.SelRange.Loc, res.SelRange.Len)
	fmt.Println("保持第二次选中 6 秒…")
	pumpAll(6 * time.Second)
	fmt.Println("dragtest 结束")
}
