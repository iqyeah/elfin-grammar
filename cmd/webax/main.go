//go:build darwin

package main

// webax 临时诊断：验证网页内容（AXWebArea）选中文本能否被生产读取路径捕获。
// 用法：CGO_ENABLED=1 go run ./cmd/webax
// 运行后在浏览器（Safari/Chrome）中选中一段网页正文，观察输出：
// isWeb=true 且 selection 等于选中文本即表示网页选中触发链路可用。

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation

#include <ApplicationServices/ApplicationServices.h>
#include <stdio.h>
#include <string.h>

// print_chain: 从焦点元素向上打印 role/subrole 链（最多 16 层），
// 用于诊断网页焦点元素层级（AXWebArea/AXStaticText 等）。
static void print_chain(void) {
    AXUIElementRef sys = AXUIElementCreateSystemWide();
    if (sys == NULL) return;
    AXUIElementRef cur = NULL;
    if (AXUIElementCopyAttributeValue(sys, kAXFocusedUIElementAttribute,
        (CFTypeRef *)&cur) != kAXErrorSuccess || cur == NULL) {
        CFRelease(sys);
        printf("  [chain] no focused element\n");
        return;
    }
    CFRelease(sys);
    for (int i = 0; i < 16 && cur != NULL; i++) {
        CFStringRef r = NULL, s = NULL;
        AXUIElementCopyAttributeValue(cur, kAXRoleAttribute, (CFTypeRef *)&r);
        AXUIElementCopyAttributeValue(cur, kAXSubroleAttribute, (CFTypeRef *)&s);
        char rbuf[64] = "?", sbuf[64] = "";
        if (r != NULL) { CFStringGetCString(r, rbuf, sizeof(rbuf), kCFStringEncodingUTF8); CFRelease(r); }
        if (s != NULL) { CFStringGetCString(s, sbuf, sizeof(sbuf), kCFStringEncodingUTF8); CFRelease(s); }
        printf("  [chain] #%d %s (%s)\n", i, rbuf, sbuf);
        AXUIElementRef parent = NULL;
        if (AXUIElementCopyAttributeValue(cur, kAXParentAttribute, (CFTypeRef *)&parent) != kAXErrorSuccess) {
            CFRelease(cur);
            break;
        }
        CFRelease(cur);
        cur = parent;
    }
    if (cur != NULL) CFRelease(cur);
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"time"

	"elfin-grammar/internal/ax"
)

func main() {
	runtime.LockOSThread()
	fmt.Println("webax: 在浏览器中选中网页正文，观察 isWeb 与 selection 输出（30 秒后退出）。")
	fmt.Println("预期：WebArea 目标 Text 为空、isWeb=true、selection 等于选中文本。")
	deadline := time.Now().Add(30 * time.Second)
	lastLine := ""
	for time.Now().Before(deadline) {
		res := ax.ReadFocusedLight()
		line := fmt.Sprintf("[webax] skip=%v isWeb=%v role=%q text=%q selection=%q", res.Skip, res.IsWeb, res.Role, res.Text, res.Selection)
		if line != lastLine {
			fmt.Println(line)
			lastLine = line
			C.print_chain()
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Println("webax 结束。")
}
