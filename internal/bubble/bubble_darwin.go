//go:build darwin

package bubble

/*
#cgo LDFLAGS: -framework AppKit -framework QuartzCore -framework CoreFoundation -framework CoreGraphics

#include <objc/runtime.h>
#include <objc/message.h>
#include <CoreFoundation/CoreFoundation.h>
#include <CoreGraphics/CoreGraphics.h>
#include <stddef.h>
#include <stdio.h>
#include <math.h>

// AppKit 的 C 入口：确保 AppKit 运行时已加载（CLI 进程必需）。
extern Boolean NSApplicationLoad(void);

// 与 NSPoint/NSSize/NSRect 布局一致的 C 结构体（均为 double）。
typedef struct { double x, y; } NSPoint_C;
typedef struct { double w, h; } NSSize_C;
typedef struct { NSPoint_C origin; NSSize_C size; } NSRect_C;

// 返回结构体（>16 字节）在 x86_64 上必须走 objc_msgSend_stret，
// 且 sret 隐藏指针作为第一个参数传入（rdi）。
static NSRect_C msg_rect(id obj, const char *selname) {
    SEL sel = sel_registerName(selname);
#if defined(__x86_64__)
    NSRect_C r = {{0, 0}, {0, 0}};
    ((void(*)(void *, id, SEL))objc_msgSend_stret)(&r, obj, sel);
    return r;
#else
    return ((NSRect_C(*)(id, SEL))objc_msgSend)(obj, sel);
#endif
}

#define MSG_RECT(obj, selname) msg_rect((obj), (selname))

// boundingRectWithSize:options:context: 的发送辅助：NSSize 是 16 字节 SSE 参数，
// 与 msg_rect 的 NSRect（32 字节内存参数）ABI 不同，需独立签名保证调用正确。
static NSRect_C msg_rect_size_opts(id obj, const char *selname, NSSize_C s, unsigned long opts, id ctx) {
    SEL sel = sel_registerName(selname);
#if defined(__x86_64__)
    NSRect_C r = {{0, 0}, {0, 0}};
    ((void(*)(void *, id, SEL, NSSize_C, unsigned long, id))objc_msgSend_stret)(&r, obj, sel, s, opts, ctx);
    return r;
#else
    return ((NSRect_C(*)(id, SEL, NSSize_C, unsigned long, id))objc_msgSend)(obj, sel, s, opts, ctx);
#endif
}

static id msg_id0(id obj, const char *name) {
    SEL sel = sel_registerName(name);
    return ((id(*)(id, SEL))objc_msgSend)(obj, sel);
}

static id msg_id1(id obj, const char *name, id a1) {
    SEL sel = sel_registerName(name);
    return ((id(*)(id, SEL, id))objc_msgSend)(obj, sel, a1);
}

static id msg_id_rect(id obj, const char *name, NSRect_C r) {
    SEL sel = sel_registerName(name);
    return ((id(*)(id, SEL, NSRect_C))objc_msgSend)(obj, sel, r);
}

static void msg_void0(id obj, const char *name) {
    SEL sel = sel_registerName(name);
    ((void(*)(id, SEL))objc_msgSend)(obj, sel);
}

static void msg_void_bool(id obj, const char *name, char b) {
    SEL sel = sel_registerName(name);
    ((void(*)(id, SEL, char))objc_msgSend)(obj, sel, b);
}

static void msg_void_long(id obj, const char *name, long v) {
    SEL sel = sel_registerName(name);
    ((void(*)(id, SEL, long))objc_msgSend)(obj, sel, v);
}

static void msg_void_double(id obj, const char *name, double v) {
    SEL sel = sel_registerName(name);
    ((void(*)(id, SEL, double))objc_msgSend)(obj, sel, v);
}

static void msg_void_point(id obj, const char *name, NSPoint_C p) {
    SEL sel = sel_registerName(name);
    ((void(*)(id, SEL, NSPoint_C))objc_msgSend)(obj, sel, p);
}

static void msg_void_size(id obj, const char *name, NSSize_C s) {
    SEL sel = sel_registerName(name);
    ((void(*)(id, SEL, NSSize_C))objc_msgSend)(obj, sel, s);
}

static void msg_void_rect_bool(id obj, const char *name, NSRect_C r, char b) {
    SEL sel = sel_registerName(name);
    ((void(*)(id, SEL, NSRect_C, char))objc_msgSend)(obj, sel, r, b);
}

static void msg_void1(id obj, const char *name, id a1) {
    SEL sel = sel_registerName(name);
    ((void(*)(id, SEL, id))objc_msgSend)(obj, sel, a1);
}

static id msg_id_double0(id obj, const char *name, double v) {
    SEL sel = sel_registerName(name);
    return ((id(*)(id, SEL, double))objc_msgSend)(obj, sel, v);
}

// 常量
enum {
    NS_ACCESSORY_POLICY = 1,          // NSApplicationActivationPolicyAccessory
    NS_BORDERLESS = 0,                // NSWindowStyleMaskBorderless
    NS_NONACTIVATING = 1 << 7,        // NSWindowStyleMaskNonactivatingPanel
    NS_BACKING_BUFFERED = 2,          // NSBackingStoreBuffered
    NS_FLOATING_LEVEL = 3,            // NSFloatingWindowLevel (CGWindowLevelForKey(kCGFloatingWindowLevel))
    NS_WRAP_MODE = 0,                 // NSLineBreakByWordWrapping
    NS_HUD_MATERIAL = 13,             // NSVisualEffectMaterialHUDWindow
    NS_BEHIND_WINDOW = 0,             // NSVisualEffectBlendingModeBehindWindow
    NS_WITHIN_WINDOW = 1,             // NSVisualEffectBlendingModeWithinWindow
    NS_EFFECT_ACTIVE = 1,             // NSVisualEffectStateActive
};
#define NS_CORNER_RADIUS 12.0         // 圆角
#define NS_PAD_X 12.0                 // 文本左右内边距
#define NS_PAD_Y 8.0                  // 文本上下内边距
#define NS_MAX_WIDTH 360.0            // 气泡最大宽度（超宽文本按 NS_LABEL_MAX_W 断行）
#define NS_LABEL_MAX_W (NS_MAX_WIDTH - 2 * NS_PAD_X - (NS_ROW_BTN_W + NS_ROW_GAP2)) // 标签断行上限
#define NS_GAP 8.0                    // 与输入框间距
#define NS_ROW_H 24.0                 // 底部按钮行高
#define NS_TAB_H 22.0                 // 顶部 tab 栏行高
#define NS_ROW_GAP 6.0                // 标签与按钮行间距
#define NS_TAB_GAP 6.0                // 标签与 tab 栏间距
#define NS_ROW_BTN_W 54.0             // 行内 Apply/Copy/Copied 按钮宽
#define NS_ROW_GAP2 4.0               // 英文行与中文行间距
#define NS_MIN_W 240.0                // 气泡最小宽（tab 行 64+70+6+24(关闭)+2*12(内边距)=188，留余量）

static id g_panel;  // NSPanel 实例（bubble_init 创建，进程内唯一）
static id g_vev;    // 常驻内容视图（视觉效果视图）
static id g_label_en;  // 英文行常驻文本标签
static id g_label_zh;  // 中文行常驻文本标签（单行模式为空隐藏）

// 鼠标进出跟踪：mouseEntered/mouseExited 在 C 回调里置标志（回调不进 Go，避免 cgo 重入）。
static volatile int g_mouse_inside;
static volatile int g_mouse_flags;

static void bubble_on_mouse_enter(id self, SEL _cmd, id event) {
    g_mouse_inside = 1;
    g_mouse_flags |= 1;
}

static void bubble_on_mouse_exit(id self, SEL _cmd, id event) {
    g_mouse_inside = 0;
    g_mouse_flags |= 2;
}

// bubble_new_tracker: 动态 NSObject 子类实例，作为 NSTrackingArea 的 owner。
static id bubble_new_tracker(void) {
    static Class cls;
    if (cls == NULL) {
        cls = objc_allocateClassPair((Class)objc_getClass("NSObject"), "ElfinBubbleTracker", 0);
        class_addMethod(cls, sel_registerName("mouseEntered:"), (IMP)bubble_on_mouse_enter, "v@:@");
        class_addMethod(cls, sel_registerName("mouseExited:"), (IMP)bubble_on_mouse_exit, "v@:@");
        objc_registerClassPair(cls);
    }
    return class_createInstance(cls, 0);
}

// bubble_install_tracking: 在内容视图上装 NSTrackingArea，跟踪鼠标进出。
// options: NSTrackingMouseEnteredAndExited | NSTrackingActiveAlways | NSTrackingInVisibleRect。
static void bubble_install_tracking(id view) {
    id tracker = bubble_new_tracker();
    CFRetain((CFTypeRef)tracker); // owner 不被 tracking area 持有，进程级持有防悬垂
    id ta = ((id(*)(id, SEL, NSRect_C, unsigned long, id, id))objc_msgSend)(
        ((id(*)(id, SEL))objc_msgSend)((id)objc_getClass("NSTrackingArea"), sel_registerName("alloc")),
        sel_registerName("initWithRect:options:owner:userInfo:"),
        (NSRect_C){{0, 0}, {10, 10}}, 0x281, tracker, NULL);
    ((void(*)(id, SEL, id))objc_msgSend)(view, sel_registerName("addTrackingArea:"), ta);
}

// ---------- 拖动支持 ----------
// 无边框气泡无标题栏，无法依赖系统拖动（mouseDownCanMoveWindow 覆盖会让
// AppKit 走内部拖动路径，在无边框 NSPanel 上实际不移动窗口且不向 view 派发
// mouseDown），这里改为手动拖动：mouseDown 记录按下点与窗口原点（CG 全局
// 坐标），mouseDragged 按偏移移动窗口，mouseUp 结束，不阻塞事件循环。
// 按钮是 NSControl 子视图，命中按钮时事件派发给按钮本身，不走本方法；
// 标签 hitTest 返回 NULL 穿透到本视图，文本区域同样可发起拖动。
static id bubble_label_hit_test(id self, SEL _cmd, NSPoint_C p) {
    return NULL;
}

// 按下时的 CG 全局位置（主屏左上原点）与窗口原点（AppKit 坐标）。
// CG y 向下、AppKit y 向上，移动量需反向。
static double g_drag_cg_x = 0, g_drag_cg_y = 0;
static double g_drag_win_x = 0, g_drag_win_y = 0;
static int g_dragging = 0;

// bubble_event_cg_point: 取事件的底层 CGEvent 全局位置。
// locationInScreen 在 macOS 15 上会抛 NSException，CGEvent 路径稳定可靠。
static CGPoint bubble_event_cg_point(id event) {
    id ce = msg_id0(event, "CGEvent");
    if (ce == NULL) return CGPointMake(0, 0);
    return CGEventGetLocation((CGEventRef)ce);
}

static void bubble_view_mouse_down(id self, SEL _cmd, id event) {
    id win = msg_id0(self, "window");
    if (win == NULL) return;
    NSRect_C f = MSG_RECT(win, "frame");
    CGPoint loc = bubble_event_cg_point(event);
    g_drag_cg_x = loc.x;
    g_drag_cg_y = loc.y;
    g_drag_win_x = f.origin.x;
    g_drag_win_y = f.origin.y;
    g_dragging = 1;
}

static void bubble_view_mouse_dragged(id self, SEL _cmd, id event) {
    if (!g_dragging) return;
    id win = msg_id0(self, "window");
    if (win == NULL) return;
    CGPoint loc = bubble_event_cg_point(event);
    double nx = g_drag_win_x + (loc.x - g_drag_cg_x);
    double ny = g_drag_win_y - (loc.y - g_drag_cg_y); // CG y 向下，转 AppKit
    msg_void_point(win, "setFrameOrigin:", (NSPoint_C){nx, ny});
}

static void bubble_view_mouse_up(id self, SEL _cmd, id event) {
    g_dragging = 0;
}

// bubble_panel_class: NSPanel 动态子类（无边框面板，拖动由内容视图手动实现）。
static Class bubble_panel_class(void) {
    static Class cls;
    if (cls == NULL) {
        cls = objc_allocateClassPair((Class)objc_getClass("NSPanel"), "ElfinBubblePanel", 0);
        objc_registerClassPair(cls);
    }
    return cls;
}

// bubble_view_class: NSVisualEffectView 动态子类（mouseDown/mouseDragged/mouseUp → 手动拖动）。
static Class bubble_view_class(void) {
    static Class cls;
    if (cls == NULL) {
        cls = objc_allocateClassPair((Class)objc_getClass("NSVisualEffectView"), "ElfinBubbleView", 0);
        class_addMethod(cls, sel_registerName("mouseDown:"), (IMP)bubble_view_mouse_down, "v@:@");
        class_addMethod(cls, sel_registerName("mouseDragged:"), (IMP)bubble_view_mouse_dragged, "v@:@");
        class_addMethod(cls, sel_registerName("mouseUp:"), (IMP)bubble_view_mouse_up, "v@:@");
        objc_registerClassPair(cls);
    }
    return cls;
}

// bubble_label_class: NSTextField 动态子类（hitTest: 返回 NULL，鼠标透明）。
static Class bubble_label_class(void) {
    static Class cls;
    if (cls == NULL) {
        cls = objc_allocateClassPair((Class)objc_getClass("NSTextField"), "ElfinBubbleLabel", 0);
        class_addMethod(cls, sel_registerName("hitTest:"), (IMP)bubble_label_hit_test, "@@:{CGPoint=dd}");
        objc_registerClassPair(cls);
    }
    return cls;
}

// NSRange 结构（Foundation 的 NSRange 为 {NSUInteger location, length}）。
typedef struct { unsigned long loc; unsigned long len; } NSRange_C;

// 差异区间（UTF-16 码元单位，与 NSRange 一致）。
typedef struct { long start; long len; } bubble_diff_t;

static long msg_long0(id obj, const char *name) {
    SEL sel = sel_registerName(name);
    return ((long(*)(id, SEL))objc_msgSend)(obj, sel);
}

// addAttribute:value:range: 发送辅助（range 为 NSRange_C 结构体）。
static void msg_void_attr_range(id obj, id attrName, id value, NSRange_C r) {
    SEL sel = sel_registerName("addAttribute:value:range:");
    ((void(*)(id, SEL, id, id, NSRange_C))objc_msgSend)(obj, sel, attrName, value, r);
}

// bubble_init: 创建无边框浮动面板、常驻内容视图与标签（仅调用一次）。
static void bubble_init(void) {
    if (g_panel != NULL) return;

    NSApplicationLoad();
    id app = msg_id0((id)objc_getClass("NSApplication"), "sharedApplication");
    msg_void_long(app, "setActivationPolicy:", NS_ACCESSORY_POLICY);

    // panel = [[NSPanel alloc] initWithContentRect:styleMask:backing:defer:]
    id cls = (id)objc_getClass("NSPanel");
    id panel = msg_id0(cls, "alloc");
    NSRect_C zero = {{0, 0}, {10, 10}};
    SEL initSel = sel_registerName("initWithContentRect:styleMask:backing:defer:");
    panel = ((id(*)(id, SEL, NSRect_C, unsigned long, unsigned long, char))objc_msgSend)(
        panel, initSel, zero, NS_BORDERLESS | NS_NONACTIVATING, NS_BACKING_BUFFERED, 0);
    object_setClass(panel, bubble_panel_class()); // 无边框面板可拖动

    msg_void_long(panel, "setLevel:", NS_FLOATING_LEVEL);
    msg_void_bool(panel, "setOpaque:", 0);
    msg_void1(panel, "setBackgroundColor:", msg_id0((id)objc_getClass("NSColor"), "clearColor"));
    // 深色外观：tab/按钮文字自动用浅色，深底上保持可读。
    id darkApp = ((id(*)(id, SEL, id))objc_msgSend)((id)objc_getClass("NSAppearance"),
        sel_registerName("appearanceNamed:"), (id)CFSTR("NSAppearanceNameDarkAqua"));
    msg_void1(panel, "setAppearance:", darkApp);
    msg_void_bool(panel, "setHasShadow:", 1);
    msg_void_bool(panel, "setIgnoresMouseEvents:", 0);   // 可交互：底部按钮需接收点击
    msg_void_bool(panel, "setReleasedWhenClosed:", 0);

    // 常驻内容视图（HUD 深色背景 + 圆角）
    id vev = msg_id_rect((id)msg_id0((id)objc_getClass("NSVisualEffectView"), "alloc"),
        "initWithFrame:", (NSRect_C){{0, 0}, {10, 10}});
    object_setClass(vev, bubble_view_class()); // 背景命中即发起拖动
    msg_void_long(vev, "setMaterial:", NS_HUD_MATERIAL);
    // 材质仅与窗口自身内容混合（不透后面屏幕内容），底色稳定可控：
    // BehindWindow 在浅色壁纸下材质透亮，白色文字几乎不可读。
    msg_void_long(vev, "setBlendingMode:", NS_WITHIN_WINDOW);
    msg_void_long(vev, "setState:", NS_EFFECT_ACTIVE);
    msg_void_bool(vev, "setWantsLayer:", 1);
    id layer = msg_id0(vev, "layer");
    msg_void_double(layer, "setCornerRadius:", NS_CORNER_RADIUS);
    msg_void_bool(layer, "setMasksToBounds:", 1);
    // 深色半透明底色：任何屏幕背景下浅色文字都有足够对比度。
    id bg = ((id(*)(id, SEL, double, double, double, double))objc_msgSend)(
        (id)objc_getClass("NSColor"), sel_registerName("colorWithSRGBRed:green:blue:alpha:"),
        0.12, 0.12, 0.15, 0.92);
    ((void(*)(id, SEL, id))objc_msgSend)(layer, sel_registerName("setBackgroundColor:"),
        msg_id0(bg, "CGColor"));

    // 常驻文本标签（英文行在上、中文行在下；单行模式中文行隐藏）
    CFStringRef empty = CFStringCreateWithCString(NULL, "", kCFStringEncodingUTF8);
    id label_en = msg_id1((id)objc_getClass("NSTextField"), "labelWithString:", (id)empty);
    id label_zh = msg_id1((id)objc_getClass("NSTextField"), "labelWithString:", (id)empty);
    CFRelease(empty);
    // 行内按钮占用右侧宽度，标签最大宽相应收窄（文本+按钮不超过 NS_MAX_WIDTH）。
    double labelMaxW = NS_LABEL_MAX_W;
    for (int i = 0; i < 2; i++) {
        id label = i == 0 ? label_en : label_zh;
        object_setClass(label, bubble_label_class()); // 文本区域鼠标透明，可拖动
        msg_void_double(label, "setPreferredMaxLayoutWidth:", labelMaxW);
        msg_void_long(label, "setMaximumNumberOfLines:", 0);     // 多行
        msg_void_long(label, "setLineBreakMode:", NS_WRAP_MODE);
        msg_void_bool(label, "setUsesSingleLineMode:", 0);
        id font = msg_id_double0((id)objc_getClass("NSFont"), "systemFontOfSize:", 13.0);
        msg_void1(label, "setFont:", font);
        msg_void1(label, "setTextColor:", msg_id0((id)objc_getClass("NSColor"), "whiteColor"));
        msg_void1(vev, "addSubview:", label);
    }

    msg_void1(panel, "setContentView:", vev);
    bubble_install_tracking(vev);

    g_panel = panel;
    g_vev = vev;
    g_label_en = label_en;
    g_label_zh = label_zh;
}

// bubble_content_view: 返回常驻内容视图（供 Go 侧添加按钮）。
static void *bubble_content_view(void) {
    return g_vev;
}

// bubble_set_label: 设置标签富文本(基础色 + 变化词红色)并 sizeToFit,返回 (w, h)。
static void bubble_set_label(id label, const char *text, int green,
                            const bubble_diff_t *diffs, int diff_count,
                            double *out_w, double *out_h) {
    CFStringRef cfstr = CFStringCreateWithCString(NULL, text, kCFStringEncodingUTF8);
    if (cfstr == NULL) return;
    id attr = msg_id0((id)objc_getClass("NSMutableAttributedString"), "alloc");
    attr = msg_id1(attr, "initWithString:", (id)cfstr);
    CFRelease(cfstr);

    // 字体与基础色覆盖全文(attributed string 需显式携带字体参与尺寸计算)。
    id font = msg_id_double0((id)objc_getClass("NSFont"), "systemFontOfSize:", 13.0);
    id base;
    if (green) {
        base = ((id(*)(id, SEL, double, double, double, double))objc_msgSend)(
            (id)objc_getClass("NSColor"), sel_registerName("colorWithSRGBRed:green:blue:alpha:"),
            0.30, 0.85, 0.39, 1.0);
    } else {
        base = msg_id0((id)objc_getClass("NSColor"), "whiteColor");
    }
    long total = msg_long0(msg_id0(attr, "string"), "length");
    msg_void_attr_range(attr, (id)CFSTR("NSFont"), font, (NSRange_C){0, (unsigned long)total});
    msg_void_attr_range(attr, (id)CFSTR("NSForegroundColor"), base, (NSRange_C){0, (unsigned long)total});

    // 变化词区间标红(SRGB 1.0/0.32/0.32,与 Windows 端 BUB_DIFF_RED 一致)。
    if (diff_count > 0) {
        fprintf(stderr, "[bubble] set_label total=%ld diffs=%d:", total, diff_count);
        id red = ((id(*)(id, SEL, double, double, double, double))objc_msgSend)(
            (id)objc_getClass("NSColor"), sel_registerName("colorWithSRGBRed:green:blue:alpha:"),
            1.0, 0.32, 0.32, 1.0);
        for (int i = 0; i < diff_count; i++) {
            if (diffs[i].start < 0 || diffs[i].len <= 0 || diffs[i].start + diffs[i].len > total) {
                fprintf(stderr, " [%ld,%ld)(skip)", diffs[i].start, diffs[i].start + diffs[i].len);
                continue;
            }
            msg_void_attr_range(attr, (id)CFSTR("NSForegroundColor"), red,
                (NSRange_C){(unsigned long)diffs[i].start, (unsigned long)diffs[i].len});
            fprintf(stderr, " [%ld,%ld)", diffs[i].start, diffs[i].start + diffs[i].len);
        }
        fprintf(stderr, "\n");
    }

    msg_void1(label, "setAttributedStringValue:", attr);
    // sizeToFit 不尊重 preferredMaxLayoutWidth（长文本按单行全宽测量，气泡被撑宽），
    // 改用 boundingRectWithSize 按 NS_LABEL_MAX_W 断行测量（与绘制断行规则一致）。
    NSRect_C br = msg_rect_size_opts(attr, "boundingRectWithSize:options:context:",
        (NSSize_C){NS_LABEL_MAX_W, 1e9},
        0x1 | 0x2, // NSStringDrawingUsesLineFragmentOrigin | UsesFontLeading
        NULL);
    double lw = br.size.w, lh = ceil(br.size.h);
    // 标签 frame 宽固定为 NS_LABEL_MAX_W：绘制按该宽度断行，与测量上限一致；
    // 若窄于测量上限，多行文本会以更窄宽度重新断行导致高度溢出被裁切。
    msg_void_size(label, "setFrameSize:", (NSSize_C){NS_LABEL_MAX_W, lh});
    msg_void0(attr, "release"); // 标签已 retain，释放本函数持有的引用
    if (out_w != NULL) *out_w = lw;
    if (out_h != NULL) *out_h = lh;
}

// bubble_show: 在 AX 坐标 (ax_x, ax_y, ax_w, ax_h) 的输入框上方显示文本气泡。
// 双行模式(Correct tab): text 为英文行、text2 为中文行;单行模式 text2 为空。
// AX 坐标原点为主屏左上(y 向下);Cocoa 原点为主屏左下(y 向上)。
// out_w/out_h 返回内容视图尺寸(供 Go 侧摆放按钮),
// out_en_h/out_en_w 返回英文行高宽、out_zh_h/out_zh_w 返回中文行高宽
// (供 Go 侧摆放行内 Apply 按钮与顶部 tab 栏)。
static void bubble_show(const char *text, const char *text2, int green, const bubble_diff_t *diffs, int diff_count,
                        double ax_x, double ax_y, double ax_w, double ax_h,
                        double *out_w, double *out_h, double *out_en_h, double *out_en_w,
                        double *out_zh_h, double *out_zh_w) {
    if (g_panel == NULL) return;

    // 主屏高度(Cocoa 坐标)
    id screen = msg_id0((id)objc_getClass("NSScreen"), "mainScreen");
    NSRect_C sf = MSG_RECT(screen, "frame");
    double screenH = sf.origin.y + sf.size.h;

    // 渲染两行标签(单行模式中文行隐藏)。
    int two = text2 != NULL && text2[0] != '\0';
    double en_w = 0, en_h = 0, zh_w = 0, zh_h = 0;
    bubble_set_label(g_label_en, text, green, diffs, diff_count, &en_w, &en_h);
    if (two) {
        bubble_set_label(g_label_zh, text2, 0, NULL, 0, &zh_w, &zh_h);
    }
    msg_void_bool(g_label_zh, "setHidden:", two ? 0 : 1);

    double padX = NS_PAD_X, padY = NS_PAD_Y;
    // 行宽 = 文本宽 + 行内按钮宽 + 间距（标签最大宽已收窄保证不超 NS_MAX_WIDTH）。
    double w = en_w + NS_ROW_BTN_W + NS_ROW_GAP2 + 2 * padX;
    if (two && zh_w + NS_ROW_BTN_W + NS_ROW_GAP2 + 2 * padX > w) {
        w = zh_w + NS_ROW_BTN_W + NS_ROW_GAP2 + 2 * padX;
    }
    if (w < NS_MIN_W) w = NS_MIN_W;
    if (w < 40) w = 40;
    if (w > NS_MAX_WIDTH) w = NS_MAX_WIDTH; // 硬钳制：防御超长不可断文本撑爆气泡
    double h = NS_TAB_H + NS_TAB_GAP + en_h + (two ? NS_ROW_GAP2 + zh_h : 0)
        + NS_ROW_GAP + NS_ROW_H + 2 * padY;

    // 标签位置(Cocoa 左下原点):按钮行在底、中文行在按钮行上方、英文行再上方。
    double zhY = padY + NS_ROW_H + NS_ROW_GAP;
    double enY = zhY + (two ? zh_h + NS_ROW_GAP2 : 0);
    msg_void_point(g_label_en, "setFrameOrigin:", (NSPoint_C){padX, enY});
    if (two) {
        msg_void_point(g_label_zh, "setFrameOrigin:", (NSPoint_C){padX, zhY});
    }
    msg_void_size(g_vev, "setFrameSize:", (NSSize_C){w, h});

    // 气泡位置：输入框上方 8pt
    // AX: 输入框顶部 y = ax_y。Cocoa: 输入框顶部 = screenH - ax_y。
    // 气泡底 = 输入框顶部 + GAP。
    double screenW = sf.origin.x + sf.size.w;
    double bottom = screenH - ax_y + NS_GAP;
    double x = ax_x;
    double y = bottom;
    // 若上方放不下（贴近屏幕顶部），改放输入框下方
    if (y + h > screenH - 4) {
        y = screenH - (ax_y + ax_h) - NS_GAP - h;
    }
    if (y < 4) y = 4;
    // 横向越界钳制回屏内（与 Windows 端一致）。
    if (x + w > screenW - 4) x = screenW - w - 4;
    if (x < 4) x = 4;

    msg_void_size(g_panel, "setContentSize:", (NSSize_C){w, h});
    msg_void_rect_bool(g_panel, "setFrame:display:", (NSRect_C){{x, y}, {w, h}}, 1);
    msg_void1(g_panel, "orderFront:", NULL);

    if (out_w != NULL) *out_w = w;
    if (out_h != NULL) *out_h = h;
    if (out_en_h != NULL) *out_en_h = en_h;
    if (out_en_w != NULL) *out_en_w = en_w;
    if (out_zh_h != NULL) *out_zh_h = zh_h;
    if (out_zh_w != NULL) *out_zh_w = zh_w;
}

// bubble_hide: 隐藏气泡。
static void bubble_hide(void) {
    if (g_panel == NULL) return;
    msg_void1(g_panel, "orderOut:", NULL);
    // 重置鼠标跟踪状态：窗口隐藏后 tracking 暂停，mouseExited 可能不派发，
    // 残留"鼠标在内"标志会导致下次弹出后输入活动永不淡出；
    // 重新显示时 NSTrackingArea 会按实际鼠标位置重新校准。
    g_mouse_inside = 0;
    g_mouse_flags = 0;
}

// bubble_visible: 气泡当前是否可见。
static int bubble_visible(void) {
    if (g_panel == NULL) return 0;
    char b = ((char(*)(id, SEL))objc_msgSend)(g_panel, sel_registerName("isVisible"));
    return b ? 1 : 0;
}

// bubble_set_alpha: 设置面板整体透明度（淡出动画由 Go 侧驱动）。
static void bubble_set_alpha(double a) {
    if (g_panel == NULL) return;
    msg_void_double(g_panel, "setAlphaValue:", a);
}

// bubble_mouse_inside: 鼠标当前是否位于气泡内。
static int bubble_mouse_inside(void) {
    return g_mouse_inside;
}

// bubble_mouse_flags: 返回并清除鼠标进出事件（bit0=进入，bit1=离开）。
static int bubble_mouse_flags(void) {
    int f = g_mouse_flags;
    g_mouse_flags = 0;
    return f;
}
*/
import "C"

import (
	"fmt"
	"unsafe"

	"elfin-grammar/internal/appkit"
	"elfin-grammar/internal/ax"
)

// 气泡控件（进程内唯一，Init 创建）。
var (
	btnTabCorrect *appkit.Button
	btnTabAnalyze *appkit.Button
	tabList       []*appkit.Button
	btnApplyEn    *appkit.Button // 英文行行内按钮（Apply/Copy/Copied 动态）
	btnApplyZh    *appkit.Button // 中文行行内按钮（Apply/Copy/Copied 动态）
	btnCopy       *appkit.Button
	btnSettings   *appkit.Button
	btnList       []*appkit.Button
	btnClose      *appkit.Button // 右上角关闭按钮
	// tabWidths 与 tabList 一一对应；btnWidths 与 btnList 一一对应。
	tabWidths = []float64{64, 70} // Correct/Analyze（Translate 已合并进 Correct）
	btnWidths = []float64{48, 72} // Copy/Settings（Apply 已改为行内按钮）
	// 最近一次 Show 的各行高宽（摆行内按钮与 tab 栏用）。
	enH, enW  float64
	zhH, zhW  float64
	bubbleW   float64      // 最近一次 Show 的内容视图宽度（摆关闭按钮用）
	activeTab = TabCorrect // 当前选中 tab
)

// Init 初始化气泡窗口（NSPanel + 常驻内容视图 + 顶部 tab 栏 + 行内按钮 + 底部按钮行）。
// 必须在主线程调用，进程内仅调用一次。
func Init() {
	C.bubble_init()
	content := appkit.NewHandle(unsafe.Pointer(C.bubble_content_view()))
	btnTabCorrect = appkit.NewButton(0, 0, 64, tabH, "Correct", tagTabCorrect, false)
	btnTabAnalyze = appkit.NewButton(0, 0, 70, tabH, "Analyze", tagTabAnalyze, false)
	tabList = []*appkit.Button{btnTabCorrect, btnTabAnalyze}
	for _, tb := range tabList {
		tb.SetBezelStyle(15) // NSBezelStyleInline：页签风格，SetState 驱动选中高亮
		tb.SetFontSize(11)
		content.AddSubview(tb)
	}
	// 行内按钮：紧跟各文本行右侧（标题由 Show 的 TabOptions 动态指定）。
	btnApplyEn = appkit.NewButton(0, 0, rowBtnW, btnRowH, "Apply", tagApplyEn, false)
	btnApplyZh = appkit.NewButton(0, 0, rowBtnW, btnRowH, "Apply", tagApplyZh, false)
	content.AddSubview(btnApplyEn)
	content.AddSubview(btnApplyZh)
	btnCopy = appkit.NewButton(0, 0, 48, btnRowH, "Copy", tagCopy, false)
	btnSettings = appkit.NewButton(0, 0, 72, btnRowH, "Settings", tagSettings, false)
	btnList = []*appkit.Button{btnCopy, btnSettings}
	for _, b := range btnList {
		content.AddSubview(b)
	}
	btnClose = appkit.NewButton(0, 0, closeW, tabH, "✕", tagClose, false)
	btnClose.SetBezelStyle(15) // NSBezelStyleInline：与 tab 行风格统一
	btnClose.SetFontSize(13)
	content.AddSubview(btnClose)
}

// Show 在输入框上方显示文本气泡（顶部 tab 栏 + 内容 + 底部按钮行）。
// r 为输入框在 AX 坐标系中的位置。en/zh 为英文行/中文行文本（zh 为空即单行模式）。
// green 为 true 时英文行文字用绿色（表示无纠正的肯定反馈）；
// diffs 为英文行的变化词区间（UTF-16 码元单位），非空时对应文字标红。
// opts.EnTitle/ZhTitle 控制行内按钮标题（空=隐藏）。必须在主线程调用。
func Show(en, zh string, r ax.Rect, opts TabOptions, green bool, diffs []Span) {
	CancelFade() // 新内容出现时恢复完全不透明
	fmt.Printf("[bubble] Show en=%q zh=%q green=%v diffs=%v\n", en, zh, green, diffs)
	cEn := C.CString(en)
	defer C.free(unsafe.Pointer(cEn))
	cZh := C.CString(zh)
	defer C.free(unsafe.Pointer(cZh))
	cDiffs := make([]C.bubble_diff_t, len(diffs))
	for i, d := range diffs {
		cDiffs[i] = C.bubble_diff_t{start: C.long(d.Start), len: C.long(d.Length)}
	}
	var diffPtr *C.bubble_diff_t
	if len(cDiffs) > 0 {
		diffPtr = &cDiffs[0]
	}
	var w, h, eh, ew, zhh, zhw C.double
	C.bubble_show(cEn, cZh, C.int(b2i(green)), diffPtr, C.int(len(cDiffs)),
		C.double(r.X), C.double(r.Y), C.double(r.W), C.double(r.H),
		&w, &h, &eh, &ew, &zhh, &zhw)
	enH, enW = float64(eh), float64(ew)
	zhH, zhW = float64(zhh), float64(zhw)
	bubbleW = float64(w)
	activeTab = opts.Active
	layoutTabs(opts.Visible)
	layoutButtons()
	layoutRowButtons(opts.EnTitle, opts.ZhTitle)
}

// b2i 将布尔转为 C 侧 int 标志。
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// layoutTabs 在内容标签上方摆放 tab 栏：控制可见性与选中高亮。
func layoutTabs(visible int) {
	y := btnPadY + btnRowH + btnGap + zhH + rowGap2 + enH + tabGap
	x := btnPadX
	for i, tb := range tabList {
		mask := 1 << i
		vis := visible&mask != 0
		tb.SetHidden(!vis)
		tb.SetState(activeTab == mask)
		if vis {
			tb.SetFrame(x, y, tabWidths[i], tabH)
			x += tabWidths[i] + tabGap
		}
	}
	// 关闭按钮固定右上角，与 tab 行同高（x 由气泡实际宽度推算）。
	btnClose.SetFrame(bubbleW-btnPadX-closeW, y, closeW, tabH)
}

// layoutButtons 按 btnWidths 在底部一行摆放按钮（间距 btnGap）。
func layoutButtons() {
	x := btnPadX
	for i, b := range btnList {
		b.SetFrame(x, btnPadY, btnWidths[i], btnRowH)
		x += btnWidths[i] + btnGap
	}
}

// layoutRowButtons 摆放行内按钮：紧跟各文本行右侧、垂直居中，标题为空则隐藏。
func layoutRowButtons(enTitle, zhTitle string) {
	zhY := btnPadY + btnRowH + btnGap
	enY := zhY + zhH + rowGap2
	btnApplyEn.SetTitle(enTitle)
	btnApplyEn.SetHidden(enTitle == "")
	btnApplyEn.SetFrame(btnPadX+enW+rowGap2, enY+(enH-btnRowH)/2, rowBtnW, btnRowH)
	btnApplyZh.SetTitle(zhTitle)
	btnApplyZh.SetHidden(zhTitle == "")
	btnApplyZh.SetFrame(btnPadX+zhW+rowGap2, zhY+(zhH-btnRowH)/2, rowBtnW, btnRowH)
}

// SetRowButtonTitle 更新行内按钮标题（剪贴板场景 Copy/Copied 动态切换）。
// which: 0=英文行，1=中文行。必须在主线程调用。
func SetRowButtonTitle(which int, title string) {
	b := btnApplyEn
	if which == 1 {
		b = btnApplyZh
	}
	if title == "" {
		b.SetHidden(true)
		return
	}
	b.SetTitle(title)
	b.SetHidden(false)
}

// SetActiveTab 更新选中 tab 的高亮（不改变显示内容）。必须在主线程调用。
func SetActiveTab(t int) {
	activeTab = t
	for i, tb := range tabList {
		tb.SetState(t == 1<<i)
	}
}

// ActiveTab 返回当前选中 tab。必须在主线程调用。
func ActiveTab() int {
	return activeTab
}

// Hide 隐藏气泡（见 bubble_common.go）。必须在主线程调用。
func setAlpha(a float64) {
	C.bubble_set_alpha(C.double(a))
}

// hideWindow 隐藏气泡（见 bubble_common.go）。必须在主线程调用。
func hideWindow() {
	C.bubble_hide()
}

// ConsumeMouseEvents 返回并清除鼠标进出事件。必须在主线程调用。
func ConsumeMouseEvents() MouseEvent {
	return MouseEvent(C.bubble_mouse_flags())
}

// MouseInside 返回鼠标当前是否位于气泡内。必须在主线程调用。
func MouseInside() bool {
	return C.bubble_mouse_inside() != 0
}

// Visible 返回气泡当前是否可见。必须在主线程调用。
func Visible() bool {
	return C.bubble_visible() != 0
}
