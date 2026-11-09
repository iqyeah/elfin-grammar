//go:build darwin

// axdump 临时诊断：打印当前前台应用与焦点元素的 AX 角色链、文本与选中内容。
// 用法：CGO_ENABLED=1 go run ./cmd/axdump
package main

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation

#include <ApplicationServices/ApplicationServices.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

static char *ax_str(AXUIElementRef ref, CFStringRef attr) {
    CFTypeRef val = NULL;
    if (AXUIElementCopyAttributeValue(ref, attr, &val) != kAXErrorSuccess || val == NULL) return NULL;
    char *out = NULL;
    if (CFGetTypeID(val) == CFStringGetTypeID()) {
        CFStringRef s = (CFStringRef)val;
        CFIndex len = CFStringGetLength(s);
        CFIndex maxLen = CFStringGetMaximumSizeForEncoding(len, kCFStringEncodingUTF8) + 1;
        out = (char *)malloc(maxLen);
        if (out != NULL && !CFStringGetCString(s, out, maxLen, kCFStringEncodingUTF8)) { free(out); out = NULL; }
    }
    CFRelease(val);
    return out;
}

static AXUIElementRef copy_parent(AXUIElementRef ref) {
    AXUIElementRef parent = NULL;
    if (AXUIElementCopyAttributeValue(ref, kAXParentAttribute, (CFTypeRef *)&parent) != kAXErrorSuccess) return NULL;
    return parent;
}

// 打印角色链（含每个元素的选中文本/值），返回是否找到文本域
static void dump_chain(const char *tag) {
    AXUIElementRef sys = AXUIElementCreateSystemWide();
    if (sys == NULL) return;
    AXUIElementRef cur = NULL;
    AXError err = AXUIElementCopyAttributeValue(sys, kAXFocusedUIElementAttribute, (CFTypeRef *)&cur);
    int trusted = AXIsProcessTrusted();
    fprintf(stderr, "[%s] err=%d trusted=%d\n", tag, (int)err, trusted);
    if (err != kAXErrorSuccess || cur == NULL) {
        fprintf(stderr, "[%s] 无焦点元素\n", tag);
        CFRelease(sys);
        return;
    }
    CFRelease(sys);
    CFRetain(cur);
    for (int i = 0; i < 6 && cur != NULL; i++) {
        char *role = ax_str(cur, kAXRoleAttribute);
        char *sub = ax_str(cur, kAXSubroleAttribute);
        char *val = ax_str(cur, kAXValueAttribute);
        char *sel = ax_str(cur, kAXSelectedTextAttribute);
        fprintf(stderr, "[%s] L%d role=%s subrole=%s value=%s selection=%s\n",
            tag, i, role ? role : "?", sub ? sub : "-", val ? val : "-", sel ? sel : "-");
        free(role); free(sub); free(val); free(sel);
        AXUIElementRef parent = copy_parent(cur);
        CFRelease(cur);
        cur = parent;
    }
    if (cur) CFRelease(cur);
}
*/
import "C"

import (
	"fmt"
	"runtime"
)

func main() {
	runtime.LockOSThread()
	for i := 0; i < 3; i++ {
		fmt.Printf("---- 采样 %d ----\n", i+1)
		C.dump_chain(C.CString("dump"))
		// 提示用户期间切换应用/选中文本
		fmt.Println()
		C.usleep(2000000)
	}
}
