//go:build windows

// axdiag 临时诊断：逐步骤打印 UIA 初始化 HRESULT 与焦点元素采样，
// 定位 Windows 上 GetFocusedElement 持续失败的原因。
// 用法：cmd /c axdiag.exe > axdiag.log 2>&1
// 运行 15 秒内保持焦点在记事本/浏览器并选中文字，采样 30 次 × 500ms。
package main

/*
#cgo LDFLAGS: -lole32 -loleaut32 -luser32

#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0601
#endif
#include <windows.h>
#include <ole2.h>
#include <uiautomationclient.h>
#include <stdio.h>

static const GUID DG_CLSID_CUIAutomation =
    {0xff48dba4, 0x60ef, 0x4201, {0xaa, 0x87, 0x54, 0x10, 0x3e, 0xef, 0x59, 0x4e}};
static const GUID DG_IID_IUIAutomation =
    {0x30cbe57d, 0xd9d0, 0x452a, {0xab, 0x13, 0x7a, 0xc5, 0xac, 0x48, 0x25, 0xee}};
static const GUID DG_IID_IUnknown =
    {0x00000000, 0x0000, 0x0000, {0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}};
static const GUID DG_IID_IClassFactory =
    {0x00000001, 0x0000, 0x0000, {0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}};
static const GUID DG_IID_IUIAutomationTextPattern =
    {0x32eba289, 0x3583, 0x42c9, {0x9c, 0x59, 0x3b, 0x6d, 0x9a, 0x1e, 0x9a, 0x6a}};

// diag_manual: 绕过 CoCreateInstance 手动走 LoadLibrary→DllGetClassObject
// →CreateInstance(IUnknown)→QI(IUIAutomation)，逐步骤打印 HRESULT，
// 定位 E_NOINTERFACE 出现在哪一步。path 指定被测 dll（system32 或 WinSxS
// 旧版本，对比是否系统更新引入回归）。
static void diag_manual(const wchar_t *path) {
    HMODULE hm = LoadLibraryW(path);
    printf("LoadLibrary %ls hm=%p", path, (void *)hm);
    if (hm == NULL) {
        printf(" err=%lu\n", GetLastError());
        return;
    }
    printf("\n");
    typedef HRESULT (WINAPI *DllGetClassObject_t)(REFCLSID, REFIID, LPVOID *);
    DllGetClassObject_t pfn = (DllGetClassObject_t)(void *)GetProcAddress(hm, "DllGetClassObject");
    printf("GetProcAddress DllGetClassObject=%p", (void *)pfn);
    if (pfn == NULL) {
        printf(" err=%lu\n", GetLastError());
        return;
    }
    printf("\n");
    IClassFactory *cf = NULL;
    HRESULT hr = pfn(&DG_CLSID_CUIAutomation, &DG_IID_IClassFactory, (void **)&cf);
    printf("DllGetClassObject hr=0x%08lX cf=%p\n", (unsigned long)hr, (void *)cf);
    if (hr != S_OK || cf == NULL) return;
    IUnknown *unk = NULL;
    hr = cf->lpVtbl->CreateInstance(cf, NULL, &DG_IID_IUnknown, (void **)&unk);
    printf("CreateInstance(IUnknown) hr=0x%08lX unk=%p\n", (unsigned long)hr, (void *)unk);
    if (unk != NULL) {
        IUIAutomation *au = NULL;
        hr = unk->lpVtbl->QueryInterface(unk, &DG_IID_IUIAutomation, (void **)&au);
        printf("QI(IUIAutomation) hr=0x%08lX au=%p\n", (unsigned long)hr, (void *)au);
        if (au) au->lpVtbl->Release(au);
        unk->lpVtbl->Release(unk);
    }
    cf->lpVtbl->Release(cf);
}

static void diag_print_elem(const char *tag, IUIAutomationElement *e) {
    int ct = 0;
    BSTR name = NULL;
    e->lpVtbl->get_CurrentControlType(e, &ct);
    e->lpVtbl->get_CurrentName(e, &name);
    printf("%s: ctype=%d name=%ls\n", tag, ct, name ? name : L"(null)");
    if (name) SysFreeString(name);
}

static void diag_once(IUIAutomation *au, int n) {
    IUIAutomationElement *e = NULL;
    HRESULT hr = au->lpVtbl->GetFocusedElement(au, &e);
    if (hr != S_OK || e == NULL) {
        printf("sample %d: GetFocusedElement hr=0x%08lX\n", n, (unsigned long)hr);
        return;
    }
    int ct = 0;
    e->lpVtbl->get_CurrentControlType(e, &ct);
    printf("sample %d: ctype=%d", n, ct);
    // TextPattern 支持性
    IUIAutomationTextPattern *tp = NULL;
    HRESULT htp = e->lpVtbl->GetCurrentPatternAs(e, UIA_TextPatternId,
        &DG_IID_IUIAutomationTextPattern, (void **)&tp);
    printf(" tp=0x%08lX", (unsigned long)htp);
    if (tp) tp->lpVtbl->Release(tp);
    // 原生 HWND 与窗口类名 + EM_GETSEL 选区
    UIA_HWND hwnd = NULL;
    HRESULT hhw = e->lpVtbl->get_CurrentNativeWindowHandle(e, &hwnd);
    printf(" hwnd=0x%llX(hr=0x%08lX)", (unsigned long long)(ULONG_PTR)hwnd, (unsigned long)hhw);
    if (hwnd != NULL) {
        wchar_t cls[64] = L"?";
        GetClassNameW((HWND)hwnd, cls, 64);
        printf(" class=%ls", cls);
        int total = GetWindowTextLengthW((HWND)hwnd);
        printf(" len=%d", total);
        DWORD_PTR start = 0, end = 0;
        LRESULT sr = 0;
        BOOL ok = SendMessageTimeoutW((HWND)hwnd, EM_GETSEL, (WPARAM)&start, (LPARAM)&end,
            SMTO_ABORTIFHUNG, 300, &sr);
        printf(" EM_GETSEL ok=%d sr=%lld sel=[%lld,%lld)", (int)ok, (long long)sr,
            (long long)start, (long long)end);
    }
    // 前台窗口标题（判断是哪个应用）
    HWND fg = GetForegroundWindow();
    if (fg != NULL) {
        wchar_t title[128] = L"";
        GetWindowTextW(fg, title, 128);
        printf(" fg=%ls", title);
    }
    printf("\n");
    e->lpVtbl->Release(e);
}

static int diag_main(void) {
    // CRT 层把 stdout/stderr 直接绑到日志文件：schtasks 启动时无 shell
    // 重定向可用，freopen 保证所有 printf 输出落盘。
    // 注意：c:\ 根目录对普通用户只读，必须写用户目录。
    FILE *lf = freopen("C:\\Users\\vboxuser\\Library\\Logs\\axdiag.log", "a", stdout);
    if (lf != NULL) setvbuf(lf, NULL, _IONBF, 0);
    freopen("C:\\Users\\vboxuser\\Library\\Logs\\axdiag.log", "a", stderr);
    HRESULT hr = CoInitializeEx(NULL, COINIT_APARTMENTTHREADED);
    printf("CoInitializeEx hr=0x%08lX\n", (unsigned long)hr);
    IUIAutomation *au = NULL;
    hr = CoCreateInstance(&DG_CLSID_CUIAutomation, NULL, CLSCTX_INPROC_SERVER,
        &DG_IID_IUIAutomation, (void **)&au);
    printf("CoCreateInstance hr=0x%08lX au=%p\n", (unsigned long)hr, (void *)au);
    diag_manual(L"uiautomationcore.dll");
    diag_manual(L"C:\\Windows\\WinSxS\\amd64_microsoft-windows-uiautomationcore_31bf3856ad364e35_10.0.19041.2788_none_24aa2c72f7742985\\UIAutomationCore.dll");
    if (hr != S_OK || au == NULL) return 1;
    int i;
    for (i = 0; i < 90; i++) {
        diag_once(au, i);
        fflush(stdout);
        Sleep(500);
    }
    au->lpVtbl->Release(au);
    CoUninitialize();
    return 0;
}
*/
import "C"

func main() {
	C.diag_main()
}
