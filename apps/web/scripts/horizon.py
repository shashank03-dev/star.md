#!/usr/bin/env python3
"""Generate the hero's layered horizon from a single source image.

Builds two alpha-cutout WebPs used by the landing hero (see Landing.tsx / the
`.horizon-far` / `.horizon-near` rules in landing.css):

  horizon-far.webp   distant ridge + atmospheric glow; sky above the glow is
                     transparent (star canvas shows through). Sits BEHIND the
                     demo window.
  horizon-near.webp  foreground terrain; sky above the near ridge is
                     transparent. Paints in FRONT of the window and occludes
                     its lower edge (the "cutout").

The ridge line is detected per column from image luminance, so the transparency
follows the real silhouette instead of a flat gradient mask.

Run:  python3 scripts/horizon.py     (from apps/web)
Deps: pillow, numpy
"""
import os
import numpy as np
from PIL import Image, ImageFilter

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.join(HERE, "horizon-source.jpg")
OUT = os.path.join(HERE, "..", "public")
W = 1920  # target web width

im = Image.open(SRC).convert("RGB")
ow, oh = im.size
H = round(oh * W / ow)
im = im.resize((W, H), Image.LANCZOS)
arr = np.asarray(im).astype(np.float32)
lum = 0.2126 * arr[:, :, 0] + 0.7152 * arr[:, :, 1] + 0.0722 * arr[:, :, 2]


def smooth(line, k):
    pad = np.pad(line.astype(np.float32), (k, k), mode="edge")
    return np.convolve(pad, np.ones(2 * k + 1) / (2 * k + 1), mode="same")[k:-k]


def alpha_from_ridge(ridge, feather):
    yy = np.arange(H)[:, None]
    a = np.clip((yy - ridge[None, :]) / feather, 0.0, 1.0) * 255.0
    return Image.fromarray(a.astype(np.uint8), "L").filter(ImageFilter.GaussianBlur(1.1))


RUN = 6

# NEAR: foreground dunes. Scan up from the bottom to the first sustained bright
# run (mist above the nearest ridge); clamp to the lower band so we never grab
# the tall distant mountains and the very bottom stays solid ground.
bright = lum > 60.0
near_ridge = np.zeros(W, dtype=np.float32)
for x in range(W):
    col = bright[:, x]
    r = H - 1
    while r > 0:
        if col[r] and col[max(0, r - RUN):r].mean() > 0.7:
            break
        r -= 1
    near_ridge[x] = r
# Clamp to the lower band (never the tall distant peaks) and cap the per-column
# rise against a heavily-smoothed baseline: a lone bright column (e.g. the
# snowy right-edge peak) can otherwise spike the ridge and punch a hard-edged
# opaque block into the cutout. Keep each column within `slope` px of the
# baseline, then smooth the whole silhouette so it reads as one dune line.
near_ridge = np.clip(near_ridge, H * 0.60, H * 0.74)
baseline = smooth(near_ridge, 60)
slope = H * 0.06
near_ridge = np.clip(near_ridge, baseline - slope, baseline + slope)
near_ridge = smooth(near_ridge, 12)
near = im.copy()
near.putalpha(alpha_from_ridge(near_ridge, 14.0))
near.save(os.path.join(OUT, "horizon-near.webp"), "WEBP", quality=88, method=6)

# FAR: glow + distant mountains; transparent above the glow top (scan DOWN from
# the top to the first sustained bright run).
glow = lum > 30.0
far_ridge = np.zeros(W, dtype=np.float32)
for x in range(W):
    col = glow[:, x]
    r = 0
    while r < H - 1:
        if col[r] and col[r:min(H, r + RUN)].mean() > 0.7:
            break
        r += 1
    far_ridge[x] = r
far_ridge = smooth(np.clip(far_ridge, H * 0.20, H * 0.62), 14)
# The source bakes in vertical aurora/godray columns: once the sky is cut out
# they composite as uneven, blotchy blue patches ("the behind light is not
# good"). Dissolve them with a strong horizontal-only blur (downsample X, keep
# Y) so the whole horizon reads as one continuous atmospheric glow; distant
# ridges soften into haze, which correctly sells depth on the FAR layer. The
# NEAR layer stays sharp for foreground crispness.
kx = 14
far = im.resize((max(1, W // kx), H), Image.LANCZOS).resize((W, H), Image.BILINEAR)
far.putalpha(alpha_from_ridge(far_ridge, 46.0))
far.save(os.path.join(OUT, "horizon-far.webp"), "WEBP", quality=86, method=6)

kb = lambda p: round(os.path.getsize(p) / 1024)
print("near", kb(os.path.join(OUT, "horizon-near.webp")), "KB   "
      "far", kb(os.path.join(OUT, "horizon-far.webp")), "KB")
