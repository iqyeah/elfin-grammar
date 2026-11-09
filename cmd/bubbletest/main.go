//go:build darwin

// bubbletest 验证气泡窗口在 Intel Mac 上能正常显示（不依赖辅助功能权限）。
// 用法：CGO_ENABLED=1 go run ./cmd/bubbletest
package main

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation

#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>

// pump: 泵主线程 CFRunLoop（AppKit 窗口显示需要事件循环处理）。
static void pump(double seconds) {
    CFRunLoopRunInMode(kCFRunLoopDefaultMode, seconds, true);
}

// count_own_windows: 统计本进程可见窗口数（验证气泡确实创建在屏幕上）。
static int count_own_windows(void) {
    pid_t me = getpid();
    CFArrayRef list = CGWindowListCopyWindowInfo(
        kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements,
        kCGNullWindowID);
    if (list == NULL) return -1;
    int total = (int)CFArrayGetCount(list);
    int count = 0;
    for (CFIndex i = 0; i < CFArrayGetCount(list); i++) {
        CFDictionaryRef d = (CFDictionaryRef)CFArrayGetValueAtIndex(list, i);
        CFNumberRef owner = (CFNumberRef)CFDictionaryGetValue(d, kCGWindowOwnerPID);
        pid_t pid = 0;
        if (owner != NULL && CFNumberGetValue(owner, kCFNumberSInt32Type, &pid) && pid == me) {
            count++;
        }
    }
    CFRelease(list);
    return count * 1000 + total; // 高 3 位本进程窗口，低 3 位总窗口
}
*/
import "C"

import (
	"fmt"
	"runtime"

	"elfin-grammar/internal/appkit"
	"elfin-grammar/internal/ax"
	"elfin-grammar/internal/bubble"
)

func main() {
	runtime.LockOSThread()
	fmt.Println("bubble.Init...")
	bubble.Init()
	// Correct tab diff 演示：与原文不同的词应标红（go→goes）；双行 + 行内 Apply 按钮。
	orig := "He go to school every day."
	corr := "He goes to school every day."
	zh := "他每天去上学。"
	diffs := bubble.DiffSpans(orig, corr)
	fmt.Printf("diff spans (%s -> %s): %+v\n", orig, corr, diffs)
	fmt.Println("显示气泡 5 秒（屏幕左上区域）...")
	bubble.Show(corr, zh,
		ax.Rect{X: 100, Y: 300, W: 400, H: 24},
		bubble.TabOptions{Visible: bubble.TabCorrect | bubble.TabAnalyze, Active: bubble.TabCorrect,
			EnTitle: "Apply", ZhTitle: "Apply"}, false, diffs)
	for i := 0; i < 50; i++ { // 5 秒：细粒度泵（与真实应用 pumpRunLoop 一致），事件派发及时
		C.pump(0.02) // 泵事件循环
		for appkit.PumpEvents() {
		}
	}
	fmt.Printf("屏幕上本进程窗口数: %d\n", int(C.count_own_windows()))
	for i := 0; i < 200; i++ {
		C.pump(0.02)
		for appkit.PumpEvents() {
		}
	}
	bubble.Hide()
	for i := 0; i < 5; i++ {
		C.pump(0.1)
	}
	fmt.Printf("隐藏后窗口数: %d\n", int(C.count_own_windows()))
	fmt.Println("done")
}
