//go:build darwin

// Package appkit 提供 AppKit 的 CGO 桥接基础能力（NSApplication 初始化、
// 窗口/控件创建、文本读写、按钮回调、全局快捷键），供 bubble/settings 复用。
// 所有函数必须在主线程（已 LockOSThread 的 goroutine）调用。
package appkit

/*
#cgo LDFLAGS: -framework AppKit -framework QuartzCore -framework CoreImage -framework Carbon -framework ApplicationServices

#include <objc/runtime.h>
#include <objc/message.h>
#include <CoreFoundation/CoreFoundation.h>
#include <ApplicationServices/ApplicationServices.h>
#include <Carbon/Carbon.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <unistd.h>

// AppKit 的 C 入口：确保 AppKit 运行时已加载（CLI 进程必需）。
extern Boolean NSApplicationLoad(void);

// 与 NSPoint/NSSize/NSRect 布局一致的 C 结构体（均为 double）。
typedef struct { double x, y; } NSPoint_C;
typedef struct { double w, h; } NSSize_C;
typedef struct { NSPoint_C origin; NSSize_C size; } NSRect_C;

// 返回结构体（>16 字节）在 x86_64 上必须走 objc_msgSend_stret，
// 且 sret 隐藏指针作为第一个参数传入（rdi）。
static NSRect_C appkit_msg_rect(id obj, const char *selname) {
    SEL sel = sel_registerName(selname);
#if defined(__x86_64__)
    NSRect_C r = {{0, 0}, {0, 0}};
    ((void(*)(void *, id, SEL))objc_msgSend_stret)(&r, obj, sel);
    return r;
#else
    return ((NSRect_C(*)(id, SEL))objc_msgSend)(obj, sel);
#endif
}

// ---------- 按钮点击回调（动态 ObjC 类 + C IMP，回调不进 Go，避免 cgo 重入） ----------
// 事件标志位：bit0=保存点击，bit1=取消点击，bit2=快捷键按下，bit3-7=气泡按钮，
// bit8=设置窗口模型下拉框选择变化，bit9-10=状态栏菜单，bit11=设置窗口厂商下拉框选择变化，
// bit20=状态栏菜单 User Guide。
static volatile int g_event_flags;

// 保存/取消按钮与气泡按钮各自的 tag 值（创建控件时设置）。
enum {
    BTN_TAG_SAVE = 1001, BTN_TAG_CANCEL = 1002,
    BTN_TAG_APPLY = 2001, BTN_TAG_TRANSLATE = 2002, BTN_TAG_COPY = 2003,
    BTN_TAG_ANALYZE = 2004, BTN_TAG_SETTINGS = 2005,
    // 行内按钮（英文行/中文行的 Apply/Copy/Copied 动态按钮）：bit23-24 事件位。
    BTN_TAG_APPLY_EN = 2006, BTN_TAG_APPLY_ZH = 2007,
    BTN_TAG_POPUP_MODEL = 3001, BTN_TAG_POPUP_PROVIDER = 3002,
    BTN_TAG_STATUS_SETTINGS = 4001, BTN_TAG_STATUS_EXIT = 4002, BTN_TAG_STATUS_GUIDE = 4003,
    // 气泡 tab 页签（bit12-14 事件位）与右上角关闭按钮（bit15）。
    BTN_TAG_TAB_CORRECT = 5001, BTN_TAG_TAB_TRANSLATE = 5002, BTN_TAG_TAB_ANALYZE = 5003,
    BTN_TAG_CLOSE = 5004,
    // 设置窗口内置 LLM 登录区（bit16-19 事件位）：登录/退出/注册友情捐献链接/二维码切换。
    BTN_TAG_LOGIN = 6001, BTN_TAG_LOGOUT = 6002, BTN_TAG_PORTAL = 6003, BTN_TAG_QR = 6004,
    // 设置窗口页签（bit21-22 事件位）：基础配置/LLM。
    BTN_TAG_TAB_BASIC = 6005, BTN_TAG_TAB_LLM = 6006,
};

static void appkit_on_button(id self, SEL _cmd, id sender) {
    long tag = (long)((long(*)(id, SEL))objc_msgSend)(sender, sel_registerName("tag"));
    if (tag == BTN_TAG_SAVE) {
        g_event_flags |= 1;
    } else if (tag == BTN_TAG_CANCEL) {
        g_event_flags |= 2;
    } else if (tag >= BTN_TAG_APPLY && tag <= BTN_TAG_SETTINGS) {
        g_event_flags |= 1 << (tag - BTN_TAG_APPLY + 3);
    } else if (tag == BTN_TAG_APPLY_EN) {
        g_event_flags |= 1 << 23; // EventApplyEn:英文行按钮
    } else if (tag == BTN_TAG_APPLY_ZH) {
        g_event_flags |= 1 << 24; // EventApplyZh:中文行按钮
    } else if (tag == BTN_TAG_POPUP_MODEL) {
        g_event_flags |= 256;
    } else if (tag == BTN_TAG_POPUP_PROVIDER) {
        g_event_flags |= 2048;
    } else if (tag == BTN_TAG_STATUS_SETTINGS) {
        g_event_flags |= 512;
    } else if (tag == BTN_TAG_STATUS_EXIT) {
        g_event_flags |= 1024;
    } else if (tag == BTN_TAG_STATUS_GUIDE) {
        g_event_flags |= 1 << 20; // EventStatusGuide：打开在线手册
    } else if (tag >= BTN_TAG_TAB_CORRECT && tag <= BTN_TAG_CLOSE) {
        // 气泡 tab 页签与关闭按钮：bit12-15（4096/8192/16384/32768）。
        g_event_flags |= 1 << (tag - BTN_TAG_TAB_CORRECT + 12);
    } else if (tag >= BTN_TAG_LOGIN && tag <= BTN_TAG_QR) {
        // 内置 LLM 登录区：bit16-19（65536/131072/262144/524288）。
        g_event_flags |= 1 << (tag - BTN_TAG_LOGIN + 16);
    } else if (tag == BTN_TAG_TAB_BASIC) {
        g_event_flags |= 1 << 21; // EventTabBasic：切到「基础配置」页签
    } else if (tag == BTN_TAG_TAB_LLM) {
        g_event_flags |= 1 << 22; // EventTabLLM：切到「LLM」页签
    }
}

// appkit_new_target: 动态创建一个 NSObject 子类实例作为按钮 target。
static id appkit_new_target(void) {
    static Class cls;
    if (cls == NULL) {
        cls = objc_allocateClassPair((Class)objc_getClass("NSObject"), "ElfinAppkitTarget", 0);
        class_addMethod(cls, sel_registerName("onButton:"), (IMP)appkit_on_button, "v@:@");
        objc_registerClassPair(cls);
    }
    return class_createInstance(cls, 0);
}

// ---------- 事件轮询 ----------
static int appkit_poll_events(void) {
    int f = g_event_flags;
    g_event_flags = 0;
    return f;
}

// ---------- 全局快捷键（Cmd+Shift+, 打开设置） ----------
static EventHotKeyRef g_hotkey_ref;
static EventHandlerUPP g_hotkey_upp;

static OSStatus appkit_hotkey_handler(EventHandlerCallRef next, EventRef event, void *ud) {
    g_event_flags |= 4;
    return noErr;
}

// ---------- 全局输入活动监控（CGEventTap：任意按键/鼠标点击置标志） ----------
static volatile int g_input_flag;
static CFMachPortRef g_input_tap;

static CGEventRef appkit_input_callback(CGEventTapProxy proxy, CGEventType type,
                                        CGEventRef event, void *ud) {
    g_input_flag = 1;
    return event;
}

// appkit_init_input_monitor: 安装 listen-only 事件源，监听按键与鼠标点击。
// 回调只在主线程 CFRunLoop 泵中触发，仅置标志位（不重入 Go）。
static int appkit_init_input_monitor(void) {
    if (g_input_tap != NULL) return 1;
    CGEventMask mask = CGEventMaskBit(kCGEventKeyDown) |
        CGEventMaskBit(kCGEventLeftMouseDown) |
        CGEventMaskBit(kCGEventRightMouseDown) |
        CGEventMaskBit(kCGEventOtherMouseDown);
    g_input_tap = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap,
        kCGEventTapOptionListenOnly, mask, appkit_input_callback, NULL);
    if (g_input_tap == NULL) return 0;
    CFRunLoopSourceRef src = CFMachPortCreateRunLoopSource(NULL, g_input_tap, 0);
    if (src == NULL) return 0;
    CFRunLoopAddSource(CFRunLoopGetMain(), src, kCFRunLoopCommonModes);
    return 1;
}

// appkit_input_activity: 返回并清除输入活动标志（bit 只表示是否有按键/点击）。
static int appkit_input_activity(void) {
    int f = g_input_flag;
    g_input_flag = 0;
    return f;
}

static int appkit_register_hotkey(void) {
    if (g_hotkey_ref != NULL) return 1;
    EventTypeSpec spec = { kEventClassKeyboard, kEventHotKeyPressed };
    g_hotkey_upp = NewEventHandlerUPP(appkit_hotkey_handler);
    if (InstallEventHandler(GetEventDispatcherTarget(), g_hotkey_upp, 1, &spec, NULL, NULL) != noErr) {
        return 0;
    }
    EventHotKeyID hid = { 'EFNG', 1 };
    if (RegisterEventHotKey(kVK_ANSI_Comma, cmdKey | shiftKey, hid,
        GetEventDispatcherTarget(), 0, &g_hotkey_ref) != noErr) {
        return 0;
    }
    return 1;
}

// ---------- 基础对象操作 ----------
static id appkit_class(const char *name) { return (id)objc_getClass(name); }

static id appkit_msg_id0(id obj, const char *selname) {
    SEL sel = sel_registerName(selname);
    return ((id(*)(id, SEL))objc_msgSend)(obj, sel);
}

static id appkit_msg_id1(id obj, const char *selname, id a1) {
    SEL sel = sel_registerName(selname);
    return ((id(*)(id, SEL, id))objc_msgSend)(obj, sel, a1);
}

static void appkit_msg_void0(id obj, const char *selname) {
    SEL sel = sel_registerName(selname);
    ((void(*)(id, SEL))objc_msgSend)(obj, sel);
}

static void appkit_msg_void1(id obj, const char *selname, id a1) {
    SEL sel = sel_registerName(selname);
    ((void(*)(id, SEL, id))objc_msgSend)(obj, sel, a1);
}

static void appkit_msg_void_long(id obj, const char *selname, long v) {
    SEL sel = sel_registerName(selname);
    ((void(*)(id, SEL, long))objc_msgSend)(obj, sel, v);
}

static void appkit_msg_void_double(id obj, const char *selname, double v) {
    SEL sel = sel_registerName(selname);
    ((void(*)(id, SEL, double))objc_msgSend)(obj, sel, v);
}

static void appkit_msg_void_bool(id obj, const char *selname, char b) {
    SEL sel = sel_registerName(selname);
    ((void(*)(id, SEL, char))objc_msgSend)(obj, sel, b);
}

static void appkit_msg_void_rect_bool(id obj, const char *selname, NSRect_C r, char b) {
    SEL sel = sel_registerName(selname);
    ((void(*)(id, SEL, NSRect_C, char))objc_msgSend)(obj, sel, r, b);
}

// 字符串互转：NSString(id) <-> malloc 的 UTF-8 C 字符串
static char *appkit_nsstring_to_c(id ns) {
    if (ns == NULL) return NULL;
    CFStringRef s = (CFStringRef)ns;
    CFIndex len = CFStringGetLength(s);
    CFIndex maxLen = CFStringGetMaximumSizeForEncoding(len, kCFStringEncodingUTF8) + 1;
    char *out = (char *)malloc(maxLen);
    if (out != NULL && !CFStringGetCString(s, out, maxLen, kCFStringEncodingUTF8)) {
        free(out);
        out = NULL;
    }
    return out;
}

static id appkit_c_to_nsstring(const char *s) {
    return (id)CFStringCreateWithCString(NULL, s, kCFStringEncodingUTF8);
}

// ---------- 事件泵 ----------
// appkit_pump_events: 从 NSApplication 事件队列拉取并派发一个事件（非阻塞）。
// CLI 进程（无 [NSApp run]）中 CFRunLoop 泵不会派发鼠标/键盘事件，
// 必须由主循环定期调用 nextEvent+sendEvent，按钮点击等交互才生效。
// 返回 1 表示处理了一个事件，0 表示队列为空。
static int appkit_pump_events(void) {
    id app = appkit_msg_id0(appkit_class("NSApplication"), "sharedApplication");
    if (app == NULL) return 0;
    id pool = appkit_msg_id0(appkit_msg_id0(appkit_class("NSAutoreleasePool"), "alloc"), "init");
    static id past = NULL;
    if (past == NULL) {
        past = appkit_msg_id0(appkit_class("NSDate"), "distantPast");
    }
    // nextEventMatchingMask:untilDate:inMode:dequeue:
    //  mask = NSEventMaskAny（NSUIntegerMax），untilDate 为过去时刻 → 非阻塞。
    id event = ((id(*)(id, SEL, unsigned long, id, id, char))objc_msgSend)(
        app, sel_registerName("nextEventMatchingMask:untilDate:inMode:dequeue:"),
        ~0UL, past, (id)CFSTR("kCFRunLoopDefaultMode"), 1);
    int handled = 0;
    if (event != NULL) {
        appkit_msg_void1(app, "sendEvent:", event);
        handled = 1;
    }
    appkit_msg_void0(pool, "drain");
    return handled;
}


// ---------- NSStatusItem 状态栏 ----------
static id g_status_item;

// appkit_status_icon_from_png: 把内嵌 PNG 字节流解码为 NSImage 并置为模板图标
// （packaging/gen_icons.py 生成的对勾气泡剪影，36x36 @2x），
// 菜单栏随深浅外观自动适配前景色，无需外部资源文件。
static id appkit_status_icon_from_png(const unsigned char *data, int len) {
    if (data == NULL || len <= 0) return NULL;
    id nsdata = ((id(*)(id, SEL, const void *, unsigned long))objc_msgSend)(
        (id)appkit_class("NSData"), sel_registerName("dataWithBytes:length:"),
        data, (unsigned long)len);
    if (nsdata == NULL) return NULL;
    id img = appkit_msg_id0(appkit_class("NSImage"), "alloc");
    img = ((id(*)(id, SEL, id))objc_msgSend)(img, sel_registerName("initWithData:"), nsdata);
    if (img == NULL) return NULL;
    appkit_msg_void_bool(img, "setTemplate:", 1);
    // PNG 为 36x36 @2x 像素流；逻辑尺寸设 18x18 保证 Retina 清晰。
    ((void(*)(id, SEL, NSSize_C))objc_msgSend)(img, sel_registerName("setSize:"), (NSSize_C){18, 18});
    return img;
}

// appkit_status_init: 创建状态栏图标与下拉菜单（Settings / User Guide / Exit）。
// 菜单项复用 onButton: 回调（target 为动态类实例），仅置事件标志位，不重入 Go。
static int appkit_status_init(const unsigned char *png, int png_len) {
    if (g_status_item != NULL) return 1;
    id bar = appkit_msg_id0(appkit_class("NSStatusBar"), "systemStatusBar");
    if (bar == NULL) return 0;
    // NSSquareStatusItemLength = -2：正方形长度（高等于状态栏高度）。
    id item = ((id(*)(id, SEL, double))objc_msgSend)(bar, sel_registerName("statusItemWithLength:"), -2.0);
    if (item == NULL) return 0;
    id btn = appkit_msg_id0(item, "button");
    id icon = appkit_status_icon_from_png(png, png_len);
    if (btn == NULL || icon == NULL) return 0;
    appkit_msg_void1(btn, "setImage:", icon);
    id tip = appkit_c_to_nsstring("elfin-grammar");
    appkit_msg_void1(btn, "setToolTip:", tip);
    CFRelease((CFTypeRef)tip);

    id menu = appkit_msg_id0(appkit_class("NSMenu"), "alloc");
    menu = appkit_msg_id0(menu, "init");
    if (menu == NULL) return 0;
    id tgt = appkit_new_target();
    SEL addSel = sel_registerName("addItemWithTitle:action:keyEquivalent:");
    SEL actionSel = sel_registerName("onButton:");
    id eq = appkit_c_to_nsstring("");
    id t = appkit_c_to_nsstring("Settings");
    id mi = ((id(*)(id, SEL, id, SEL, id))objc_msgSend)(menu, addSel, t, actionSel, eq);
    CFRelease((CFTypeRef)t);
    appkit_msg_void_long(mi, "setTag:", BTN_TAG_STATUS_SETTINGS);
    appkit_msg_void1(mi, "setTarget:", tgt);

    t = appkit_c_to_nsstring("User Guide");
    mi = ((id(*)(id, SEL, id, SEL, id))objc_msgSend)(menu, addSel, t, actionSel, eq);
    CFRelease((CFTypeRef)t);
    appkit_msg_void_long(mi, "setTag:", BTN_TAG_STATUS_GUIDE);
    appkit_msg_void1(mi, "setTarget:", tgt);

    id sep = appkit_msg_id0(appkit_class("NSMenuItem"), "separatorItem");
    appkit_msg_void1(menu, "addItem:", sep);

    t = appkit_c_to_nsstring("Exit");
    mi = ((id(*)(id, SEL, id, SEL, id))objc_msgSend)(menu, addSel, t, actionSel, eq);
    CFRelease((CFTypeRef)t);
    appkit_msg_void_long(mi, "setTag:", BTN_TAG_STATUS_EXIT);
    appkit_msg_void1(mi, "setTarget:", tgt);
    CFRelease((CFTypeRef)eq);

    appkit_msg_void1(item, "setMenu:", menu);
    g_status_item = item;
    CFRetain((CFTypeRef)item); // 进程级持有，与窗口句柄同一策略
    return 1;
}


// ---------- 调试：窗口真实屏幕状态（临时诊断用） ----------
static int appkit_window_on_active_space(id win) {
    char b = ((char(*)(id, SEL))objc_msgSend)(win, sel_registerName("isOnActiveSpace"));
    return b ? 1 : 0;
}

static unsigned long appkit_window_occlusion_state(id win) {
    return ((unsigned long(*)(id, SEL))objc_msgSend)(win, sel_registerName("occlusionState"));
}

static void appkit_window_frame(id win, double *x, double *y, double *w, double *h) {
    NSRect_C r = appkit_msg_rect(win, "frame");
    *x = r.origin.x; *y = r.origin.y; *w = r.size.w; *h = r.size.h;
}

static long appkit_window_level(id win) {
    return ((long(*)(id, SEL))objc_msgSend)(win, sel_registerName("level"));
}

static int appkit_app_active(void) {
    id app = appkit_msg_id0(appkit_class("NSApplication"), "sharedApplication");
    if (app == NULL) return 0;
    char b = ((char(*)(id, SEL))objc_msgSend)(app, sel_registerName("isActive"));
    return b ? 1 : 0;
}

// appkit_own_onscreen: CGWindowList 真实在屏窗口数（不受 AppKit 状态影响）。
static int appkit_own_onscreen(void) {
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

// appkit_dump_zorder: 打印本窗口在全局在屏窗口列表中的 z 序位置与遮挡情况（临时诊断）。
static void appkit_dump_zorder(id win) {
    long num = ((long(*)(id, SEL))objc_msgSend)(win, sel_registerName("windowNumber"));
    NSRect_C f = appkit_msg_rect(win, "frame");
    // 主屏高度（Cocoa 坐标），用于把窗口 frame 转到 CG 坐标（原点左上）。
    id screen = appkit_msg_id0(appkit_class("NSScreen"), "mainScreen");
    NSRect_C sf = appkit_msg_rect(screen, "frame");
    double sh = sf.origin.y + sf.size.h;
    double wx = f.origin.x, wy = sh - (f.origin.y + f.size.h);
    double ww = f.size.w, wh = f.size.h;

    CFArrayRef list = CGWindowListCopyWindowInfo(
        kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements,
        kCGNullWindowID);
    if (list == NULL) { fprintf(stderr, "[dbg-zorder] list=NULL\n"); return; }
    CFIndex total = CFArrayGetCount(list);
    CFIndex idx = -1;
    for (CFIndex i = 0; i < total; i++) {
        CFDictionaryRef d = (CFDictionaryRef)CFArrayGetValueAtIndex(list, i);
        CFNumberRef n = (CFNumberRef)CFDictionaryGetValue(d, kCGWindowNumber);
        int wn = 0;
        if (n != NULL && CFNumberGetValue(n, kCFNumberSInt32Type, &wn) && (long)wn == num) { idx = i; break; }
    }
    fprintf(stderr, "[dbg-zorder] windowNumber=%ld idx=%ld total=%ld frame_cg=(%.0f,%.0f,%.0f,%.0f)\n",
        num, (long)idx, (long)total, wx, wy, ww, wh);
    if (idx < 0) { CFRelease(list); return; }
    int cover = 0;
    for (CFIndex i = 0; i < idx; i++) { // z 序在本窗口之前的窗口
        CFDictionaryRef d = (CFDictionaryRef)CFArrayGetValueAtIndex(list, i);
        CFDictionaryRef b = (CFDictionaryRef)CFDictionaryGetValue(d, kCGWindowBounds);
        double bx = 0, by = 0, bw = 0, bh = 0;
        CFNumberRef nb;
        if (b != NULL &&
            (nb = (CFNumberRef)CFDictionaryGetValue(b, CFSTR("X"))) != NULL && CFNumberGetValue(nb, kCFNumberDoubleType, &bx) &&
            (nb = (CFNumberRef)CFDictionaryGetValue(b, CFSTR("Y"))) != NULL && CFNumberGetValue(nb, kCFNumberDoubleType, &by) &&
            (nb = (CFNumberRef)CFDictionaryGetValue(b, CFSTR("Width"))) != NULL && CFNumberGetValue(nb, kCFNumberDoubleType, &bw) &&
            (nb = (CFNumberRef)CFDictionaryGetValue(b, CFSTR("Height"))) != NULL && CFNumberGetValue(nb, kCFNumberDoubleType, &bh)) {
            if (bx < wx + ww && bx + bw > wx && by < wy + wh && by + bh > wy) {
                if (cover < 3) {
                    CFStringRef name = (CFStringRef)CFDictionaryGetValue(d, kCGWindowOwnerName);
                    char *s = appkit_nsstring_to_c((id)name);
                    fprintf(stderr, "[dbg-zorder] 覆盖者: owner=%s bounds=(%.0f,%.0f,%.0f,%.0f)\n",
                        s ? s : "?", bx, by, bw, bh);
                    free(s);
                }
                cover++;
            }
        }
    }
    fprintf(stderr, "[dbg-zorder] 上方相交窗口数=%d\n", cover);
    CFRelease(list);
}

// appkit_dump_frontmost: 打印当前前台应用名（临时诊断）。
static void appkit_dump_frontmost(void) {
    id ws = appkit_msg_id0(appkit_class("NSWorkspace"), "sharedWorkspace");
    id app = appkit_msg_id0(ws, "frontmostApplication");
    if (app == NULL) { fprintf(stderr, "[dbg-frontmost] (null)\n"); return; }
    id name = appkit_msg_id0(app, "localizedName");
    char *s = appkit_nsstring_to_c(name);
    fprintf(stderr, "[dbg-frontmost] %s\n", s ? s : "(null)");
    free(s);
}

// ---------- 打开 URL 与二维码（内置 LLM 登录区） ----------
// appkit_open_url: 用 NSWorkspace 在默认浏览器打开 URL（注册/友情捐献链接）。
static void appkit_open_url(const char *url) {
    if (url == NULL || url[0] == '\0') return;
    id ws = appkit_msg_id0(appkit_class("NSWorkspace"), "sharedWorkspace");
    if (ws == NULL) return;
    id s = appkit_c_to_nsstring(url);
    id u = ((id(*)(id, SEL, id))objc_msgSend)(
        appkit_class("NSURL"), sel_registerName("URLWithString:"), s);
    CFRelease((CFTypeRef)s);
    if (u == NULL) return;
    appkit_msg_void1(ws, "openURL:", u);
}

// appkit_qr_image: 用 CoreImage CIQRCodeGenerator 把文本生成二维码 NSImage
// （无第三方依赖）。返回新 NSImage（调用者持有），失败返回 NULL。
static id appkit_qr_image(const char *text) {
    if (text == NULL || text[0] == '\0') return NULL;
    id str = appkit_c_to_nsstring(text);
    // NSUTF8StringEncoding = 4
    id data = ((id(*)(id, SEL, unsigned long))objc_msgSend)(
        str, sel_registerName("dataUsingEncoding:"), 4);
    CFRelease((CFTypeRef)str);
    if (data == NULL) return NULL;
    id filter = ((id(*)(id, SEL, id))objc_msgSend)(
        appkit_class("CIFilter"), sel_registerName("filterWithName:"),
        (id)CFSTR("CIQRCodeGenerator"));
    if (filter == NULL) return NULL;
    ((void(*)(id, SEL, id, id))objc_msgSend)(filter, sel_registerName("setValue:forKey:"),
        data, (id)CFSTR("inputMessage"));
    id ciimg = appkit_msg_id0(filter, "outputImage");
    if (ciimg == NULL) return NULL;
    // CIImage 是无限大矢量图像，NSImage 显示需要 bitmap：经 CIContext 渲染为 CGImage。
    id ctx = ((id(*)(id, SEL, id))objc_msgSend)(
        appkit_class("CIContext"), sel_registerName("contextWithOptions:"), NULL);
    if (ctx == NULL) return NULL;
    NSRect_C ext = appkit_msg_rect(ciimg, "extent");
    CGImageRef cg = ((CGImageRef(*)(id, SEL, id, NSRect_C))objc_msgSend)(
        ctx, sel_registerName("createCGImage:fromRect:"), ciimg, ext);
    if (cg == NULL) return NULL;
    id img = appkit_msg_id0(appkit_class("NSImage"), "alloc");
    img = ((id(*)(id, SEL, CGImageRef, NSSize_C))objc_msgSend)(
        img, sel_registerName("initWithCGImage:size:"), cg, ext.size);
    CGImageRelease(cg);
    return img;
}

// appkit_create_image_view: 创建 NSImageView 显示二维码（等比缩放）。
// image 传入后由视图 retain，本函数 release 调用方持有（与控件创建同一策略）。
static id appkit_create_image_view(double x, double y, double w, double h, id image) {
    id v = ((id(*)(id, SEL, NSRect_C))objc_msgSend)(
        appkit_msg_id0(appkit_class("NSImageView"), "alloc"),
        sel_registerName("initWithFrame:"),
        (NSRect_C){{x, y}, {w, h}});
    if (image != NULL) {
        appkit_msg_void1(v, "setImage:", image);
        CFRelease((CFTypeRef)image); // setImage: 已 retain
    }
    // NSImageScaleProportionallyUpOrDown = 3：等比缩放适配视图尺寸。
    appkit_msg_void_long(v, "setImageScaling:", 3);
    CFRetain((CFTypeRef)v);
    return v;
}

// appkit_image_view_set_text: 重新生成二维码并替换视图图像（链接/二维码切换）。
static void appkit_image_view_set_text(id v, const char *text) {
    id img = appkit_qr_image(text);
    if (img == NULL) return;
    appkit_msg_void1(v, "setImage:", img);
    CFRelease((CFTypeRef)img); // setImage: 已 retain
}

// ---------- NSApplication ----------
static void appkit_init_application(void) {
    static int done;
    if (done) return;
    done = 1;
    NSApplicationLoad();
    id app = appkit_msg_id0(appkit_class("NSApplication"), "sharedApplication");
    // NSApplicationActivationPolicyAccessory：Dock 不显示图标，但可显示窗口
    appkit_msg_void_long(app, "setActivationPolicy:", 1);
    // 安装事件源/完成启动准备（未调用 [NSApp run] 时窗口事件仍由 PumpEvents 派发）
    appkit_msg_void0(app, "finishLaunching");
}

// ---------- 模态确认对话框（首次启动免责声明） ----------
// appkit_confirm_dialog: NSAlert runModal 阻塞式确认框（自建模态事件循环，
// 主线程阻塞直到用户点击，无需主循环 PumpEvents 配合）。
// 返回 1=第一按钮（accept），0=第二按钮（decline）。
static int appkit_confirm_dialog(const char *title, const char *message,
                                 const char *accept, const char *decline) {
    // Accessory 应用不激活时弹窗会排在前台应用之后，先激活再弹。
    id app = appkit_msg_id0(appkit_class("NSApplication"), "sharedApplication");
    if (app != NULL) {
        appkit_msg_void_bool(app, "activateIgnoringOtherApps:", 1);
    }
    id alert = appkit_msg_id0(appkit_class("NSAlert"), "alloc");
    alert = appkit_msg_id0(alert, "init");
    if (alert == NULL) return 0;
    id s = appkit_c_to_nsstring(title);
    appkit_msg_void1(alert, "setMessageText:", s);
    CFRelease((CFTypeRef)s);
    s = appkit_c_to_nsstring(message);
    appkit_msg_void1(alert, "setInformativeText:", s);
    CFRelease((CFTypeRef)s);
    // NSAlertStyleWarning = 1：警告样式图标。
    appkit_msg_void_long(alert, "setAlertStyle:", 1);
    s = appkit_c_to_nsstring(accept);
    id bAccept = ((id(*)(id, SEL, id))objc_msgSend)(alert, sel_registerName("addButtonWithTitle:"), s);
    CFRelease((CFTypeRef)s);
    s = appkit_c_to_nsstring(decline);
    id bDecline = ((id(*)(id, SEL, id))objc_msgSend)(alert, sel_registerName("addButtonWithTitle:"), s);
    CFRelease((CFTypeRef)s);
    // 默认焦点放在「退出」（回车不误同意）：清空第一按钮的回车键位，第二按钮设回车。
    id eq = appkit_c_to_nsstring("");
    appkit_msg_void1(bAccept, "setKeyEquivalent:", eq);
    CFRelease((CFTypeRef)eq);
    eq = appkit_c_to_nsstring("\r");
    appkit_msg_void1(bDecline, "setKeyEquivalent:", eq);
    CFRelease((CFTypeRef)eq);
    // NSAlertFirstButtonReturn = 1000（第一按钮为 accept）。
    long resp = ((long(*)(id, SEL))objc_msgSend)(alert, sel_registerName("runModal"));
    return resp == 1000 ? 1 : 0;
}

// ---------- NSWindow ----------
// 矩形包含点判定（均为全局 Cocoa 坐标）。
static int appkit_rect_contains_point(NSRect_C r, NSPoint_C p) {
    return p.x >= r.origin.x && p.x < r.origin.x + r.size.w &&
        p.y >= r.origin.y && p.y < r.origin.y + r.size.h;
}

// appkit_center_on_mouse_screen: 将窗口居中到鼠标所在屏幕的可见区域。
// 鼠标不在任何屏幕时退回主屏。解决多显示器下设置窗口弹到主屏（用户看不到）的问题。
static void appkit_center_on_mouse_screen(id win) {
    // +[NSEvent mouseLocation] 返回全局 Cocoa 坐标的 NSPoint（16 字节，直接 objc_msgSend）。
    NSPoint_C mp = ((NSPoint_C(*)(id, SEL))objc_msgSend)(
        appkit_class("NSEvent"), sel_registerName("mouseLocation"));
    id screens = appkit_msg_id0(appkit_class("NSScreen"), "screens");
    id target = NULL;
    if (screens != NULL) {
        unsigned long cnt = ((unsigned long(*)(id, SEL))objc_msgSend)(screens, sel_registerName("count"));
        for (unsigned long i = 0; i < cnt; i++) {
            id sc = ((id(*)(id, SEL, unsigned long))objc_msgSend)(screens, sel_registerName("objectAtIndex:"), i);
            if (appkit_rect_contains_point(appkit_msg_rect(sc, "frame"), mp)) { target = sc; break; }
        }
    }
    if (target == NULL) target = appkit_msg_id0(appkit_class("NSScreen"), "mainScreen");
    NSRect_C vf = appkit_msg_rect(target, "visibleFrame");
    NSRect_C f = appkit_msg_rect(win, "frame");
    NSPoint_C origin = {
        vf.origin.x + (vf.size.w - f.size.w) / 2.0,
        vf.origin.y + (vf.size.h - f.size.h) / 2.0
    };
    ((void(*)(id, SEL, NSPoint_C))objc_msgSend)(win, sel_registerName("setFrameOrigin:"), origin);
}

// 创建标准窗口（标题栏+关闭按钮）。返回窗口对象（CFRetain 持有，进程级单例）。
static id appkit_create_window(double w, double h, const char *title) {
    id cls = appkit_class("NSWindow");
    id win = appkit_msg_id0(cls, "alloc");
    SEL initSel = sel_registerName("initWithContentRect:styleMask:backing:defer:");
    // Titled(1) | Closable(2) | Miniaturizable(4)
    unsigned long mask = 1 | 2 | 4;
    win = ((id(*)(id, SEL, NSRect_C, unsigned long, unsigned long, char))objc_msgSend)(
        win, initSel, (NSRect_C){{0, 0}, {w, h}}, mask, 2, 0);
    id t = appkit_c_to_nsstring(title);
    appkit_msg_void1(win, "setTitle:", t);
    CFRelease((CFTypeRef)t);
    appkit_center_on_mouse_screen(win);
    appkit_msg_void_bool(win, "setReleasedWhenClosed:", 0);
    CFRetain((CFTypeRef)win); // 独立于自动释放持有
    return win;
}

// appkit_window_show: 激活本应用后显示窗口。Accessory 应用不激活时直接
// makeKeyAndOrderFront，窗口会被排在前台应用窗口之后（z 序靠后，看似未弹出），
// 先激活才能让窗口盖过前台应用（无 Dock 图标/菜单栏）。
// 气泡是 NonactivatingPanel，点其 Settings 按钮不会激活本应用，且
// activateIgnoringOtherApps: 在 macOS 14+ 已废弃，故双保险：同时走
// NSRunningApplication activateWithOptions:（AllWindows|IgnoringOtherApps）。
static void appkit_window_show(id win) {
    id app = appkit_msg_id0(appkit_class("NSApplication"), "sharedApplication");
    if (app != NULL) {
        ((void(*)(id, SEL, char))objc_msgSend)(app, sel_registerName("activateIgnoringOtherApps:"), 1);
        // NSApplicationActivateAllWindows(1) | NSApplicationActivateIgnoringOtherApps(2) = 3
        id ra = ((id(*)(id, SEL, int))objc_msgSend)(
            appkit_class("NSRunningApplication"),
            sel_registerName("runningApplicationWithProcessIdentifier:"),
            (int)getpid());
        if (ra != NULL) {
            appkit_msg_void_long(ra, "activateWithOptions:", 3);
        }
    }
    appkit_msg_void0(win, "makeKeyAndOrderFront:");
}

// appkit_app_deactivate: 归还激活状态（设置窗口关闭后调用，把输入焦点还给用户之前的应用）。
static void appkit_app_deactivate(void) {
    id app = appkit_msg_id0(appkit_class("NSApplication"), "sharedApplication");
    if (app != NULL) {
        appkit_msg_void0(app, "deactivate");
    }
}

static void appkit_window_close(id win) {
    appkit_msg_void0(win, "close");
}

static void appkit_window_orderout(id win) {
    appkit_msg_void1(win, "orderOut:", NULL);
}

static int appkit_window_visible(id win) {
    char b = ((char(*)(id, SEL))objc_msgSend)(win, sel_registerName("isVisible"));
    return b ? 1 : 0;
}

static void appkit_window_release(id win) {
    if (win) CFRelease((CFTypeRef)win);
}

// ---------- NSTextField ----------
// 创建可编辑文本框。isSecure 时用 NSSecureTextField 子类（密码式输入）。
static id appkit_create_textfield(double x, double y, double w, double h, const char *text, int isSecure) {
    id cls = appkit_class(isSecure ? "NSSecureTextField" : "NSTextField");
    id f = ((id(*)(id, SEL, NSRect_C))objc_msgSend)(
        appkit_msg_id0(cls, "alloc"), sel_registerName("initWithFrame:"),
        (NSRect_C){{x, y}, {w, h}});
    id s = appkit_c_to_nsstring(text ? text : "");
    appkit_msg_void1(f, "setStringValue:", s);
    CFRelease((CFTypeRef)s);
    appkit_msg_void_bool(f, "setEditable:", 1);
    appkit_msg_void_bool(f, "setBezeled:", 1);
    appkit_msg_void_bool(f, "setDrawsBackground:", 1);
    CFRetain((CFTypeRef)f);
    return f;
}

static char *appkit_textfield_get(id f) {
    id s = appkit_msg_id0(f, "stringValue");
    return appkit_nsstring_to_c(s);
}

static void appkit_textfield_set(id f, const char *text) {
    id s = appkit_c_to_nsstring(text ? text : "");
    appkit_msg_void1(f, "setStringValue:", s);
    CFRelease((CFTypeRef)s);
}

static void appkit_textfield_set_editable(id f, int editable) {
    appkit_msg_void_bool(f, "setEditable:", editable);
}

// ---------- NSTextField 标签（不可编辑） ----------
static id appkit_create_label(double x, double y, double w, double h, const char *text) {
    id cls = appkit_class("NSTextField");
    id l = ((id(*)(id, SEL, NSRect_C))objc_msgSend)(
        appkit_msg_id0(cls, "alloc"), sel_registerName("initWithFrame:"),
        (NSRect_C){{x, y}, {w, h}});
    id s = appkit_c_to_nsstring(text);
    appkit_msg_void1(l, "setStringValue:", s);
    CFRelease((CFTypeRef)s);
    appkit_msg_void_bool(l, "setEditable:", 0);
    appkit_msg_void_bool(l, "setBezeled:", 0);
    appkit_msg_void_bool(l, "setDrawsBackground:", 0);
    // 右对齐
    appkit_msg_void_long(l, "setAlignment:", 2);
    CFRetain((CFTypeRef)l);
    return l;
}

// ---------- NSComboBox 可编辑下拉框 ----------
// 创建可编辑下拉框：可从预设项中选择，也可直接输入自定义值。
// 用 NSComboBox（NSTextField 子类，可编辑、带下拉列表），
// 而非 NSPopUpButton —— 后者不可编辑、没有 setEditable:，发送该选择子会抛
// NSInvalidArgumentException（unrecognized selector）导致进程崩溃。
// tag 区分事件来源：BTN_TAG_POPUP_PROVIDER（厂商）或 BTN_TAG_POPUP_MODEL（模型）。
static id appkit_create_popup(double x, double y, double w, double h, long tag) {
    id p = ((id(*)(id, SEL, NSRect_C))objc_msgSend)(
        appkit_msg_id0(appkit_class("NSComboBox"), "alloc"),
        sel_registerName("initWithFrame:"),
        (NSRect_C){{x, y}, {w, h}});
    appkit_msg_void_long(p, "setTag:", tag);
    appkit_msg_void1(p, "setTarget:", appkit_new_target());
    appkit_msg_void1(p, "setAction:", (id)sel_registerName("onButton:"));
    // 可编辑：选中预设项之外允许直接键入自定义模型名（厂商下拉框随后关闭手输）。
    appkit_msg_void_bool(p, "setEditable:", 1);
    // 关闭自动补全与数据源模式，改用静态 addItemWithObjectValue: 追加预设项。
    appkit_msg_void_bool(p, "setCompletes:", 0);
    appkit_msg_void_bool(p, "setUsesDataSource:", 0);
    CFRetain((CFTypeRef)p);
    return p;
}

static void appkit_popup_add_item(id p, const char *title) {
    id s = appkit_c_to_nsstring(title);
    appkit_msg_void1(p, "addItemWithObjectValue:", s);
    CFRelease((CFTypeRef)s);
}

static void appkit_popup_remove_all_items(id p) {
    appkit_msg_void0(p, "removeAllItems");
}

static void appkit_popup_set_editable(id p, int editable) {
    appkit_msg_void_bool(p, "setEditable:", editable);
}

static long appkit_popup_index(id p) {
    return ((long(*)(id, SEL))objc_msgSend)(p, sel_registerName("indexOfSelectedItem"));
}

static void appkit_popup_select_index(id p, long idx) {
    // NSComboBox 无"清除选择"公开接口，下标 -1 会越界；无选中项由调用方随后
    // SetString 回显自定义文本实现（此时 indexOfSelectedItem 自然返回 -1）。
    if (idx < 0) return;
    appkit_msg_void_long(p, "selectItemAtIndex:", idx);
}

// ---------- NSButton ----------
static id appkit_create_button(double x, double y, double w, double h,
    const char *title, long tag, int isDefault) {
    id cls = appkit_class("NSButton");
    id b = ((id(*)(id, SEL, NSRect_C))objc_msgSend)(
        appkit_msg_id0(cls, "alloc"), sel_registerName("initWithFrame:"),
        (NSRect_C){{x, y}, {w, h}});
    id t = appkit_c_to_nsstring(title);
    appkit_msg_void1(b, "setTitle:", t);
    CFRelease((CFTypeRef)t);
    // NSBezelStyleRounded = 2
    appkit_msg_void_long(b, "setBezelStyle:", 2);
    appkit_msg_void_long(b, "setTag:", tag);
    appkit_msg_void1(b, "setTarget:", appkit_new_target());
    appkit_msg_void1(b, "setAction:", (id)sel_registerName("onButton:"));
    if (isDefault) {
        id eq = appkit_c_to_nsstring("\r");
        appkit_msg_void1(b, "setKeyEquivalent:", eq);
        CFRelease((CFTypeRef)eq);
    }
    CFRetain((CFTypeRef)b);
    return b;
}

// ---------- 添加子视图 ----------
// container 可能是 NSWindow（应加入其 contentView）或 NSView（直接作为父视图），
// 用 respondsToSelector: 区分，兼容 NewWindow 与 NewHandle（视图）两种句柄。
static void appkit_add_subview(id container, id view) {
    id parent = container;
    SEL cvSel = sel_registerName("contentView");
    char isWindow = ((char(*)(id, SEL, SEL))objc_msgSend)(
        container, sel_registerName("respondsToSelector:"), cvSel);
    if (isWindow) {
        parent = appkit_msg_id0(container, "contentView");
    }
    appkit_msg_void1(parent, "addSubview:", view);
}

static void appkit_make_first_responder(id win, id field) {
    appkit_msg_void1(win, "makeFirstResponder:", field);
}

// ---------- 控件 frame ----------
static void appkit_button_set_frame(id b, double x, double y, double w, double h) {
    ((void(*)(id, SEL, NSRect_C))objc_msgSend)(b, sel_registerName("setFrame:"), (NSRect_C){{x, y}, {w, h}});
}

static void appkit_button_set_title(id b, const char *title) {
    id s = appkit_c_to_nsstring(title);
    appkit_msg_void1(b, "setTitle:", s);
    CFRelease((CFTypeRef)s);
}

static void appkit_label_set_alignment(id l, long align) {
    appkit_msg_void_long(l, "setAlignment:", align);
}

static void appkit_button_set_hidden(id b, int hidden) {
    appkit_msg_void_bool(b, "setHidden:", hidden);
}

// appkit_control_set_hidden: 通用控件显隐（TextField/Label/Popup/ImageView）。
static void appkit_control_set_hidden(id v, int hidden) {
    appkit_msg_void_bool(v, "setHidden:", hidden);
}

// appkit_button_set_state: NSControlStateValueOn(1)/Off(0)，用于 tab 选中态高亮。
static void appkit_button_set_state(id b, int on) {
    appkit_msg_void_long(b, "setState:", on ? 1 : 0);
}

static void appkit_button_set_bezel_style(id b, long style) {
    appkit_msg_void_long(b, "setBezelStyle:", style);
}

static void appkit_button_set_font_size(id b, double pt) {
    id font = ((id(*)(id, SEL, double))objc_msgSend)(appkit_class("NSFont"), sel_registerName("systemFontOfSize:"), pt);
    appkit_msg_void1(b, "setFont:", font);
}

// ---------- 复选开关 ----------
// 设置窗口功能开关：NSSwitchButton 复选样式，点击自动切换勾选态，
// 无需回调（Save 时直接读 state）。
static id appkit_create_checkbox(double x, double y, double w, double h, const char *title) {
    id b = ((id(*)(id, SEL, NSRect_C))objc_msgSend)(
        appkit_msg_id0(appkit_class("NSButton"), "alloc"), sel_registerName("initWithFrame:"),
        (NSRect_C){{x, y}, {w, h}});
    appkit_msg_void_long(b, "setButtonType:", 3); // NSSwitchButton：复选样式
    id t = appkit_c_to_nsstring(title ? title : "");
    appkit_msg_void1(b, "setTitle:", t);
    CFRelease((CFTypeRef)t);
    CFRetain((CFTypeRef)b);
    return b;
}

static long appkit_checkbox_state(id b) {
    return ((long(*)(id, SEL))objc_msgSend)(b, sel_registerName("state"));
}

static void appkit_checkbox_set_state(id b, long on) {
    appkit_msg_void_long(b, "setState:", on ? 1 : 0);
}

// ---------- 剪贴板与粘贴 ----------
// g_pb_self_written: 本进程最近一次写入剪贴板的文本。读取时若与之一致则拦截，
// 防止自身写入（Copy 按钮/Apply 降级复制/粘贴兜底）回环触发剪贴板监视。
static char *g_pb_self_written;

static char *appkit_strdup(const char *s) {
    if (s == NULL) return NULL;
    size_t n = strlen(s) + 1;
    char *p = (char *)malloc(n);
    if (p != NULL) memcpy(p, s, n);
    return p;
}

static void appkit_copy_to_pasteboard(const char *s) {
    id pb = ((id(*)(id, SEL))objc_msgSend)((id)objc_getClass("NSPasteboard"), sel_registerName("generalPasteboard"));
    if (pb == NULL) return;
    ((void(*)(id, SEL))objc_msgSend)(pb, sel_registerName("clearContents"));
    id ns = (id)CFStringCreateWithCString(NULL, s, kCFStringEncodingUTF8);
    if (ns == NULL) return;
    id type = (id)CFSTR("public.utf8-plain-text"); // NSPasteboardTypeString
    ((void(*)(id, SEL, id, id))objc_msgSend)(pb, sel_registerName("setString:forType:"), ns, type);
    CFRelease((CFTypeRef)ns);
    free(g_pb_self_written);
    g_pb_self_written = appkit_strdup(s);
}

// appkit_pasteboard_change_count: 返回 general pasteboard 的 changeCount（任何写入自增）。
static long appkit_pasteboard_change_count(void) {
    id pb = ((id(*)(id, SEL))objc_msgSend)((id)objc_getClass("NSPasteboard"), sel_registerName("generalPasteboard"));
    if (pb == NULL) return -1;
    return ((long(*)(id, SEL))objc_msgSend)(pb, sel_registerName("changeCount"));
}

// appkit_pasteboard_string: 读取剪贴板纯文本（malloc 的 UTF-8，调用者 free）。
// 非文本内容返回 NULL；文本与最近一次自身写入一致时也返回 NULL（防回环触发）。
static char *appkit_pasteboard_string(void) {
    id pb = ((id(*)(id, SEL))objc_msgSend)((id)objc_getClass("NSPasteboard"), sel_registerName("generalPasteboard"));
    if (pb == NULL) return NULL;
    id s = ((id(*)(id, SEL, id))objc_msgSend)(pb, sel_registerName("stringForType:"),
        (id)CFSTR("public.utf8-plain-text"));
    if (s == NULL) return NULL;
    char *out = appkit_nsstring_to_c(s);
    if (out != NULL && g_pb_self_written != NULL && strcmp(out, g_pb_self_written) == 0) {
        free(out);
        return NULL;
    }
    return out;
}

// appkit_paste_text: 剪贴板写入 s 后模拟 Cmd+V（kVK_ANSI_V）。
static void appkit_paste_text(const char *s) {
    appkit_copy_to_pasteboard(s);
    CGEventRef down = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)9, true);
    CGEventSetFlags(down, kCGEventFlagMaskCommand);
    CGEventPost(kCGHIDEventTap, down);
    CFRelease(down);
    CGEventRef up = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)9, false);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(up);
}
*/
import "C"

import (
	"log"
	"unsafe"
)

// cString 转 Go string（malloc 的，调用方负责 free）。
func toGo(p *C.char) string {
	if p == nil {
		return ""
	}
	s := C.GoString(p)
	C.free(unsafe.Pointer(p))
	return s
}

// InitApplication 初始化 NSApplication（Accessory 策略，Dock 不显示图标）。
// 进程内仅一次，必须在主线程调用。
func InitApplication() {
	C.appkit_init_application()
}

// Deactivate 归还激活状态（设置窗口关闭后调用，把焦点还给用户之前的应用）。
// 必须在主线程调用。
func Deactivate() {
	C.appkit_app_deactivate()
}

// ConfirmDialog 弹出模态确认对话框（阻塞直到用户点击）。
// title 为标题，message 为正文，accept/decline 分别为第一/第二按钮文案。
// 返回 true 表示点击 accept。必须在主线程调用。
func ConfirmDialog(title, message, accept, decline string) bool {
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	cm := C.CString(message)
	defer C.free(unsafe.Pointer(cm))
	ca := C.CString(accept)
	defer C.free(unsafe.Pointer(ca))
	cd := C.CString(decline)
	defer C.free(unsafe.Pointer(cd))
	return C.appkit_confirm_dialog(ct, cm, ca, cd) != 0
}

// RegisterHotkey 注册全局快捷键 Cmd+Shift+, 打开设置窗口。
// 返回是否注册成功（抢占失败时失败）。必须在主线程调用。
func RegisterHotkey() bool {
	return C.appkit_register_hotkey() != 0
}

// InitStatusBar 创建状态栏图标与下拉菜单（Settings/Exit）。
// 菜单项点击通过事件标志位 EventStatusSettings/EventStatusExit 上报
// （由 PollEvents 轮询分发）。进程内仅一次，必须在主线程调用。
func InitStatusBar() bool {
	png := statusIconPNG()
	if len(png) == 0 {
		return false
	}
	return C.appkit_status_init((*C.uchar)(unsafe.Pointer(&png[0])), C.int(len(png))) != 0
}

// InitInputMonitor 安装全局输入活动监控（任意按键/鼠标点击置标志）。
// 必须在主线程调用，进程内仅一次。返回是否安装成功。
func InitInputMonitor() bool {
	return C.appkit_init_input_monitor() != 0
}

// ConsumeInputActivity 返回并清除输入活动标志（任意按键/鼠标点击）。必须在主线程调用。
func ConsumeInputActivity() bool {
	return C.appkit_input_activity() != 0
}

// PollEvents 轮询并清除按钮/快捷键事件标志。必须在主线程调用。
func PollEvents() EventFlags {
	return EventFlags(C.appkit_poll_events())
}

// PumpEvents 从 NSApplication 事件队列拉取并派发一个鼠标/键盘事件（非阻塞）。
// 无 [NSApp run] 的 CLI 进程必须由主循环定期调用（泵完一批后处理其余逻辑），
// 否则窗口上的按钮点击、文本输入等交互不会生效。必须在主线程调用。
// 返回 true 表示本次处理了一个事件。
func PumpEvents() bool {
	return C.appkit_pump_events() != 0
}

// Handle 是对 AppKit 对象（NSWindow/NSTextField 等）的不透明句柄。
type Handle struct {
	ptr C.id
}

// NewHandle 包装外部已创建的 AppKit 对象指针（如 bubble 的 NSPanel 内容视图），
// 使其可复用 AddSubview 等方法。必须在主线程调用。
func NewHandle(ptr unsafe.Pointer) *Window {
	return &Window{ptr: C.id(ptr)}
}

// Window 是配置窗口句柄。
type Window Handle

// TextField 是文本框句柄。
type TextField Handle

// Label 是标签句柄。
type Label Handle

// Button 是按钮句柄。
type Button Handle

// Popup 是下拉框句柄（可编辑：可选预设项也可直接输入）。
type Popup Handle

// ImageView 是图像视图句柄（内置 LLM 登录区二维码展示）。
type ImageView Handle

// Checkbox 是复选开关句柄（设置窗口功能开关）。
type Checkbox Handle

// NewWindow 创建带标题栏的标准窗口（未显示）。必须在主线程调用。
func NewWindow(w, h float64, title string) *Window {
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	return &Window{ptr: C.appkit_create_window(C.double(w), C.double(h), ct)}
}

// Show 显示窗口：激活本应用（Accessory 应用需显式激活才能盖过前台应用）
// 并 makeKeyAndOrderFront 置为 key。窗口已可见但失焦时可再次调用重新激活。
// 必须在主线程调用。
func (w *Window) Show() {
	C.appkit_window_show(w.ptr)
}

// CenterOnMouseScreen 将窗口居中到鼠标所在屏幕的可见区域（无匹配时退回主屏）。
// 必须在主线程调用。
func (w *Window) CenterOnMouseScreen() {
	C.appkit_center_on_mouse_screen(w.ptr)
}

// Close 关闭窗口。必须在主线程调用。
func (w *Window) Close() {
	C.appkit_window_close(w.ptr)
}

// OrderOut 强制将窗口移出屏幕（close 未生效时的兜底，如存在进行中的文本编辑会话）。
// 必须在主线程调用。
func (w *Window) OrderOut() {
	C.appkit_window_orderout(w.ptr)
}

// Visible 查询窗口是否可见。必须在主线程调用。
func (w *Window) Visible() bool {
	return C.appkit_window_visible(w.ptr) != 0
}

// DumpWindowState 打印窗口真实屏幕状态（临时诊断用）。必须在主线程调用。
func (w *Window) DumpWindowState() {
	var x, y, ww, hh C.double
	C.appkit_window_frame(w.ptr, &x, &y, &ww, &hh)
	log.Printf("[dbg-window] frame=(%.0f,%.0f,%.0f,%.0f) level=%d visible=%v onActiveSpace=%v occlusion=%d appActive=%v onScreen=%d",
		float64(x), float64(y), float64(ww), float64(hh),
		int(C.appkit_window_level(w.ptr)),
		w.Visible(),
		C.appkit_window_on_active_space(w.ptr) != 0,
		int(C.appkit_window_occlusion_state(w.ptr)),
		C.appkit_app_active() != 0,
		int(C.appkit_own_onscreen()))
	C.appkit_dump_zorder(w.ptr)
	C.appkit_dump_frontmost()
}

// Release 释放窗口句柄。
func (w *Window) Release() {
	C.appkit_window_release(w.ptr)
	w.ptr = nil
}

// AddSubview 将控件加入容器：句柄为 NSWindow 时加入其 contentView，
// 句柄本身是 NSView（如气泡的内容视图）时直接加入。必须在主线程调用。
func (w *Window) AddSubview(view any) {
	switch v := view.(type) {
	case *TextField:
		C.appkit_add_subview(w.ptr, v.ptr)
	case *Label:
		C.appkit_add_subview(w.ptr, v.ptr)
	case *Button:
		C.appkit_add_subview(w.ptr, v.ptr)
	case *Popup:
		C.appkit_add_subview(w.ptr, v.ptr)
	case *ImageView:
		C.appkit_add_subview(w.ptr, v.ptr)
	case *Checkbox:
		C.appkit_add_subview(w.ptr, v.ptr)
	}
}

// MakeFirstResponder 设置初始焦点控件。必须在主线程调用。
func (w *Window) MakeFirstResponder(f *TextField) {
	C.appkit_make_first_responder(w.ptr, f.ptr)
}

// NewTextField 创建可编辑文本框。isSecure 为密码式（星号）输入。
func NewTextField(x, y, w, h float64, text string, isSecure bool) *TextField {
	ct := C.CString(text)
	defer C.free(unsafe.Pointer(ct))
	var sec C.int
	if isSecure {
		sec = 1
	}
	return &TextField{ptr: C.appkit_create_textfield(C.double(x), C.double(y), C.double(w), C.double(h), ct, sec)}
}

// String 读取文本框内容。必须在主线程调用。
func (f *TextField) String() string {
	return toGo(C.appkit_textfield_get(f.ptr))
}

// SetString 设置文本框内容。必须在主线程调用。
func (f *TextField) SetString(s string) {
	ct := C.CString(s)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_textfield_set(f.ptr, ct)
}

// SetEditable 设置文本框是否可编辑：false 时 Base URL 由厂商固定（只读）。必须在主线程调用。
func (f *TextField) SetEditable(editable bool) {
	var e C.int
	if editable {
		e = 1
	}
	C.appkit_textfield_set_editable(f.ptr, e)
}

// SetHidden 设置控件是否隐藏（内置 LLM 登录区随厂商切换显隐）。必须在主线程调用。
func (f *TextField) SetHidden(hidden bool) {
	var h C.int
	if hidden {
		h = 1
	}
	C.appkit_control_set_hidden(f.ptr, h)
}

// NewLabel 创建不可编辑右对齐标签。
func NewLabel(x, y, w, h float64, text string) *Label {
	ct := C.CString(text)
	defer C.free(unsafe.Pointer(ct))
	return &Label{ptr: C.appkit_create_label(C.double(x), C.double(y), C.double(w), C.double(h), ct)}
}

// SetHidden 设置控件是否隐藏（内置 LLM 登录区随厂商切换显隐）。必须在主线程调用。
func (l *Label) SetHidden(hidden bool) {
	var h C.int
	if hidden {
		h = 1
	}
	C.appkit_control_set_hidden(l.ptr, h)
}

// SetAlignment 设置标签对齐：0 左对齐 / 1 居中 / 2 右对齐（提示文本用左对齐）。
// 必须在主线程调用。
func (l *Label) SetAlignment(align int) {
	C.appkit_label_set_alignment(l.ptr, C.long(align))
}

// SetString 设置标签文本（提示行/余额回显）。必须在主线程调用。
func (l *Label) SetString(s string) {
	ct := C.CString(s)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_textfield_set(l.ptr, ct)
}

// NewPopup 创建下拉框（NSComboBox）：可从预设项中选择，也可直接键入自定义值
// （厂商下拉框随后调 SetEditable(false) 关闭手输）。
// tag 区分事件来源：TagPopupProvider → EventProviderChanged，
// TagPopupModel → EventModelChanged（轮询 PollEvents 派发）。必须在主线程调用。
func NewPopup(x, y, w, h float64, tag int64) *Popup {
	return &Popup{ptr: C.appkit_create_popup(C.double(x), C.double(y), C.double(w), C.double(h), C.long(tag))}
}

// AddItem 向下拉框追加一项。必须在主线程调用。
func (p *Popup) AddItem(title string) {
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_popup_add_item(p.ptr, ct)
}

// RemoveAllItems 清空下拉框全部选项（切换厂商时重建模型列表）。必须在主线程调用。
func (p *Popup) RemoveAllItems() {
	C.appkit_popup_remove_all_items(p.ptr)
}

// SetEditable 设置下拉框是否可输入：厂商下拉框用 false 锁定选项。必须在主线程调用。
func (p *Popup) SetEditable(editable bool) {
	var e C.int
	if editable {
		e = 1
	}
	C.appkit_popup_set_editable(p.ptr, e)
}

// Index 返回当前选中项下标（无选中项为 -1）。必须在主线程调用。
func (p *Popup) Index() int {
	return int(C.appkit_popup_index(p.ptr))
}

// SelectIndex 选中指定下标项（-1 清除选择，配合 SetString 回显自定义值）。
// 编程式选择不触发 action（不产生 EventModelChanged）。必须在主线程调用。
func (p *Popup) SelectIndex(i int) {
	C.appkit_popup_select_index(p.ptr, C.long(i))
}

// String 返回下拉框当前文本：选中项的标题或用户直接输入的自定义值。
// 必须在主线程调用。
func (p *Popup) String() string {
	return toGo(C.appkit_textfield_get(p.ptr))
}

// SetString 设置下拉框当前文本（回显自定义值）。必须在主线程调用。
func (p *Popup) SetString(s string) {
	ct := C.CString(s)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_textfield_set(p.ptr, ct)
}

// SetHidden 设置控件是否隐藏（内置 LLM 登录区随厂商切换显隐）。必须在主线程调用。
func (p *Popup) SetHidden(hidden bool) {
	var h C.int
	if hidden {
		h = 1
	}
	C.appkit_control_set_hidden(p.ptr, h)
}

// NewImageView 创建二维码图像视图：text 经 CoreImage CIQRCodeGenerator
// 生成二维码并等比缩放显示（内置 LLM 登录区的链接/二维码切换）。必须在主线程调用。
func NewImageView(x, y, w, h float64, text string) *ImageView {
	ct := C.CString(text)
	defer C.free(unsafe.Pointer(ct))
	img := C.appkit_qr_image(ct)
	return &ImageView{ptr: C.appkit_create_image_view(C.double(x), C.double(y), C.double(w), C.double(h), img)}
}

// SetHidden 设置控件是否隐藏（链接/二维码切换）。必须在主线程调用。
func (v *ImageView) SetHidden(hidden bool) {
	var h C.int
	if hidden {
		h = 1
	}
	C.appkit_control_set_hidden(v.ptr, h)
}

// SetText 重新生成二维码并替换视图图像（链接/二维码切换）。必须在主线程调用。
func (v *ImageView) SetText(text string) {
	ct := C.CString(text)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_image_view_set_text(v.ptr, ct)
}

// NewCheckbox 创建复选开关（NSSwitchButton：点击自动切换勾选态，Save 时读 state）。
// 必须在主线程调用。
func NewCheckbox(x, y, w, h float64, title string) *Checkbox {
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	return &Checkbox{ptr: C.appkit_create_checkbox(C.double(x), C.double(y), C.double(w), C.double(h), ct)}
}

// State 返回勾选状态。必须在主线程调用。
func (c *Checkbox) State() bool {
	return C.appkit_checkbox_state(c.ptr) != 0
}

// SetState 设置勾选状态（Show 回显配置）。必须在主线程调用。
func (c *Checkbox) SetState(on bool) {
	var s C.long
	if on {
		s = 1
	}
	C.appkit_checkbox_set_state(c.ptr, s)
}

// SetHidden 设置控件是否隐藏（随页签切换显隐）。必须在主线程调用。
func (c *Checkbox) SetHidden(hidden bool) {
	var h C.int
	if hidden {
		h = 1
	}
	C.appkit_control_set_hidden(c.ptr, h)
}

// OpenURL 用 NSWorkspace 在默认浏览器打开 URL（注册/友情捐献链接）。必须在主线程调用。
func OpenURL(url string) {
	ct := C.CString(url)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_open_url(ct)
}

// NewButton 创建按钮。tag 用于区分回调来源（EventSave/EventCancel/EventApply 等）。
func NewButton(x, y, w, h float64, title string, tag int64, isDefault bool) *Button {
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	var def C.int
	if isDefault {
		def = 1
	}
	return &Button{ptr: C.appkit_create_button(C.double(x), C.double(y), C.double(w), C.double(h), ct, C.long(tag), def)}
}

// SetFrame 设置控件位置与尺寸。必须在主线程调用。
func (b *Button) SetFrame(x, y, w, h float64) {
	C.appkit_button_set_frame(b.ptr, C.double(x), C.double(y), C.double(w), C.double(h))
}

// SetTitle 设置按钮标题（登录/退出登录切换）。必须在主线程调用。
func (b *Button) SetTitle(title string) {
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_button_set_title(b.ptr, ct)
}

// SetHidden 设置控件是否隐藏（中文场景隐藏 Correct/Analyze tab）。必须在主线程调用。
func (b *Button) SetHidden(hidden bool) {
	var h C.int
	if hidden {
		h = 1
	}
	C.appkit_button_set_hidden(b.ptr, h)
}

// SetState 设置按钮选中态（tab 高亮）。必须在主线程调用。
func (b *Button) SetState(on bool) {
	var s C.int
	if on {
		s = 1
	}
	C.appkit_button_set_state(b.ptr, s)
}

// SetBezelStyle 设置按钮外观样式（tab 用 NSBezelStyleInline=15）。必须在主线程调用。
func (b *Button) SetBezelStyle(style int64) {
	C.appkit_button_set_bezel_style(b.ptr, C.long(style))
}

// SetFontSize 设置按钮字体大小（pt）。必须在主线程调用。
func (b *Button) SetFontSize(pt float64) {
	C.appkit_button_set_font_size(b.ptr, C.double(pt))
}

// CopyToPasteboard 将文本写入系统剪贴板。必须在主线程调用。
func CopyToPasteboard(s string) {
	ct := C.CString(s)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_copy_to_pasteboard(ct)
}

// ClipboardChangeCount 返回系统剪贴板的 changeCount（任何剪贴板写入都会使其自增）。
// 剪贴板监视据此检测新复制动作。必须在主线程调用。
func ClipboardChangeCount() int64 {
	return int64(C.appkit_pasteboard_change_count())
}

// ClipboardString 读取系统剪贴板纯文本。必须在主线程调用。
// 非文本内容（图片/文件）与最近一次自身写入的文本返回空（防自身写入回环触发）。
func ClipboardString() string {
	return toGo(C.appkit_pasteboard_string())
}

// PasteText 将文本写入剪贴板并模拟 Cmd+V 粘贴到当前焦点位置。
// 用于 AX 写回失败时的兜底（配合预先设置好的选中区间保证替换边界）。
// 必须在主线程调用。
func PasteText(s string) {
	ct := C.CString(s)
	defer C.free(unsafe.Pointer(ct))
	C.appkit_paste_text(ct)
}
