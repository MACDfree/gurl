#!/usr/bin/env python3
"""把图标 PNG 圆角外的白底清成透明。

qlmanage 会把 SVG 合成到不透明白底上，圆角方形外面那圈因此是纯白；放到
深色工具栏里就是一块白砖。这里按圆角矩形算每个像素的覆盖率，圆角外一律
改涂背景色再乘覆盖率写进 alpha —— 直接置 0 会留下白色锯齿，所以颜色要一起换。

只用标准库：这台机器上没有 PIL，也没有 rsvg-convert / imagemagick。

用法: round-corners.py <input.png> [output.png]   # 省略 output 就地改写
"""
import math
import struct
import sys
import zlib

# 这两个常量必须与 build/icon.svg 里 rect 的 rx 和 fill 保持一致。
RX_RATIO = 116 / 512
BG = (14, 21, 31)
SUBSAMPLES = 4


def decode(path):
    data = open(path, "rb").read()
    if data[:8] != b"\x89PNG\r\n\x1a\n":
        raise SystemExit(f"{path}: 不是 PNG")
    pos, idat, ihdr = 8, bytearray(), None
    while pos < len(data):
        (length,) = struct.unpack(">I", data[pos:pos + 4])
        ctype = data[pos + 4:pos + 8]
        body = data[pos + 8:pos + 8 + length]
        if ctype == b"IHDR":
            ihdr = struct.unpack(">IIBBBBB", body)
        elif ctype == b"IDAT":
            idat += body
        pos += 12 + length
    w, h, depth, color, _, _, interlace = ihdr
    if (depth, color, interlace) != (8, 6, 0):
        raise SystemExit(
            f"{path}: 只处理 8bit RGBA 非隔行 PNG"
            f"（实际 depth={depth} color={color} interlace={interlace}）"
        )
    raw = zlib.decompress(bytes(idat))
    stride = w * 4
    rows, prev, pos = [], bytearray(stride), 0
    for _ in range(h):
        ftype = raw[pos]
        pos += 1
        line = bytearray(raw[pos:pos + stride])
        pos += stride
        if ftype == 1:
            for x in range(4, stride):
                line[x] = (line[x] + line[x - 4]) & 255
        elif ftype == 2:
            for x in range(stride):
                line[x] = (line[x] + prev[x]) & 255
        elif ftype == 3:
            for x in range(stride):
                left = line[x - 4] if x >= 4 else 0
                line[x] = (line[x] + ((left + prev[x]) >> 1)) & 255
        elif ftype == 4:
            for x in range(stride):
                a = line[x - 4] if x >= 4 else 0
                b = prev[x]
                c = prev[x - 4] if x >= 4 else 0
                p = a + b - c
                pa, pb, pc = abs(p - a), abs(p - b), abs(p - c)
                pred = a if (pa <= pb and pa <= pc) else (b if pb <= pc else c)
                line[x] = (line[x] + pred) & 255
        elif ftype != 0:
            raise SystemExit(f"{path}: 未知的行过滤类型 {ftype}")
        rows.append(line)
        prev = line
    return w, h, rows


def covered(px, py, w, h, r):
    """点 (px, py) 是否落在圆角矩形内；w == h 时就是正圆角方形。"""
    dx = px - min(max(px, r), w - r)
    dy = py - min(max(py, r), h - r)
    return dx * dx + dy * dy <= r * r


def coverage(x, y, w, h, r):
    step = 1.0 / SUBSAMPLES
    hits = 0
    for i in range(SUBSAMPLES):
        for j in range(SUBSAMPLES):
            if covered(x + (i + 0.5) * step, y + (j + 0.5) * step, w, h, r):
                hits += 1
    return hits / (SUBSAMPLES * SUBSAMPLES)


def encode(w, h, rows):
    raw = bytearray()
    for line in rows:
        raw.append(0)
        raw += line

    def chunk(ctype, body):
        return (struct.pack(">I", len(body)) + ctype + body
                + struct.pack(">I", zlib.crc32(ctype + body) & 0xFFFFFFFF))

    return (b"\x89PNG\r\n\x1a\n"
            + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 6, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(bytes(raw), 9))
            + chunk(b"IEND", b""))


def round_corners(src, dst):
    w, h, rows = decode(src)
    r = RX_RATIO * w
    # 圆角只影响四个角上的 r×r 方块，其余像素与正方形一致，不必逐个算。
    span = int(math.ceil(r))
    corners = ((0, 0), (w - span, 0), (0, h - span), (w - span, h - span))
    for x0, y0 in corners:
        for y in range(y0, y0 + span):
            line = rows[y]
            for x in range(x0, x0 + span):
                c = coverage(x, y, w, h, r)
                if c >= 1:
                    continue
                line[x * 4:x * 4 + 3] = bytes(BG)
                line[x * 4 + 3] = round(255 * c)
    open(dst, "wb").write(encode(w, h, rows))


def main():
    if len(sys.argv) < 2:
        raise SystemExit(__doc__)
    src = sys.argv[1]
    round_corners(src, sys.argv[2] if len(sys.argv) > 2 else src)


if __name__ == "__main__":
    main()
