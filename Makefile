# elfin-grammar 打包 Makefile
#
# 常用目标：
#   make            # 默认：构建 .app 并打包成 .dmg（等同 make dmg）
#   make binary     # 仅编译原始可执行文件到 build/elfin-grammar
#   make app        # 编译并组装 build/ElfinGrammar.app
#   make dmg        # 依赖 app，生成 dist/elfin-grammar-<版本>.dmg
#   make run        # 直接运行编译好的二进制（开发调试用）
#   make sign       # 对 .app 做 ad-hoc 代码签名（首次运行/授权更稳定）
#   make windows      # 交叉编译 Windows x64 可执行文件到 dist/（免控制台窗口）
#   make windows-sign # 对 Windows exe 做 Authenticode 签名（SHA-256+时间戳）
#   make clean      # 清理 build/ 与 dist/
#   make help       # 显示本帮助
#
# 依赖：macOS 自带 go、hdiutil、codesign、iconutil（若提供图标）。
# windows 目标额外依赖 MinGW 工具链：brew install mingw-w64。

# 目录名不含空格（避免 Make 按空格拆分路径）；用户可见的显示名由 Info.plist 提供。
APP_NAME      := ElfinGrammar
APP_DISPLAY   := Elfin Grammar
BUNDLE_ID     := com.elfin.grammar
EXEC          := elfin-grammar
VERSION       := 1.0.0
BUILD_DIR     := build
DIST_DIR      := dist
APP_DIR       := $(BUILD_DIR)/$(APP_NAME).app
CONTENTS      := $(APP_DIR)/Contents
MACOS_DIR     := $(CONTENTS)/MacOS
RESOURCES_DIR := $(CONTENTS)/Resources
DMG           := $(DIST_DIR)/$(EXEC)-$(VERSION).dmg
ICON_SRC      := packaging/AppIcon.icns

# 本地工具链已启用 CGO（AppKit 桥接必需）。
export CGO_ENABLED := 1
GOFLAGS := -trimpath
LDFLAGS := -s -w -X main.version=$(VERSION)
# Windows 交叉编译：-H windowsgui 免控制台窗口。
WIN_LDFLAGS := -s -w -H windowsgui -X main.version=$(VERSION)
WIN_CC := x86_64-w64-mingw32-gcc

.PHONY: all dmg binary app sign windows windows-sign run clean help

all: dmg

# ---------------------------------------------------------------------------
# 显示帮助
# ---------------------------------------------------------------------------
help:
	@echo "可用目标："
	@echo "  make (all)   # 默认：构建 .app 并打包 .dmg（等同 make dmg）"
	@echo "  make binary  # 仅编译原始可执行文件到 build/elfin-grammar"
	@echo "  make app     # 编译并组装 build/ElfinGrammar.app"
	@echo "  make dmg     # 依赖 app，生成 dist/elfin-grammar-<版本>.dmg"
	@echo "  make sign    # 对 .app 做 ad-hoc 代码签名（首次运行/授权更稳定）"
	@echo "  make run     # 直接运行编译好的二进制（开发调试用）"
	@echo "  make windows      # 交叉编译 Windows x64 可执行文件到 dist/（免控制台窗口）"
	@echo "  make windows-sign # 对 Windows exe 做 Authenticode 签名（SHA-256+时间戳）"
	@echo "  make clean   # 清理 build/ 与 dist/"
	@echo "  make help    # 显示本帮助"

# ---------------------------------------------------------------------------
# 仅编译可执行文件
# ---------------------------------------------------------------------------
binary: $(BUILD_DIR)/$(EXEC)

$(BUILD_DIR)/$(EXEC): main.go runloop_darwin.go $(wildcard internal/*/*.go) go.mod
	@mkdir -p $(BUILD_DIR)
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ .

# ---------------------------------------------------------------------------
# 组装 .app bundle
# ---------------------------------------------------------------------------
app: $(APP_DIR)

$(APP_DIR): $(BUILD_DIR)/$(EXEC) packaging/Info.plist
	@rm -rf $(APP_DIR)
	@mkdir -p $(MACOS_DIR) $(RESOURCES_DIR)
	cp $(BUILD_DIR)/$(EXEC) $(MACOS_DIR)/$(EXEC)
	cp packaging/Info.plist $(CONTENTS)/Info.plist
	printf 'APPL????' > $(CONTENTS)/PkgInfo
	@if [ -f $(ICON_SRC) ]; then \
		cp $(ICON_SRC) $(RESOURCES_DIR)/AppIcon.icns; \
	else \
		echo "提示：未提供 $(ICON_SRC)，使用默认图标。"; \
	fi
	@echo "已组装 $(APP_DIR)"

# ---------------------------------------------------------------------------
# ad-hoc 代码签名：让 .app 拥有稳定的签名身份。
# 关键：--requirements 将 designated requirement 固定为纯 identifier（默认
# 会锚定二进制 CDHash）。macOS 的辅助功能(Accessibility)授权绑定 DR，
# 默认锚定 CDHash 时每次重新打包(二进制变化)都会导致授权失效、要求重新授权；
# 固定为 identifier 后，同一 bundle ID 重打包重装，授权可跨构建保留。
# ---------------------------------------------------------------------------
sign: $(APP_DIR)
	codesign --force --deep --sign - \
		--identifier $(BUNDLE_ID) \
		--requirements '=designated => identifier "$(BUNDLE_ID)"' \
		$(APP_DIR)
	@echo "ad-hoc 签名完成"

# ---------------------------------------------------------------------------
# 打包 .dmg：.app + /Applications 软链接，拖拽即安装
# ---------------------------------------------------------------------------
dmg: sign $(DMG)

$(DMG): $(APP_DIR)
	@mkdir -p $(DIST_DIR)
	@rm -rf $(BUILD_DIR)/dmg-staging
	@mkdir -p $(BUILD_DIR)/dmg-staging
	cp -R $(APP_DIR) $(BUILD_DIR)/dmg-staging/
	ln -s /Applications $(BUILD_DIR)/dmg-staging/Applications
	hdiutil create \
		-volname "$(APP_DISPLAY)" \
		-srcfolder $(BUILD_DIR)/dmg-staging \
		-ov -format UDZO \
		-o $(DMG)
	@rm -rf $(BUILD_DIR)/dmg-staging
	@echo "已生成 $(DMG)"

# ---------------------------------------------------------------------------
# 直接运行（开发调试）
# ---------------------------------------------------------------------------
run: binary
	./$(BUILD_DIR)/$(EXEC)

# ---------------------------------------------------------------------------
# Windows x64 交叉编译（UI Automation + Win32 桥接，无需 macOS 授权概念）
# ---------------------------------------------------------------------------
windows: $(DIST_DIR)/$(EXEC)-$(VERSION).exe

# 资源对象：visual styles 清单 + exe 图标，windres 编译后放主包目录
# （_windows_amd64 后缀使其仅参与 Windows 构建，macOS 构建自动忽略）。
RES_SYSO := resource_windows_amd64.syso

$(RES_SYSO): packaging/winres/elfin-grammar.rc packaging/winres/elfin-grammar.exe.manifest packaging/AppIcon-1024.png
	go run ./cmd/mkicon
	x86_64-w64-mingw32-windres packaging/winres/elfin-grammar.rc -O coff -o $@

$(DIST_DIR)/$(EXEC)-$(VERSION).exe: main.go runloop_windows.go $(wildcard internal/*/*.go) go.mod $(RES_SYSO)
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=$(WIN_CC) \
		go build $(GOFLAGS) -ldflags "$(WIN_LDFLAGS)" -o $@ .
	@echo "已生成 $@（Windows x64，双击运行；含视觉样式清单与 exe 图标）"

# ---------------------------------------------------------------------------
# Windows 签名：jsign 对 exe 做 Authenticode 签名（SHA-256 + RFC3161 时间戳）。
# 依赖 Java（jsign 为纯 Java 实现，macOS 无需 osslsigncode/signtool）。
# 当前使用自签证书（packaging/signing/，已 gitignore）——仅用于本地验证/测试；
# 对外分发前请替换为正式代码签名证书导出的 PKCS#12 并更新 P12/P12_PASS。
# ---------------------------------------------------------------------------
SIGN_DIR  := packaging/signing
JSIGN     := $(SIGN_DIR)/jsign-7.6.jar
P12       := $(SIGN_DIR)/elfin_code.p12
P12_PASS  := elfin-dev
TSA_URL   := http://timestamp.sectigo.com

windows-sign: $(DIST_DIR)/$(EXEC)-$(VERSION).exe
	java -jar $(JSIGN) --replace --storetype PKCS12 --keystore $(P12) \
		--storepass $(P12_PASS) --alg SHA-256 --tsaurl $(TSA_URL) \
		--tsretries 2 --tsretrywait 3 $<
	@echo "已签名 $<（SHA-256 + RFC3161 时间戳）"

clean:
	rm -rf $(BUILD_DIR) $(DIST_DIR)
