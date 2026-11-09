//go:build darwin

package main

// settingsrepro 临时复现：点击气泡 Settings 按钮后设置窗口是否弹出（用后归档，不保留）。

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation

#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <objc/runtime.h>
#include <objc/message.h>
#include <stddef.h>

typedef struct { double x, y; } NSPoint_C;
typedef struct { double w, h; } NSSize_C;
typedef struct { NSPoint_C origin; NSSize_C size; } NSRect_C;

// 主屏高度（Cocoa 坐标）
static double screen_h(void) {
    id cls = (id)objc_getClass("NSScreen");
    id screen = ((id(*)(id, SEL))objc_msgSend)(cls, sel_registerName("mainScreen"));
    NSRect_C r = {{0,0},{0,0}};
    ((void(*)(void*, id, SEL))objc_msgSend_stret)(&r, screen, sel_registerName("frame"));
    return r.origin.y + r.size.h;
}

static void pump(double s) {
    CFRunLoopRunInMode(kCFRunLoopDefaultMode, s, true);
}

// 本进程在屏可见窗口数（CGWindowList）
static int own_onscreen(void) {
    pid_t me = getpid();
    CFArrayRef list = CGWindowListCopyWindowInfo(
        kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements,
        kCGNullWindowID);
    if (list == NULL) return -1;
    int n = 0;
    for (CFIndex i = 0; i < CFArrayGetCount(list); i++) {
        CFDictionaryRef d = (CFDictionaryRef)CFArrayGetValueAtIndex(list, i);
        CFNumberRef owner = (CFNumberRef)CFDictionaryGetValue(d, kCGWindowOwnerPID);
        pid_t pid = 0;
        if (owner != NULL && CFNumberGetValue(owner, kCFNumberSInt32Type, &pid) && pid == me) n++;
    }
    CFRelease(list);
    return n;
}

// 模拟真实鼠标点击（CGEvent，屏幕坐标原点左上）
static void click_at(double x, double y) {
    CGPoint p = CGPointMake(x, y);
    CGEventRef down = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseDown, p, kCGMouseButtonLeft);
    CGEventRef up = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseUp, p, kCGMouseButtonLeft);
    CGEventPost(kCGHIDEventTap, down);
    usleep(60000);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(down);
    CFRelease(up);
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"time"

	"elfin-grammar/internal/appkit"
	"elfin-grammar/internal/ax"
	"elfin-grammar/internal/bubble"
	"elfin-grammar/internal/config"
	"elfin-grammar/internal/settings"
)

func main() {
	runtime.LockOSThread()
	appkit.InitApplication()

	cfg, err := config.Load()
	if err != nil {
		cfg = &config.Config{}
	}

	bubble.Init()
	// 气泡放在屏幕左上方区域（AX 坐标），方便点击测试
	axX, axY := 200.0, 220.0
	bubble.Show("He goes to school every day.",
		"他每天去上学。",
		ax.Rect{X: axX, Y: axY, W: 400, H: 24},
		bubble.TabOptions{Visible: bubble.TabCorrect | bubble.TabAnalyze, Active: bubble.TabCorrect,
			EnTitle: "Apply", ZhTitle: "Apply"}, false, nil)

	sh := float64(C.screen_h())
	// 复刻 bubble.go 布局计算：Settings 按钮中心（面板内坐标）
	// 面板 x = axX；面板底（Cocoa）= sh - axY + 8
	btnCX := axX + 162.0 // 12 + (54+6+48+6) + 36
	panelBottom := sh - axY + 8
	btnCY := panelBottom + 20.0 // y=8 + 24/2
	cgX := btnCX
	cgY := sh - btnCY // Cocoa -> CG（原点左上）

	fmt.Printf("screenH=%.0f 点击目标 CG=(%.0f, %.0f)\n", sh, cgX, cgY)

	pollDeadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(pollDeadline) {
		C.pump(0.02)
		for appkit.PumpEvents() {
		}
	}
	fmt.Printf("[点击前] 本进程在屏窗口数=%d settings.IsShown=%v\n", int(C.own_onscreen()), settings.IsShown())

	// 真实点击气泡 Settings 按钮
	C.click_at(C.double(cgX), C.double(cgY))
	fmt.Println("[已模拟点击 Settings 按钮]")

	end := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(end) {
		C.pump(0.02)
		for appkit.PumpEvents() {
		}
		flags := appkit.PollEvents()
		if flags&appkit.EventSettings != 0 {
			fmt.Println("[事件] EventSettings 已触发")
			if !settings.IsShown() {
				settings.Show(cfg)
			}
		}
		if flags != 0 {
			fmt.Printf("[事件] flags=%d\n", flags)
		}
		time.Sleep(30 * time.Millisecond)
	}
	fmt.Printf("[点击后] 本进程在屏窗口数=%d settings.IsShown=%v\n", int(C.own_onscreen()), settings.IsShown())

	// 清理：关闭设置窗口，隐藏气泡
	settings.Close()
	bubble.Hide()
	for i := 0; i < 5; i++ {
		C.pump(0.05)
	}
	fmt.Println("done")
}
