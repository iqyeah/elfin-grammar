//go:build darwin

package main

/*
#cgo LDFLAGS: -framework CoreFoundation

#include <CoreFoundation/CoreFoundation.h>

// pump: 让 CFRunLoop 运行一小段时间，处理窗口显示/重绘等（必须在主线程调用）。
static void pump(double seconds) {
    CFRunLoopRunInMode(kCFRunLoopDefaultMode, seconds, true);
}
*/
import "C"

import (
	"log"
	"os"
	"syscall"

	"elfin-grammar/internal/appkit"
)

// redirectLogToFile 把 stdout/stderr 重定向到日志文件：GUI 双击启动时无终端可写，
// panic 堆栈（固定写 fd 2）会丢失，需用 dup2 在 fd 层面重定向。
func redirectLogToFile(path string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	_ = syscall.Dup2(int(f.Fd()), 1)
	_ = syscall.Dup2(int(f.Fd()), 2)
	log.SetOutput(f)
}

// pumpRunLoop 泵主线程 CFRunLoop，并排空 NSApplication 事件队列。
// CFRunLoop 泵驱动窗口显示/重绘与全局快捷键，PumpEvents 派发鼠标/键盘事件
// （无 [NSApp run] 的 CLI 进程必须手动 nextEvent+sendEvent，按钮点击才生效）。
func pumpRunLoop() {
	C.pump(0.02)
	for appkit.PumpEvents() {
	}
}
