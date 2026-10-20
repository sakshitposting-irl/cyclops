"""Turn a failed-run.sh transcript into a terminal-style GIF.
Usage: render-gif.py TRANSCRIPT OUT.gif   (needs Pillow; text after '### START' is drawn)"""
import sys
from PIL import Image, ImageDraw, ImageFont

FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"
SIZE, COLS, ROWS, PAD = 14, 104, 30, 14
BG, FG, GREEN, RED, DIM, CMD = "#1e1e2e", "#cdd6f4", "#a6e3a1", "#f38ba8", "#7f849c", "#89b4fa"

raw = open(sys.argv[1]).read().split("### START\n", 1)[1].rstrip("\n").split("\n")
font = ImageFont.truetype(FONT, SIZE)
cw = font.getlength("M")
lh = SIZE + 6
W, H = int(cw * COLS) + 2 * PAD, lh * ROWS + 2 * PAD


def wrap(line):
    """Long lines wrap onto indented continuation rows instead of being cut off."""
    parts = [line[:COLS]]
    rest = line[COLS:]
    while rest:
        parts.append("  " + rest[: COLS - 2])
        rest = rest[COLS - 2 :]
    return parts


def colour(line):
    if line.startswith("$ "):
        return CMD
    if line.startswith("#"):
        return DIM
    if "True" in line and "REPORT" not in line:
        return GREEN
    if "False" in line or "LastRunFailed" in line or '"error"' in line:
        return RED
    return FG


def frame(shown):
    img = Image.new("RGB", (W, H), BG)
    d = ImageDraw.Draw(img)
    for i, line in enumerate(shown[-ROWS:]):
        d.text((PAD, PAD + i * lh), line, font=font, fill=colour(line))
    return img


frames, durations, shown = [], [], []
for line in raw:
    shown.extend(wrap(line))
    frames.append(frame(shown))
    durations.append(1400 if line.startswith("$ ") else 2600 if not line.startswith("#") else 1000)
durations[-1] = 4000
frames[0].save(sys.argv[2], save_all=True, append_images=frames[1:], duration=durations, loop=0, optimize=True)
print(f"wrote {sys.argv[2]}: {len(frames)} frames")
