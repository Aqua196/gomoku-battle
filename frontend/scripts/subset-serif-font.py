#!/usr/bin/env python3
"""Build the tiny serif heading font (Noto Serif SC, SIL OFL 1.1).

Headings use a serif CJK face, but no common serif ships on Windows, and a
full CJK web font is megabytes. Every serif string in the UI is fixed copy,
so we ship only those glyphs: one ~20 KB woff2 per weight.

Usage (needs `pip install fonttools brotli`):
    npm pack @fontsource/noto-serif-sc@5.3.1 && tar xzf fontsource-noto-serif-sc-5.3.1.tgz
    python3 scripts/subset-serif-font.py package

Rerun after adding serif copy: anything missing from CHARS falls back to
the system serif stack. User-supplied text (nicks, room names) stays sans.
"""
import re
import sys
import tempfile
from pathlib import Path

from fontTools import subset
from fontTools.merge import Merger
from fontTools.ttLib import TTFont

CHARS = (
    "五子棋连珠一定乾坤"            # brand + landing title
    "快速匹配人机对战创建房间"      # mode cards, room list
    "对局你赢了对手获胜平双方弃"    # sidebar + result titles
    "胜负和"                        # result seals
    "夜深早上中午下晚好"            # lobby greeting
    "，！"
)
WEIGHTS = (700, 900)
OUT_DIR = Path(__file__).resolve().parent.parent / "src" / "assets" / "fonts"

FACE = re.compile(r"src: url\(\./files/([^)]+\.woff2)\).*?unicode-range: ([^;]+);", re.S)


def ranges(spec: str):
    for part in spec.split(","):
        lo, _, hi = part.strip().removeprefix("U+").partition("-")
        yield int(lo, 16), int(hi or lo, 16)


def chunks_for(pkg: Path, weight: int, needed: set[int]):
    """Map each needed code point to the fontsource chunk that contains it."""
    css = (pkg / f"{weight}.css").read_text()
    picked: dict[str, set[int]] = {}
    for file, spec in FACE.findall(css):
        spans = list(ranges(spec))
        hit = {cp for cp in needed if any(lo <= cp <= hi for lo, hi in spans)}
        if hit:
            picked.setdefault(file, set()).update(hit)
    missing = needed - set().union(*picked.values())
    if missing:
        sys.exit(f"no chunk covers: {''.join(map(chr, sorted(missing)))}")
    return picked


def build(pkg: Path, weight: int) -> Path:
    needed = {ord(c) for c in CHARS}
    parts = []
    with tempfile.TemporaryDirectory() as tmp:
        for i, (file, cps) in enumerate(sorted(chunks_for(pkg, weight, needed).items())):
            font = TTFont(pkg / "files" / file)
            opts = subset.Options()
            opts.layout_features = ["kern", "palt"]
            opts.name_IDs = ["*"]
            sub = subset.Subsetter(opts)
            sub.populate(unicodes=cps)
            sub.subset(font)
            font.flavor = None
            part = Path(tmp) / f"{i}.otf"
            font.save(part)
            parts.append(str(part))
        merged = Merger().merge(parts)
        merged.flavor = "woff2"
        OUT_DIR.mkdir(parents=True, exist_ok=True)
        out = OUT_DIR / f"noto-serif-sc-{weight}.subset.woff2"
        merged.save(out)
    return out


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    for w in WEIGHTS:
        out = build(Path(sys.argv[1]), w)
        print(f"{out.relative_to(Path.cwd()) if out.is_relative_to(Path.cwd()) else out}: {out.stat().st_size} bytes")
