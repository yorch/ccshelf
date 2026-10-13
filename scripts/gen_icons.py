#!/usr/bin/env python3
"""Draw site/assets/favicon.svg as PNG files: apple-touch-icon.png (180) and favicon.ico (32, PNG inside).

Python 3 standard library only. Run: python3 -I scripts/gen_icons.py
The shapes below must match site/assets/favicon.svg (a 28 x 28 viewBox).
"""
import os
import struct
import zlib

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "site", "assets")
BG, TEAL, GOLD, INK, BOARD = (0xEE, 0xF0, 0xEC), (0x0C, 0x5A, 0x5E), (0xF4, 0xC5, 0x42), (0x17, 0x20, 0x1C), (0xA9, 0x93, 0x70)
# (x, y, w, h, radius, colour), drawn in order; the gold bar gets an ink outline of width 1.2
SHAPES = [
    (0, 0, 28, 28, 5, BG),
    (3, 9, 5, 11, 1, TEAL),
    (9.4, 3.4, 6.2, 17.2, 1.6, INK),
    (10.6, 4.6, 3.8, 14.8, 0.4, GOLD),
    (17, 9, 5, 11, 1, TEAL),
    (1.5, 21.5, 25, 3, 1, BOARD),
]


def inside(px, py, x, y, w, h, r):
    cx = min(max(px, x + r), x + w - r)
    cy = min(max(py, y + r), y + h - r)
    return (px - cx) ** 2 + (py - cy) ** 2 <= r * r and x <= px <= x + w and y <= py <= y + h


def render(size, ss=4):
    rows = []
    for j in range(size):
        row = bytearray([0])
        for i in range(size):
            acc = [0, 0, 0, 0]
            for sj in range(ss):
                for si in range(ss):
                    px, py = (i + (si + 0.5) / ss) * 28 / size, (j + (sj + 0.5) / ss) * 28 / size
                    col = None
                    for x, y, w, h, r, c in SHAPES:
                        if inside(px, py, x, y, w, h, r):
                            col = c
                    if col:
                        acc = [acc[0] + col[0], acc[1] + col[1], acc[2] + col[2], acc[3] + 1]
            n = ss * ss
            a = acc[3]
            row += bytes([acc[0] // a, acc[1] // a, acc[2] // a, 255 * a // n] if a else [0, 0, 0, 0])
        rows.append(bytes(row))
    return png(size, b"".join(rows))


def png(size, raw):
    def chunk(t, d):
        c = struct.pack(">I", len(d)) + t + d
        return c + struct.pack(">I", zlib.crc32(t + d) & 0xFFFFFFFF)
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b""))


def main():
    with open(os.path.join(OUT, "apple-touch-icon.png"), "wb") as f:
        f.write(render(180))
    img = render(32)
    ico = struct.pack("<HHH", 0, 1, 1) + struct.pack("<BBBBHHII", 32, 32, 0, 0, 1, 32, len(img), 22) + img
    with open(os.path.join(OUT, "favicon.ico"), "wb") as f:
        f.write(ico)


if __name__ == "__main__":
    main()
