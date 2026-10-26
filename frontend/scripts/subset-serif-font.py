#!/usr/bin/env python3
"""Build the tiny serif heading font (Noto Serif SC, SIL OFL 1.1).

The UI sets its fixed Chinese copy in a serif CJK face, but no good serif
ships on Windows and a full CJK web font is megabytes. So we ship only the
glyphs the source actually contains: every CJK character found in src/
(plus a few extras), one small woff2 per weight.

Usage (needs `pip install fonttools brotli`):
    npm pack @fontsource/noto-serif-sc@5.3.1 && tar xzf fontsource-noto-serif-sc-5.3.1.tgz
    python3 scripts/subset-serif-font.py package

Rerun after changing UI copy; characters outside the subset fall back to
the system serif stack. User-supplied text (nicks, room names) stays sans.
The glyph list is also written to src/assets/fonts/serif-subset.chars.txt;
src/lib/fontSubset.test.ts fails when the source uses a character missing
from it, so a forgotten rerun shows up in CI instead of in production.
"""
import re
import sys
import tempfile
from pathlib import Path

from fontTools import subset
from fontTools.merge import Merger
from fontTools.ttLib import TTFont

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "src"
EXTRA = "壹贰叁肆伍陆柒捌玖拾零〇，。、：；！？「」『』《》（）·—…"
WEIGHTS = (600, 900)
OUT_DIR = SRC / "assets" / "fonts"


def source_chars() -> str:
    """Every CJK character in the UI source (tests excluded) plus EXTRA."""
    found: set[str] = set(EXTRA)
    for path in SRC.rglob("*.ts*"):
        if ".test." in path.name:
            continue
        found.update(re.findall(r"[\u3000-\u303f\u4e00-\u9fff\uff00-\uffef]", path.read_text(encoding="utf-8")))
    return "".join(sorted(found))


CHARS = source_chars()

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
    print(f"{len(CHARS)} characters")
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    (OUT_DIR / "serif-subset.chars.txt").write_text(CHARS + "\n", encoding="utf-8")
    for w in WEIGHTS:
        out = build(Path(sys.argv[1]), w)
        print(f"{out.relative_to(Path.cwd()) if out.is_relative_to(Path.cwd()) else out}: {out.stat().st_size} bytes")
