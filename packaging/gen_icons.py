#!/usr/bin/env python3
# gen_icons：从源图（带白底边距的整图）重新生成全部图标产物。
#
# 产物：
#   packaging/AppIcon-source-1024.png   首次运行时归档的原始源图（只读存档）
#   packaging/AppIcon-1024.png          裁掉白边、圆角外透明的主图（1024x1024）
#   packaging/AppIcon.iconset/*.png     macOS iconset 全 10 档尺寸
#   packaging/AppIcon.icns              iconutil 合成（供 .app bundle）
#   packaging/winres/elfin-grammar.ico  cmd/mkicon 合成（供 Windows exe 资源）
#   internal/appkit/status_icon_asset.go  状态栏/托盘图标内嵌素材（base64）
#   packaging/previews/                 目检用预览图（不参与构建）
#
# 状态栏图标取新主图的「精灵绿帽」剪影（--motif bubble 可换绿色对勾气泡）：
#   macOS  36x36 黑+alpha 模板图（@2x，代码里设逻辑尺寸 18x18）
#   Windows 32x32 白+alpha BGRA 像素流
#
# 用法：python3 packaging/gen_icons.py [--motif hat|bubble] [--skip-ico]
import argparse
import base64
import io
import subprocess
import sys
from collections import deque
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parent.parent
PKG = ROOT / "packaging"
ICONSET = PKG / "AppIcon.iconset"
PREVIEWS = PKG / "previews"
ASSET_GO = ROOT / "internal" / "appkit" / "status_icon_asset.go"

ICONSET_SIZES = {
    "icon_16x16.png": 16,
    "icon_16x16@2x.png": 32,
    "icon_32x32.png": 32,
    "icon_32x32@2x.png": 64,
    "icon_128x128.png": 128,
    "icon_128x128@2x.png": 256,
    "icon_256x256.png": 256,
    "icon_256x256@2x.png": 512,
    "icon_512x512.png": 512,
    "icon_512x512@2x.png": 1024,
}


def log(msg):
    print(f"[gen_icons] {msg}")


def to_image(arr: np.ndarray) -> Image.Image:
    """numpy 阵 -> PIL 图。

    注意：Pillow 11.3 的 Image.fromarray 产物 readonly=1，
    ImageDraw.floodfill 的像素写入会静默失效，必须用 frombytes 重建。"""
    arr = np.ascontiguousarray(arr)
    if arr.ndim == 3 and arr.shape[2] == 4:
        mode = "RGBA"
    elif arr.ndim == 3 and arr.shape[2] == 3:
        mode = "RGB"
    else:
        mode = "L"
    return Image.frombytes(mode, (arr.shape[1], arr.shape[0]), arr.tobytes())


# ---------------------------------------------------------------------------
# 主图：裁白边 + 圆角外透明
# ---------------------------------------------------------------------------
def crop_artwork(src: Image.Image) -> Image.Image:
    a = np.array(src.convert("RGBA"))
    # 背景候选：近白像素、已透明像素（保证可重入），以及 artwork 外围的
    # 中性灰柔影（chroma<=6；作品自身灰蓝/奶白描边 chroma>=13 不受影响）。
    mn = a[..., :3].min(axis=-1).astype(int)
    mx = a[..., :3].max(axis=-1).astype(int)
    bg_cand = (mn >= 240) | (a[..., 3] < 16) | ((mn >= 185) & (mx - mn <= 6))
    mask = to_image((bg_cand * 255).astype(np.uint8))
    # 从四角 flood fill 连通背景，避免误伤图内浅色区域（如底部纸色）。
    for xy in ((0, 0), (mask.width - 1, 0), (0, mask.height - 1),
               (mask.width - 1, mask.height - 1)):
        if mask.getpixel(xy) == 255:
            ImageDraw.floodfill(mask, xy, 128)
    bg = np.array(mask) == 128
    fg = ~bg
    ys, xs = np.where(fg)
    if xs.size == 0:
        raise SystemExit("源图未检测到前景，终止")
    box = (int(xs.min()), int(ys.min()), int(xs.max()) + 1, int(ys.max()) + 1)
    cropped = src.convert("RGBA").crop(box)
    ca = np.array(cropped)
    ca[bg[box[1]:box[3], box[0]:box[2]], 3] = 0  # 圆角外置透明
    cropped = to_image(ca)
    # 非正方形时透明补边成正方形，保持 artwork 自身宽高比不变形。
    w, h = cropped.size
    if w != h:
        side = max(w, h)
        canvas = Image.new("RGBA", (side, side), (0, 0, 0, 0))
        canvas.paste(cropped, ((side - w) // 2, (side - h) // 2), cropped)
        cropped = canvas
    log(f"裁剪 artwork: bbox={box} -> 正方形 {cropped.size[0]}x{cropped.size[1]}")
    return cropped


# ---------------------------------------------------------------------------
# 剪影：绿色连通域提取（帽 / 对勾气泡）
# ---------------------------------------------------------------------------
def green_mask(a: np.ndarray) -> np.ndarray:
    r = a[..., 0].astype(int)
    g = a[..., 1].astype(int)
    b = a[..., 2].astype(int)
    return (g > r + 12) & (g > b + 12) & (g > 70) & (a[..., 3] > 128)


def label_components(mask: np.ndarray):
    """BFS 连通域标注，返回 [(area, component_bool_array), ...] 降序。"""
    visited = np.zeros(mask.shape, bool)
    comps = []
    h, w = mask.shape
    for sy in range(h):
        for sx in range(w):
            if visited[sy, sx] or not mask[sy, sx]:
                continue
            comp = np.zeros(mask.shape, bool)
            q = deque([(sy, sx)])
            visited[sy, sx] = True
            while q:
                y, x = q.popleft()
                comp[y, x] = True
                for dy, dx in ((1, 0), (-1, 0), (0, 1), (0, -1)):
                    ny, nx = y + dy, x + dx
                    if 0 <= ny < h and 0 <= nx < w and not visited[ny, nx] \
                            and mask[ny, nx]:
                        visited[ny, nx] = True
                        q.append((ny, nx))
            comps.append((int(comp.sum()), comp))
    comps.sort(key=lambda t: t[0], reverse=True)
    return comps


def fill_holes(comp: np.ndarray) -> np.ndarray:
    # 在全尺寸图上填孔：「外部」= 与图像边界连通的 0 区。
    # 若先裁 bbox 再 flood，帽子凹口会被 bbox 边界围成假内孔而被填实。
    img = to_image((comp * 255).astype(np.uint8))
    ImageDraw.floodfill(img, (0, 0), 128)  # 与边界连通的 0 区染 128
    arr = np.array(img)
    holes = arr == 0  # 染不到的 0 区即真内孔（如帽上缎带）
    return comp | holes


def pick_motif(comps, shape, motif: str) -> np.ndarray:
    h, w = shape
    if motif == "hat":
        return fill_holes(comps[0][1])
    # bubble：面积占比小、近圆、位于右上区域的绿色连通域（不填孔，保留镂空对勾）。
    total = h * w
    for area, comp in comps:
        frac = area / total
        if not (0.0005 < frac < 0.03):
            continue
        ys, xs = np.where(comp)
        bw, bh = xs.max() - xs.min() + 1, ys.max() - ys.min() + 1
        cy, cx = ys.mean() / h, xs.mean() / w
        if 0.8 < bw / bh < 1.25 and cx > 0.55 and cy < 0.55:
            log(f"bubble 连通域: area={area} center=({cx:.2f},{cy:.2f})")
            return comp
    raise SystemExit("未找到 bubble 连通域")


def silhouette_assets(sil: np.ndarray):
    """剪影 bool 阵 -> (36x36 模板 alpha, 32x32 alpha)。"""
    ys, xs = np.where(sil)
    box = (int(xs.min()), int(ys.min()), int(xs.max()) + 1, int(ys.max()) + 1)
    sub = sil[box[1]:box[3], box[0]:box[2]]
    bw, bh = sub.shape[1], sub.shape[0]
    pad = int(max(bw, bh) * 0.06)
    side = max(bw, bh) + pad * 2
    canvas = np.zeros((side, side), bool)
    canvas[(side - bh) // 2:(side - bh) // 2 + bh,
           (side - bw) // 2:(side - bw) // 2 + bw] = sub
    img = to_image((canvas * 255).astype(np.uint8))
    a36 = img.resize((36, 36), Image.LANCZOS)
    a32 = img.resize((32, 32), Image.LANCZOS)
    return a36, a32


def build_status_png(alpha36: Image.Image) -> bytes:
    rgba = np.zeros((36, 36, 4), np.uint8)  # 模板图：纯黑 + alpha
    rgba[..., 3] = np.array(alpha36)
    buf = io.BytesIO()
    to_image(rgba).save(buf, "png", optimize=True)
    return buf.getvalue()


def build_status_bgra(alpha32: Image.Image) -> bytes:
    a = np.array(alpha32)
    out = np.zeros((32, 32, 4), np.uint8)  # BGRA：白色 + alpha
    out[..., 0] = 255
    out[..., 1] = 255
    out[..., 2] = 255
    out[..., 3] = a
    return out.tobytes()


# ---------------------------------------------------------------------------
# 产物写出
# ---------------------------------------------------------------------------
def write_asset_go(png: bytes, bgra: bytes, motif: str):
    def wrap(b64: str) -> str:
        lines = [b64[i:i + 76] for i in range(0, len(b64), 76)]
        tab = "\t"
        # Go 无 C 的邻接字符串隐式拼接，多行字面量须显式 + 连接。
        return (" +\n" + tab).join(f'"{l}"' for l in lines)

    png_lit = wrap(base64.b64encode(png).decode())
    bgra_lit = wrap(base64.b64encode(bgra).decode())
    content = f'''// Code generated by packaging/gen_icons.py (motif={motif}). DO NOT EDIT.
// 状态栏/托盘图标内嵌素材：源自主图剪影，避免运行时依赖外部资源文件。
package appkit

import "encoding/base64"

// statusIconPNGB64：macOS 状态栏模板图（36x36 黑+alpha，18pt @2x）。
const statusIconPNGB64 = {png_lit}

// statusIconBGRA32B64：Windows 托盘图标像素流（32x32 自顶向下 BGRA，白+alpha）。
const statusIconBGRA32B64 = {bgra_lit}

// statusIconPNG 解码 macOS 模板图 PNG 字节；解码失败返回 nil。
func statusIconPNG() []byte {{
	b, err := base64.StdEncoding.DecodeString(statusIconPNGB64)
	if err != nil {{
		return nil
	}}
	return b
}}

// statusIconBGRA32 解码 Windows 托盘 BGRA 像素流（须为 32*32*4 字节）。
func statusIconBGRA32() []byte {{
	b, err := base64.StdEncoding.DecodeString(statusIconBGRA32B64)
	if err != nil || len(b) != 32*32*4 {{
		return nil
	}}
	return b
}}
'''
    ASSET_GO.write_text(content, "utf-8")
    log(f"已生成 {ASSET_GO.relative_to(ROOT)}")


def composite_preview(alpha: Image.Image, bg: int, scale: int) -> Image.Image:
    """alpha 图放到灰底上放大，模拟状态栏目检效果。"""
    small = Image.new("RGBA", alpha.size, (bg, bg, bg, 255))
    small.paste(Image.new("RGBA", alpha.size, (0, 0, 0, 255)), (0, 0), alpha)
    return small.resize((alpha.width * scale, alpha.height * scale),
                        Image.NEAREST)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--motif", choices=("hat", "bubble"), default="hat")
    ap.add_argument("--skip-ico", action="store_true",
                    help="跳过 iconutil/go mkicon（仅生成 png 与素材）")
    args = ap.parse_args()

    source_path = PKG / "AppIcon-source-1024.png"
    master_path = PKG / "AppIcon-1024.png"
    if not source_path.exists():
        source_path.write_bytes(master_path.read_bytes())
        log(f"归档原始源图 -> {source_path.name}")
    src = Image.open(source_path)

    PREVIEWS.mkdir(exist_ok=True)
    ICONSET.mkdir(exist_ok=True)

    # 1) 主图与 iconset
    master = crop_artwork(src).resize((1024, 1024), Image.LANCZOS)
    master.save(master_path, "png", optimize=True)
    log(f"已生成 {master_path.name}")
    for name, size in ICONSET_SIZES.items():
        master.resize((size, size), Image.LANCZOS).save(ICONSET / name,
                                                        "png", optimize=True)
    log(f"已生成 iconset {len(ICONSET_SIZES)} 档")

    # 2) icns / ico
    if not args.skip_ico:
        subprocess.run(["iconutil", "-c", "icns", str(ICONSET),
                        "-o", str(PKG / "AppIcon.icns")], check=True)
        log("已生成 AppIcon.icns（iconutil）")
        subprocess.run(["go", "run", "./cmd/mkicon"], cwd=ROOT, check=True)

    # 3) 状态栏剪影素材
    arr = np.array(master)
    comps = label_components(green_mask(arr))
    log(f"绿色连通域 top3 面积: {[c[0] for c in comps[:3]]}")
    sil = pick_motif(comps, arr.shape[:2], args.motif)
    a36, a32 = silhouette_assets(sil)
    png = build_status_png(a36)
    bgra = build_status_bgra(a32)
    write_asset_go(png, bgra, args.motif)

    # 4) 预览
    master.resize((256, 256), Image.LANCZOS).save(
        PREVIEWS / "appicon_256.png", "png")
    to_image((np.array(sil) * 255).astype(np.uint8)).save(
        PREVIEWS / f"silhouette_{args.motif}_full.png", "png")
    a36.save(PREVIEWS / f"status_{args.motif}_36.png", "png")
    composite_preview(a36, 0xEE, 6).save(
        PREVIEWS / f"status_{args.motif}_18_on_light.png", "png")
    composite_preview(a36, 0x33, 6).save(
        PREVIEWS / f"status_{args.motif}_18_on_dark.png", "png")
    log(f"预览已写入 {PREVIEWS.relative_to(ROOT)}/")


if __name__ == "__main__":
    sys.exit(main())
