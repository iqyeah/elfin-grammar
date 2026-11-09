//go:build windows

package main

/*
#cgo LDFLAGS: -luser32

#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0601
#endif
#include <stdio.h>
#include <windows.h>

// appkit 包的导出符号：WM_HOTKEY 转交置位（EventHotkey）。
extern void appkit_handle_thread_message(const MSG *msg);

// redirect_stdio: 用 freopen 把 CRT 的 stdout/stderr 重绑定到日志文件。
// Go runtime 的 panic 堆栈输出走 _get_osfhandle(1/2)（CRT fd 映射），只有
// freopen 能改变该映射；GUI 程序（-H windowsgui）启动时 CRT 标准流无控制台
// 句柄，freopen 重新打开即可。返回 0 表示失败。
static int redirect_stdio(const char *path) {
    if (freopen(path, "a", stdout) == NULL) return 0;
    if (freopen(path, "a", stderr) == NULL) return 0;
    return 1;
}

// pump: 拉取并派发线程消息队列的全部消息（非阻塞）。
// WM_HOTKEY（全局快捷键）投递到线程队列，转交 appkit 置事件位；
// 其余消息 TranslateMessage+DispatchMessage（驱动低级钩子回调、
// 托盘消息与窗口过程）。返回处理的消息数。
static int pump(void) {
    MSG msg;
    int n = 0;
    while (PeekMessageW(&msg, NULL, 0, 0, PM_REMOVE)) {
        if (msg.message == WM_HOTKEY) {
            appkit_handle_thread_message(&msg);
        } else if (msg.message == WM_QUIT) {
            break;
        } else {
            TranslateMessage(&msg);
            DispatchMessageW(&msg);
        }
        n++;
    }
    return n;
}
*/
import "C"

import (
	"log"
	"os"
	"unsafe"

	"elfin-grammar/internal/appkit"
)

// redirectLogToFile Windows 版：无 dup2 可用，三条路径分别重定向——
// 1) os.Stdout/os.Stderr 变量替换：fmt.Printf/log 默认输出经变量直达文件
//    （GUI 程序无控制台，标准句柄为 NULL，输出原本被静默丢弃）；
// 2) C 层 freopen 重绑定 CRT fd 1/2：Go runtime 的 panic 堆栈走
//    _get_osfhandle(1/2)，仅 freopen 能改变该映射；
// 3) log.SetOutput 显式指向文件（冗余保险，与 darwin 版保持一致）。
func redirectLogToFile(path string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	os.Stdout = f
	os.Stderr = f
	cs := C.CString(path)
	defer C.free(unsafe.Pointer(cs))
	C.redirect_stdio(cs)
	log.SetOutput(f)
}

// pumpRunLoop 泵主线程 Windows 消息队列（对应 darwin 的 CFRunLoop 泵 +
// NSApplication 事件派发）。窗口重绘、按钮点击、托盘菜单、低级输入钩子
// 回调全部依赖 PeekMessage/DispatchMessage 驱动；PumpEvents 恒 false 空转，
// main.go 的调用方式保持不变。
func pumpRunLoop() {
	C.pump()
	for appkit.PumpEvents() {
	}
}
