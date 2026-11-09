// mkicon 把 packaging/AppIcon-1024.png 包装为 PNG-in-ICO 容器
// （ICO 256x256 条目可直接内嵌 PNG 数据，Vista+ 支持），
// 供 windres 编译进 Windows exe 资源。纯标准库实现，无第三方依赖。
//
// 用法：go run ./cmd/mkicon
// 输入：packaging/AppIcon-1024.png；输出：packaging/winres/elfin-grammar.ico
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

func main() {
	root, err := findRoot()
	if err != nil {
		log.Fatal(err)
	}
	src := filepath.Join(root, "packaging", "AppIcon-1024.png")
	png, err := os.ReadFile(src)
	if err != nil {
		log.Fatalf("读取 %s 失败: %v", src, err)
	}
	if len(png) < 8 {
		log.Fatal("PNG 数据异常")
	}
	// 解析 IHDR 取宽高（PNG 前 24 字节固定含 IHDR 尺寸字段）。
	w := int(binary.BigEndian.Uint32(png[16:20]))
	h := int(binary.BigEndian.Uint32(png[20:24]))
	if w != h {
		log.Fatalf("图标必须为正方形，实际 %dx%d", w, h)
	}

	// ICO 容器：ICONDIR + 1 个 ICONDIRENTRY + PNG 数据本体。
	out := make([]byte, 0, 6+16+len(png))
	// ICONDIR：保留字 0、类型 1（图标）、图像数 1。
	out = append(out, 0, 0, 1, 0, 1, 0)
	// ICONDIRENTRY：宽/高（256 记 0）、调色板 0、保留 0、颜色平面 1、
	// 位深 32、数据长度、数据偏移（6+16=22）。
	var dim byte
	if w >= 256 {
		dim = 0
	} else {
		dim = byte(w)
	}
	out = append(out, dim, dim, 0, 0, 1, 0, 32, 0)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(png)))
	out = append(out, lenBuf[:]...)
	binary.LittleEndian.PutUint32(lenBuf[:], 22)
	out = append(out, lenBuf[:]...)
	out = append(out, png...)

	dst := filepath.Join(root, "packaging", "winres", "elfin-grammar.ico")
	if err := os.WriteFile(dst, out, 0644); err != nil {
		log.Fatalf("写入 %s 失败: %v", dst, err)
	}
	fmt.Printf("已生成 %s（%dx%d）\n", dst, w, h)
}

// findRoot 返回 go.mod 所在目录（从当前工作目录向上查找）。
func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("未找到 go.mod，请在仓库目录下运行")
		}
		dir = parent
	}
}
