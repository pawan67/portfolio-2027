#!/usr/bin/env python3
"""
Generates the brand mark exploration set: standalone SVGs, LinkedIn banners,
and the review page at design/brand/index.html.

Every mark is drawn on the same 64x64 grid with a 7-unit stroke, so they can be
compared honestly rather than by whoever got the most generous padding. Wordmarks
are outlined from the site's own Geist subset -- a logo cannot ship as live text,
and outlining here means the review page shows the real thing rather than a
browser's best guess.

    python3 -m venv .venv && .venv/bin/pip install fonttools brotli uharfbuzz
    .venv/bin/python design/brand/build.py            # round 1 -> index.html
    .venv/bin/python design/brand/build.py --block    # round 2 -> block.html
    .venv/bin/python design/brand/build.py --p67      # round 3 -> p67.html

Run from the repo root. Each round writes its own page plus a svg/ and
banners/ directory; the three pages share one scaffold and differ only in the
mark set and the argument they make.
"""

import base64
import io
import json
import sys
from pathlib import Path

from fontTools.ttLib import TTFont
from fontTools.varLib import instancer
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
import uharfbuzz as hb

ROOT = Path(__file__).resolve().parents[2]
FONT = ROOT / "web/public/fonts/geist-sans-v1.woff2"
OUT = ROOT / "design/brand"
UPM = 1000


# --------------------------------------------------------------------------
# Colour. The site declares everything in oklch; Figma, Illustrator and every
# raster exporter still want hex, so the palette is converted once here rather
# than eyeballed twice.
# --------------------------------------------------------------------------
def oklch_to_hex(L, C, h_deg):
    import math

    h = math.radians(h_deg)
    a, b = C * math.cos(h), C * math.sin(h)
    l_ = L + 0.3963377774 * a + 0.2158037573 * b
    m_ = L - 0.1055613458 * a - 0.0638541728 * b
    s_ = L - 0.0894841775 * a - 1.2914855480 * b
    l, m, s = l_ ** 3, m_ ** 3, s_ ** 3
    r = +4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s
    g = -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s
    bl = -0.0041960863 * l - 0.7034186147 * m + 1.7076147010 * s

    def enc(u):
        u = max(0.0, min(1.0, u))
        u = 12.92 * u if u <= 0.0031308 else 1.055 * u ** (1 / 2.4) - 0.055
        return round(max(0.0, min(1.0, u)) * 255)

    return "#%02x%02x%02x" % (enc(r), enc(g), enc(bl))


PALETTE = {
    # Straight from web/src/styles/global.css, so the mark and the site cannot drift.
    "bg":      oklch_to_hex(0.17, 0.006, 60),
    "surface": oklch_to_hex(0.21, 0.007, 60),
    "line":    oklch_to_hex(0.32, 0.008, 60),
    "text":    oklch_to_hex(0.94, 0.004, 80),
    "muted":   oklch_to_hex(0.66, 0.008, 70),
    "accent":  oklch_to_hex(0.80, 0.13, 75),
    # Paper. The dark accent is a darkened cut of the same hue: the light one
    # sits at 1.9:1 on paper, which is a tint, not a mark.
    "paper":       oklch_to_hex(0.97, 0.004, 80),
    "ink":         oklch_to_hex(0.19, 0.007, 60),
    "ink_muted":   oklch_to_hex(0.50, 0.008, 70),
    "ink_line":    oklch_to_hex(0.86, 0.005, 70),
    "accent_ink":  oklch_to_hex(0.58, 0.13, 62),
}


# --------------------------------------------------------------------------
# Type. Shaped through HarfBuzz so kerning is the font's, not a guess -- "Ta"
# in "Pawan Tamada" is a real pair and it shows at banner size.
# --------------------------------------------------------------------------
_instances = {}


def _instance(weight):
    if weight not in _instances:
        var = TTFont(FONT)
        static = instancer.instantiateVariableFont(var, {"wght": weight}, inplace=False)
        # Loaded from woff2, so save() would re-compress to woff2 and HarfBuzz
        # would hand back an empty face -- every glyph silently .notdef.
        static.flavor = None
        buf = io.BytesIO()
        static.save(buf)
        data = buf.getvalue()
        hb_font = hb.Font(hb.Face(hb.Blob(data)))
        hb_font.scale = (UPM, UPM)
        _instances[weight] = (static, static.getGlyphSet(), static.getGlyphOrder(), hb_font)
    return _instances[weight]


def outline(text, weight=500, tracking=0.0):
    """Return (path_d, advance) in em units (1.0 == font size), y-down, baseline at 0."""
    _, glyphset, order, hb_font = _instance(weight)
    buf = hb.Buffer()
    buf.add_str(text)
    buf.guess_segment_properties()
    hb.shape(hb_font, buf)

    parts, x = [], 0.0
    track = tracking * UPM
    for info, pos in zip(buf.glyph_infos, buf.glyph_positions):
        name = order[info.codepoint]
        pen = SVGPathPen(glyphset, ntos=lambda v: f"{v:.1f}")
        # Flip y (font units are y-up, SVG is y-down) and place at the pen position.
        glyphset[name].draw(TransformPen(pen, (1, 0, 0, -1, x + pos.x_offset, -pos.y_offset)))
        d = pen.getCommands()
        if d:
            parts.append(d)
        x += pos.x_advance + track
    return "".join(parts), (x - track if text else 0) / UPM


def text_svg(text, size, x, y, fill, weight=500, tracking=0.0, anchor="start", opacity=None):
    """One <path> of outlined type, baseline at y, in px."""
    d, adv = outline(text, weight, tracking)
    if not d:
        return "", 0.0
    width = adv * size
    dx = x - width if anchor == "end" else x - width / 2 if anchor == "middle" else x
    s = size / UPM
    op = f' opacity="{opacity}"' if opacity else ""
    return (
        f'<g transform="translate({dx:.2f} {y:.2f}) scale({s:.6f})">'
        f'<path d="{d}" fill="{fill}"{op}/></g>',
        width,
    )


def text_width(text, size, weight=500, tracking=0.0):
    return outline(text, weight, tracking)[1] * size


def ink_bounds(text, weight=500, tracking=0.0):
    """Real ink box of a shaped run, in em units, y-down. The em square lies:
    "67" has no descender and centring on it would sit the mark low."""
    from fontTools.pens.boundsPen import BoundsPen

    _, glyphset, order, hb_font = _instance(weight)
    buf = hb.Buffer()
    buf.add_str(text)
    buf.guess_segment_properties()
    hb.shape(hb_font, buf)

    bp = BoundsPen(glyphset)
    x, track = 0.0, tracking * UPM
    for info, pos in zip(buf.glyph_infos, buf.glyph_positions):
        glyphset[order[info.codepoint]].draw(
            TransformPen(bp, (1, 0, 0, -1, x + pos.x_offset, -pos.y_offset))
        )
        x += pos.x_advance + track
    if bp.bounds is None:
        raise ValueError(f"no outlines for {text!r} -- is it in the font subset?")
    x0, y0, x1, y1 = bp.bounds
    return x0 / UPM, y0 / UPM, x1 / UPM, y1 / UPM


def fit_outline(text, box, weight=500, tracking=0.0, cap=0.72):
    """Outlined type scaled and optically centred inside the 64-unit mark grid."""
    d, _ = outline(text, weight, tracking)
    x0, y0, x1, y1 = (v * UPM for v in ink_bounds(text, weight, tracking))
    scale = box * cap / (y1 - y0)
    if (x1 - x0) * scale > box * 0.92:
        scale = box * 0.92 / (x1 - x0)
    tx = box / 2 - (x0 + x1) / 2 * scale
    ty = box / 2 - (y0 + y1) / 2 * scale
    return f'<g transform="translate({tx:.3f} {ty:.3f}) scale({scale:.5f})"><path d="{d}" fill="__TEXT__"/></g>'



# --------------------------------------------------------------------------
# Round three needs type set in more than one colour inside a single shaped
# run -- "p" in paper, "67" in amber -- so the pair kerns as one word rather
# than two strings pushed together. Shape the whole string once, then split the
# glyph stream by cluster.
# --------------------------------------------------------------------------
def typeset(parts, weight=500, tracking=0.0):
    """parts: [(text, colour_token, extra_space_before_em)].

    Returns ([(path_d, colour_token)], (x0, y0, x1, y1)) in font units, y-down,
    baseline at 0.
    """
    from fontTools.pens.boundsPen import BoundsPen

    text = "".join(t for t, _, _ in parts)
    _, glyphset, order, hb_font = _instance(weight)

    missing = [c for c in set(text) if ord(c) not in TTFont(FONT).getBestCmap()]
    if missing:
        raise ValueError(f"not in the Geist subset: {missing!r}")

    buf = hb.Buffer()
    buf.add_str(text)
    buf.guess_segment_properties()
    # Ligatures and contextual alternates off: a logo is a fixed drawing, and a
    # font update that changes a substitution should not change the mark.
    hb.shape(hb_font, buf, {"liga": False, "calt": False, "dlig": False})

    owner, bounds = [], BoundsPen(glyphset)
    for i, (t, _, _) in enumerate(parts):
        owner.extend([i] * len(t))
    pens = [SVGPathPen(glyphset, ntos=lambda v: f"{v:.1f}") for _ in parts]

    x, shift, seen = 0.0, 0.0, -1
    track = tracking * UPM
    for info, pos in zip(buf.glyph_infos, buf.glyph_positions):
        idx = owner[min(info.cluster, len(owner) - 1)]
        if idx != seen:
            shift += parts[idx][2] * UPM
            seen = idx
        at = (1, 0, 0, -1, x + shift + pos.x_offset, -pos.y_offset)
        glyph = glyphset[order[info.codepoint]]
        glyph.draw(TransformPen(pens[idx], at))
        glyph.draw(TransformPen(bounds, at))
        x += pos.x_advance + track

    if bounds.bounds is None:
        raise ValueError(f"no outlines for {text!r}")
    runs = [(pens[i].getCommands(), parts[i][1]) for i in range(len(parts))]
    return [r for r in runs if r[0]], bounds.bounds


def fit_parts(parts, weight=500, tracking=-0.04, vfill=0.64, hfill=0.88, vb=None):
    """Scale a shaped run into the mark grid. Height sets the optical size, so
    every mark in the round reads at the same weight; the box widens to suit.
    Returns (svg_body, viewbox_width)."""
    runs, (x0, y0, x1, y1) = typeset(parts, weight, tracking)
    w, h = x1 - x0, y1 - y0
    scale = 64 * vfill / h
    box = vb if vb else max(64, round(w * scale / hfill))
    tx = box / 2 - (x0 + x1) / 2 * scale
    ty = 32 - (y0 + y1) / 2 * scale
    inner = "".join(f'<path d="{d}" fill="{tok}"/>' for d, tok in runs)
    return f'<g transform="translate({tx:.3f} {ty:.3f}) scale({scale:.5f})">{inner}</g>', box


def measure(parts, weight=500, tracking=0.0):
    return typeset(parts, weight, tracking)


# --------------------------------------------------------------------------
# The marks. All eight sit on one 64x64 grid with a 7-unit stroke (11% of the
# canvas) so none of them is quietly winning on weight alone.
# --------------------------------------------------------------------------
# The lowercase p, reduced to a stem and a circle, is reused three times: as the
# mark itself, knocked out of a slab, and set inside a roundel.
P_BOWL = '<circle cx="33" cy="23.5" r="11.5"/>'
P_STEM = '<path d="M19 12V52" stroke-linecap="round"/>'

MARKS_R1 = [
    {
        "id": "counter",
        "name": "Counter",
        "kicker": "A stem and a circle",
        "why": (
            "The lowercase p with everything non-essential removed: one vertical, one "
            "perfect circle, joined the way a geometric face joins them. It has no idea "
            "to explain, which is why it will still be right in five years."
        ),
        "body": f'<g fill="none" stroke="__TEXT__" stroke-width="7">{P_BOWL}</g>'
                f'<g fill="none" stroke="__TEXT__" stroke-width="7">{P_STEM}</g>',
        "accent_body": f'<g fill="none" stroke="__ACCENT__" stroke-width="7">{P_BOWL}</g>'
                       f'<g fill="none" stroke="__TEXT__" stroke-width="7">{P_STEM}</g>',
    },
    {
        "id": "block",
        "name": "Block",
        "kicker": "The same p, on a square grid",
        "why": (
            "Every curve replaced with a right angle. It reads as a p and as a terminal "
            "glyph at the same time, which suits someone whose work is mostly Go and "
            "infrastructure. Holds up better than Counter below 20px."
        ),
        "body": '<rect x="20.5" y="12" width="23" height="22" fill="none" stroke="__TEXT__" stroke-width="7.5"/>'
                '<rect x="16.75" y="8.25" width="9" height="47.5" fill="__TEXT__"/>',
        "accent_body": '<rect x="20.5" y="12" width="23" height="22" fill="none" stroke="__ACCENT__" stroke-width="7.5"/>'
                       '<rect x="16.75" y="8.25" width="9" height="47.5" fill="__TEXT__"/>',
    },
    {
        "id": "slab",
        "name": "Slab",
        "kicker": "Counter, knocked out of a filled square",
        "why": (
            "The same p, inverted. GitHub renders an avatar at 40px in a list and a bare "
            "glyph disappears into the page; a filled slab owns its square and stays "
            "findable in a sidebar of thirty repos. This is the avatar cut, not the "
            "primary mark."
        ),
        "body": '__MASK__<rect width="64" height="64" rx="15" fill="__TEXT__" mask="url(#__ID__)"/>',
        "accent_body": '__MASK__<rect width="64" height="64" rx="15" fill="__ACCENT__" mask="url(#__ID__)"/>',
        "mask": '<mask id="__ID__" maskUnits="userSpaceOnUse" x="0" y="0" width="64" height="64">'
                '<rect width="64" height="64" fill="#fff"/>'
                '<g transform="translate(32 32) scale(0.7) translate(-32 -32)" fill="none" '
                'stroke="#000" stroke-width="7">' + P_BOWL + P_STEM + '</g></mask>',
    },
    {
        "id": "arch",
        "name": "Arch",
        "kicker": "A load, and the thing under it",
        "why": (
            "The home page already says \"I build systems that hold up under load\". This "
            "is that sentence as geometry: a bar resting on an arch, touching exactly, no "
            "gap to suggest the weight is decorative. The only mark here that argues a point."
        ),
        "body": '<g fill="none" stroke="__TEXT__" stroke-linecap="round">'
                '<path d="M19 52V33.5a13 13 0 0 1 26 0V52" stroke-width="6.5"/>'
                '<path d="M15 13h34" stroke-width="9"/></g>',
        "accent_body": '<g fill="none" stroke="__TEXT__" stroke-linecap="round">'
                       '<path d="M19 52V33.5a13 13 0 0 1 26 0V52" stroke-width="6.5"/></g>'
                       '<g fill="none" stroke="__ACCENT__" stroke-linecap="round">'
                       '<path d="M15 13h34" stroke-width="9"/></g>',
    },
    {
        "id": "beam",
        "name": "Beam",
        "kicker": "An I-beam, seen head-on",
        "why": (
            "Structure with no letter in it at all. Survives at 16px better than anything "
            "else here and is the easiest to stamp, emboss or punch. The cost is that it "
            "says nothing about the name -- it needs the wordmark beside it to identify you."
        ),
        "body": '<g fill="none" stroke="__TEXT__" stroke-width="7" stroke-linecap="round">'
                '<path d="M15 12h34"/><path d="M32 12v40"/><path d="M15 52h34"/></g>',
        "accent_body": '<g fill="none" stroke="__TEXT__" stroke-width="7" stroke-linecap="round">'
                       '<path d="M32 12v40"/><path d="M15 52h34"/></g>'
                       '<g fill="none" stroke="__ACCENT__" stroke-width="7" stroke-linecap="round">'
                       '<path d="M15 12h34"/></g>',
    },
    {
        "id": "ligature",
        "name": "Ligature",
        "kicker": "P and T welded into one glyph",
        "why": (
            "Both initials on one vertical: the T's crossbar at the top, the P's bowl hung "
            "below it on the same stem. Read it honestly and it is not two letters side by "
            "side, it is one invented glyph — which is either the most personal mark here or "
            "the most decorative, depending on your appetite."
        ),
        "body": '<g fill="none" stroke="__TEXT__" stroke-width="7" stroke-linecap="round">'
                '<path d="M32 24h6a9.5 9.5 0 0 1 0 19h-6"/><path d="M14 13h36"/>'
                '<path d="M32 13v39"/></g>',
        "accent_body": '<g fill="none" stroke="__ACCENT__" stroke-width="7" stroke-linecap="round">'
                       '<path d="M32 24h6a9.5 9.5 0 0 1 0 19h-6"/></g>'
                       '<g fill="none" stroke="__TEXT__" stroke-width="7" stroke-linecap="round">'
                       '<path d="M14 13h36"/><path d="M32 13v39"/></g>',
    },
    {
        "id": "stamp",
        "name": "Stamp",
        "kicker": "Counter, inside a seal",
        "why": (
            "The roundel turns a letter into a mark of issue -- a maker's stamp. It fits "
            "a site whose whole argument is published, checkable numbers. The thin ring is "
            "the first thing to break at favicon size, so it wants a solid fallback."
        ),
        "body": '<circle cx="32" cy="32" r="28" fill="none" stroke="__TEXT__" stroke-width="3.5"/>'
                '<g transform="translate(32 32) scale(0.62) translate(-32 -32)" fill="none" '
                'stroke="__TEXT__" stroke-width="7">' + P_BOWL + P_STEM + '</g>',
        "accent_body": '<circle cx="32" cy="32" r="28" fill="none" stroke="__ACCENT__" stroke-width="3.5"/>'
                       '<g transform="translate(32 32) scale(0.62) translate(-32 -32)" fill="none" '
                       'stroke="__TEXT__" stroke-width="7">' + P_BOWL + P_STEM + '</g>',
    },
    {
        "id": "sixtyseven",
        "name": "Sixty-Seven",
        "kicker": "The handle you already own",
        "why": (
            "github.com/pawan67, in/pawan67, pawan67.dev -- the number is already the "
            "consistent half of your identity. Set in the site's own Geist at 500 and "
            "tracked in, so it is native to the typography rather than applied to it. "
            "Weakest as a standalone avatar, strongest at making three profiles look like "
            "one person."
        ),
        "body": None,  # outlined below
        "accent_body": None,
    },
]


# --------------------------------------------------------------------------
# Round two: the Block family.
#
# One construction rule the whole set obeys -- right angles only, everything on
# integers, stem 9 units, left edge 16, right edge 48, bowl 8 to 38, stem foot
# at 56, counter 25-39 x 17-29. Nothing here is a curve that got squared off
# after the fact; they are all cut from the same grid, so they can be mixed in
# one identity without looking like they came from different hands.
# --------------------------------------------------------------------------
BOWL = '<rect x="20.5" y="12.5" width="23" height="21" stroke-width="9" fill="none" stroke="%s"/>'
STEM = '<rect x="16" y="8" width="9" height="48" fill="%s"/>'
MOD = [(0, 0), (1, 0), (2, 0), (3, 0), (0, 1), (3, 1), (0, 2), (3, 2),
       (0, 3), (1, 3), (2, 3), (3, 3), (0, 4), (0, 5)]


def modules(stem_c, rest_c):
    """The p as discrete 8.5-unit tiles on a 10-unit pitch. Column 0 is the
    stem and stays one colour; the rest builds the bowl."""
    return "".join(
        f'<rect x="{12.75 + c * 10:g}" y="{2.75 + r * 10:g}" width="8.5" height="8.5" '
        f'fill="{stem_c if c == 0 else rest_c}"/>'
        for c, r in MOD
    )


SLAB_INNER = ('<g transform="translate(32 32) scale(0.66) translate(-32 -32)">'
              + (BOWL % "#000") + (STEM % "#000") + '</g>')

MARKS_R2 = [
    {
        "id": "block",
        "name": "Block",
        "kicker": "The one you picked",
        "why": (
            "Redrawn on integers so the rest of this page has something exact to inherit: "
            "stem 9 wide, bowl 32 by 30, counter 14 by 12, foot at 56. Everything below is "
            "this shape asking one more question."
        ),
        "body": (BOWL % "__TEXT__") + (STEM % "__TEXT__"),
        "accent_body": (BOWL % "__ACCENT__") + (STEM % "__TEXT__"),
    },
    {
        "id": "capital",
        "name": "Capital",
        "kicker": "The same build, as a P",
        "why": (
            "Identical construction with the bowl stopped at 36 instead of 38, which turns "
            "the descender into a leg and the p into a P. It fills a square frame better than "
            "the lowercase does — no tail hanging into the padding — so it is the easier one "
            "to set inside an avatar, a favicon, or a stamp."
        ),
        "body": '<rect x="20.5" y="12.5" width="23" height="19" stroke-width="9" fill="none" stroke="__TEXT__"/>'
                + (STEM % "__TEXT__"),
        "accent_body": '<rect x="20.5" y="12.5" width="23" height="19" stroke-width="9" fill="none" stroke="__ACCENT__"/>'
                       + (STEM % "__TEXT__"),
    },
    {
        "id": "solid",
        "name": "Solid",
        "kicker": "Mass with a square punched out",
        "why": (
            "The bowl stops being a drawn outline and becomes a block with a hole in it. "
            "Roughly twice the ink of Block at the same size, which is what you want when the "
            "mark has to hold against a photograph, a busy README header, or 40px of grey "
            "sidebar. Loses a little of the letter, gains a lot of presence."
        ),
        "body": '<path fill-rule="evenodd" d="M16 8h32v30H16zM29 16h12v12H29z" fill="__TEXT__"/>'
                + (STEM % "__TEXT__"),
        "accent_body": '<path fill-rule="evenodd" d="M16 8h32v30H16zM29 16h12v12H29z" fill="__ACCENT__"/>'
                       + (STEM % "__TEXT__"),
    },
    {
        "id": "chisel",
        "name": "Chisel",
        "kicker": "One 45° cut, and only one",
        "why": (
            "Block with the foot of the descender sliced at forty-five degrees. It is the only "
            "diagonal in the family, which is exactly why it works as a signature — a single "
            "deliberate cut in an otherwise orthogonal system reads as a decision, where two "
            "would read as a style."
        ),
        "body": '<path fill-rule="evenodd" d="M16 8h32v30H16zM25 17h14v12H25z" fill="__TEXT__"/>'
                '<path d="M16 8h9v39l-9 9z" fill="__TEXT__"/>',
        "accent_body": '<path fill-rule="evenodd" d="M16 8h32v30H16zM25 17h14v12H25z" fill="__ACCENT__"/>'
                       '<path d="M16 8h9v39l-9 9z" fill="__TEXT__"/>',
    },
    {
        "id": "module",
        "name": "Module",
        "kicker": "Assembled, not drawn",
        "why": (
            "The same p resolved onto a 4×6 tile grid with the gaps left in. It says bitmap, "
            "terminal, and built-from-parts in one read, which is the most on-the-nose the "
            "family gets about what you actually do. Below about 20px the gaps close and it "
            "quietly becomes Block — a failure mode you can live with."
        ),
        "body": modules("__TEXT__", "__TEXT__"),
        "accent_body": modules("__TEXT__", "__ACCENT__"),
    },
    {
        "id": "blockslab",
        "name": "Slab",
        "kicker": "Knocked out of a hard square",
        "why": (
            "Block inverted into a filled square with no corner radius at all. This is the "
            "avatar cut: a stroked glyph on transparent disappears into a repo list, a solid "
            "field does not. The sharp corners are the point — round them and it becomes every "
            "other app icon."
        ),
        "body": '__MASK__<rect width="64" height="64" fill="__TEXT__" mask="url(#__ID__)"/>',
        "accent_body": '__MASK__<rect width="64" height="64" fill="__ACCENT__" mask="url(#__ID__)"/>',
        "mask": '<mask id="__ID__" maskUnits="userSpaceOnUse" x="0" y="0" width="64" height="64">'
                '<rect width="64" height="64" fill="#fff"/>' + SLAB_INNER + '</mask>',
    },
    {
        "id": "frame",
        "name": "Frame",
        "kicker": "Held inside a rule",
        "why": (
            "A 4-unit square rule around the mark at 58%. The frame does two jobs: it gives the "
            "p a fixed optical size across every context, and it reads as a plate or a stamped "
            "component rather than a letter floating in space. The thin rule is the first thing "
            "to break at 16px, so it wants Slab as its small-size fallback."
        ),
        "body": '<rect x="6" y="6" width="52" height="52" stroke-width="4" fill="none" stroke="__TEXT__"/>'
                '<g transform="translate(32 32) scale(0.58) translate(-32 -32)">'
                + (BOWL % "__TEXT__") + (STEM % "__TEXT__") + '</g>',
        "accent_body": '<rect x="6" y="6" width="52" height="52" stroke-width="4" fill="none" stroke="__ACCENT__"/>'
                       '<g transform="translate(32 32) scale(0.58) translate(-32 -32)">'
                       + (BOWL % "__TEXT__") + (STEM % "__TEXT__") + '</g>',
    },
    {
        "id": "blocksixtyseven",
        "name": "Sixty-Seven",
        "kicker": "The handle, built the same way",
        "why": (
            "Not typeset — constructed. The 6 is five bars and the 7 is two, drawn at the same "
            "9-unit weight and on the same grid as the p, so the number is a sibling of the "
            "letterform rather than a font choice sitting next to it. Pair it with Block and you "
            "have a mark and a handle that were obviously cut by the same hand."
        ),
        "body": '<g fill="none" stroke-width="7" stroke-linejoin="miter">'
                '<path d="M27 15H11v34h16V32H11" stroke="__TEXT__"/>'
                '<path d="M39 15h14v34" stroke="__TEXT__"/></g>',
        "accent_body": '<g fill="none" stroke-width="7" stroke-linejoin="miter">'
                       '<path d="M27 15H11v34h16V32H11" stroke="__TEXT__"/>'
                       '<path d="M39 15h14v34" stroke="__ACCENT__"/></g>',
    },
]


# --------------------------------------------------------------------------
# Round three: p67.
#
# The number alone had no anchor -- 67 is a number until something says whose.
# Every mark here is the same fix, the letter put back, and differs only in what
# sits between them. Set in the site's Geist at 500 and outlined, so the mark is
# a drawing rather than a font dependency.
# --------------------------------------------------------------------------
T, A = "__TEXT__", "__ACCENT__"


def _pair(letter, sep, tracking=-0.04, pre=0.0, post=0.0, vfill=0.64, upper=False):
    """letter + separator + 67. Mono and accent share one shaped run; only the
    colour assignment changes, so the two variants are the same drawing, not two
    drawings that happen to agree."""
    head = letter.upper() if upper else letter

    def build(num_colour):
        parts = [(head, T, 0.0)]
        if sep:
            parts.append((sep, T, pre))
        parts.append(("67", num_colour, post if sep else 0.0))
        return fit_parts(parts, tracking=tracking, vfill=vfill)

    mono, acc = build(T), build(A)
    return mono[0], acc[0], mono[1]


def _stack():
    """p over a rule over 67. The only composition here that is taller than it
    is wide, which is the only reason it works as an avatar."""
    p_runs, p_box = typeset([("p", T, 0.0)], 500, 0.0)
    n_runs, (nx0, ny0, nx1, ny1) = typeset([("67", T, 0.0)], 500, -0.04)

    # x-height 542 against digit height 710: set at the same size the p reads a
    # size smaller than the number it is meant to be introducing.
    k = 1.2
    px0, py0, px1, py1 = (v * k for v in p_box)

    rule_h, gap = 90.0, 150.0
    rule_y0 = py1 + gap
    rule_y1 = rule_y0 + rule_h
    n_dy = rule_y1 + gap - ny0           # slide 67 down under the rule
    width = max(px1 - px0, nx1 - nx0)
    rule_w = width * 1.34
    cx = (px0 + px1) / 2                 # centre everything on the p

    x0, x1 = cx - rule_w / 2, cx + rule_w / 2
    y0, y1 = py0, ny1 + n_dy
    scale = 64 * 0.80 / (y1 - y0)
    if (x1 - x0) * scale > 64 * 0.86:
        scale = 64 * 0.86 / (x1 - x0)
    tx = 32 - (x0 + x1) / 2 * scale
    ty = 32 - (y0 + y1) / 2 * scale

    def body(rule_c, num_c):
        parts = [f'<g transform="scale({k})"><path d="{d}" fill="{T}"/></g>'
                 for d, _ in p_runs]
        parts.append(f'<rect x="{x0:.1f}" y="{rule_y0:.1f}" width="{rule_w:.1f}" '
                     f'height="{rule_h:.1f}" fill="{rule_c}"/>')
        parts += [f'<g transform="translate(0 {n_dy:.1f})">'
                  f'<path d="{d}" fill="{num_c}"/></g>' for d, _ in n_runs]
        return (f'<g transform="translate({tx:.3f} {ty:.3f}) scale({scale:.5f})">'
                + "".join(parts) + '</g>')

    return body(T, T), body(A, T), 64


def _chip(uid_token="__ID__"):
    """67 reversed out of a filled block, with the p left outside it. The only
    mark here where the accent does structural work instead of tinting."""
    p_runs, (px0, py0, px1, py1) = typeset([("p", T, 0.0)], 500, 0.0)
    n_runs, (nx0, ny0, nx1, ny1) = typeset([("67", T, 0.0)], 500, -0.035)

    padx, pady, gap = 150.0, 130.0, 150.0
    dx = px1 + gap - nx0 + padx          # slide 67 right of the p
    cx0, cx1 = nx0 + dx - padx, nx1 + dx + padx
    cy0, cy1 = ny0 - pady, ny1 + pady

    x0, x1 = px0, cx1
    y0, y1 = min(py0, cy0), max(py1, cy1)
    scale = 64 * 0.70 / (y1 - y0)
    box = max(64, round((x1 - x0) * scale / 0.88))
    tx = box / 2 - (x0 + x1) / 2 * scale
    ty = 32 - (y0 + y1) / 2 * scale

    digits = "".join(f'<path d="{d}" fill="#000"/>' for d, _ in n_runs)
    mask = (f'<mask id="{uid_token}" maskUnits="userSpaceOnUse" x="{x0:.0f}" y="{y0:.0f}" '
            f'width="{x1 - x0:.0f}" height="{y1 - y0:.0f}">'
            f'<rect x="{cx0:.1f}" y="{cy0:.1f}" width="{cx1 - cx0:.1f}" '
            f'height="{cy1 - cy0:.1f}" fill="#fff"/>'
            f'<g transform="translate({dx:.1f} 0)">{digits}</g></mask>')

    def body(chip_c):
        return (f'<g transform="translate({tx:.3f} {ty:.3f}) scale({scale:.5f})">{mask}'
                + "".join(f'<path d="{d}" fill="{T}"/>' for d, _ in p_runs)
                + f'<rect x="{cx0:.1f}" y="{cy0:.1f}" width="{cx1 - cx0:.1f}" '
                  f'height="{cy1 - cy0:.1f}" rx="60" fill="{chip_c}" '
                  f'mask="url(#{uid_token})"/></g>')

    return body(T), body(A), box


_inline_m, _inline_a, _inline_vb = _pair("p", "")
_caps_m, _caps_a, _caps_vb = _pair("p", "", upper=True, tracking=-0.035)
_dot_m, _dot_a, _dot_vb = _pair("p", ".", upper=True, tracking=-0.03, post=0.012)
_mid_m, _mid_a, _mid_vb = _pair("p", "·", tracking=-0.02, pre=0.004, post=0.006)
_port_m, _port_a, _port_vb = _pair("p", ":", tracking=-0.025, pre=0.008, post=0.012)
_path_m, _path_a, _path_vb = _pair("p", "/", tracking=-0.03, pre=0.016, post=0.016)
_stack_m, _stack_a, _stack_vb = _stack()
_chip_m, _chip_a, _chip_vb = _chip()

SLAB67_INNER = '<g transform="translate(32 32) scale(0.66) translate(-32 -32)">%s</g>'

MARKS_R3 = [
    {
        "id": "handle", "name": "Handle", "vb": _inline_vb,
        "kicker": "p67, nothing between",
        "why": (
            "The handle with the letter put back and not one character more. It is what you "
            "would type, so it is what people already half-recognise from your GitHub URL. "
            "The tightest of the set at -0.04em, which is what stops it reading as a part number."
        ),
        "body": _inline_m, "accent_body": _inline_a,
    },
    {
        "id": "caps", "name": "Caps", "vb": _caps_vb,
        "kicker": "P67, the designation",
        "why": (
            "Same lockup, uppercase. It stops being a username and starts being a designation — "
            "closer to a model number or a spec than a login. Reads as more formal and slightly "
            "more anonymous; the lowercase is unmistakably yours, this one could be a product."
        ),
        "body": _caps_m, "accent_body": _caps_a,
    },
    {
        "id": "dot", "name": "Dot", "vb": _dot_vb,
        "kicker": "P.67 — your suggestion",
        "why": (
            "The period does real work: it stops the eye between the letter and the number, so "
            "the 67 reads as a value rather than a suffix. It also borrows the grammar of a clause "
            "number or a version string, which suits a site whose argument is published measurements."
        ),
        "body": _dot_m, "accent_body": _dot_a,
    },
    {
        "id": "middot", "name": "Middot", "vb": _mid_vb,
        "kicker": "p·67, the quiet one",
        "why": (
            "The same separation, set on the centre line instead of the baseline. This is already "
            "the separator your footer and your banner use, so the mark and the site punctuate the "
            "same way. The most restrained option here and the easiest to live with for years."
        ),
        "body": _mid_m, "accent_body": _mid_a,
    },
    {
        "id": "port", "name": "Port", "vb": _port_vb,
        "kicker": "p:67, host and port",
        "why": (
            "Two characters and it reads as an address. For someone who ships Go binaries and runs "
            "his own VPS, host:port is the most native punctuation available — the mark states what "
            "you do without a tagline doing it. The one concept here that a stranger will notice."
        ),
        "body": _port_m, "accent_body": _port_a,
    },
    {
        "id": "path", "name": "Path", "vb": _path_vb,
        "kicker": "p/67, namespace and name",
        "why": (
            "The slash reads as a route, a repo path, or a namespace — the same shape as "
            "<code>pawan67/portfolio-2027</code>. Slightly noisier than the dot because the diagonal "
            "fights the uprights, which is why it carries the most extra space of any mark here."
        ),
        "body": _path_m, "accent_body": _path_a,
    },
    {
        "id": "stack", "name": "Stack", "vb": _stack_vb,
        "kicker": "p over a rule over 67",
        "why": (
            "The only composition in the round that is taller than it is wide, which makes it the "
            "only one that genuinely fills an avatar. The rule does the separating that a dot or a "
            "colon does in the others, and gives the accent somewhere useful to sit. Pair it with "
            "Handle: this one for square slots, that one for anything horizontal."
        ),
        "body": _stack_m, "accent_body": _stack_a,
    },
    {
        "id": "chip", "name": "Chip", "vb": _chip_vb,
        "kicker": "The number, in a block of its own",
        "why": (
            "67 reversed out of a filled block with the p left outside it. The accent stops being a "
            "tint and becomes structure, which is the only way a second colour ever earns its place. "
            "Holds against a photograph or a busy header where the outlined lockups go soft."
        ),
        "body": _chip_m, "accent_body": _chip_a,
    },
    {
        "id": "slab67", "name": "Slab", "vb": 64,
        "kicker": "Stack, reversed out of a square",
        "why": (
            "The Stack lockup knocked out of a filled square. This is the favicon and the avatar "
            "cut — at 16px an outlined three-character lockup is mush, and a solid field with a "
            "shape cut into it is still a shape. Not a second logo; the same one, inverted."
        ),
        "body": '__MASK__<rect width="64" height="64" rx="13" fill="__TEXT__" mask="url(#__ID__)"/>',
        "accent_body": '__MASK__<rect width="64" height="64" rx="13" fill="__ACCENT__" mask="url(#__ID__)"/>',
        "mask": '<mask id="__ID__" maskUnits="userSpaceOnUse" x="0" y="0" width="64" height="64">'
                '<rect width="64" height="64" fill="#fff"/>'
                + SLAB67_INNER % _stack_m.replace("__TEXT__", "#000").replace("__ACCENT__", "#000")
                + '</mask>',
    },
]

SHIP = "--ship" in sys.argv
ROUND = 3 if "--p67" in sys.argv else 2 if "--block" in sys.argv else 1
MARKS = {1: MARKS_R1, 2: MARKS_R2, 3: MARKS_R3}[ROUND]
FAMILY = {1: "", 2: "block-family", 3: "p67-family"}[ROUND]
SVG_DIR = (OUT / FAMILY / "svg") if FAMILY else (OUT / "svg")
BANNER_DIR = (OUT / FAMILY / "banners") if FAMILY else (OUT / "banners")
PAGE = OUT / {1: "index.html", 2: "block.html", 3: "p67.html"}[ROUND]
for d in (SVG_DIR, BANNER_DIR):
    d.mkdir(parents=True, exist_ok=True)

for m in MARKS_R1:
    if m["id"] == "sixtyseven":
        m["body"] = fit_outline("67", 64, weight=500, tracking=-0.05, cap=0.66)
        m["accent_body"] = m["body"].replace("__TEXT__", "__ACCENT__")


def render(mark, variant, text_c, accent_c, uid="", square=False):
    body = mark["accent_body"] if variant == "accent" else mark["body"]
    if "mask" in mark or "__ID__" in body:
        # "__JS__" leaves the id as a token the page uniquifies at insert time;
        # two copies of the same mask id in one document and the first one wins.
        ident = "__MID__" if uid == "__JS__" else f'{mark["id"]}-{variant}-{uid}'
        body = body.replace("__MASK__", mark.get("mask", "")).replace("__ID__", ident)
    body = body.replace("__TEXT__", text_c).replace("__ACCENT__", accent_c)
    return squared(body, mark.get("vb", 64)) if square else body


def svg_file(inner, size=64, vb=64):
    return (
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {vb} 64" '
        f'width="{size * vb / 64:g}" height="{size}">{inner}</svg>\n'
    )


def squared(body, vb):
    """A wide lockup shrunk to fit a square slot -- an avatar, a favicon, a repo
    row. The emptiness this leaves above and below is the point: it is what the
    mark actually costs in a square."""
    if vb <= 64:
        return body
    s = 64 / vb
    return f'<g transform="translate(0 {(64 - 64 * s) / 2:.3f}) scale({s:.5f})">{body}</g>'




# --------------------------------------------------------------------------
# Emit standalone files: one per mark per theme, plus the wordmark lockups.
# --------------------------------------------------------------------------
written = []
for m in ([] if SHIP else MARKS):
    for name, (t, a) in {
        "dark":   (PALETTE["text"], PALETTE["text"]),
        "light":  (PALETTE["ink"], PALETTE["ink"]),
        "accent": (PALETTE["text"], PALETTE["accent"]),
        "accent-light": (PALETTE["ink"], PALETTE["accent_ink"]),
    }.items():
        variant = "accent" if name.startswith("accent") else "mono"
        p = SVG_DIR / f'{m["id"]}-{name}.svg'
        p.write_text(svg_file(render(m, variant, t, a, uid=name), vb=m.get("vb", 64)))
        written.append(p)

WORDMARKS = {
    "wordmark-pawan": ("pawan", 500, -0.02),
    "wordmark-full": ("Pawan Tamada", 500, -0.02),
}
for key, (txt, wt, tr) in (WORDMARKS.items() if ROUND == 1 and not SHIP else ()):
    for theme, col in (("dark", PALETTE["text"]), ("light", PALETTE["ink"])):
        size, pad = 100, 12
        node, w = text_svg(txt, size, pad, pad + 74, col, wt, tr)
        p = SVG_DIR / f"{key}-{theme}.svg"
        p.write_text(
            f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w + pad * 2:.0f} 110">'
            f"{node}</svg>\n"
        )
        written.append(p)


# --------------------------------------------------------------------------
# LinkedIn banners. 1584x396, which is what LinkedIn actually serves. The
# profile photo lands bottom-left and eats roughly a 240px circle, so nothing
# load-bearing goes left of x=360 -- every layout here starts at 400.
# --------------------------------------------------------------------------
def banner_statement():
    x0 = 408
    out = [f'<rect width="1584" height="396" fill="var(--bbg)"/>']
    out.append(f'<path d="M360 100V296" stroke="var(--bline)" stroke-width="1.5"/>')
    out.append(text_svg("I build systems that", 54, x0, 158, "var(--btext)", 500, -0.03)[0])
    out.append(text_svg("hold up under load.", 54, x0, 222, "var(--btext)", 500, -0.03)[0])
    out.append(
        text_svg(
            "Backend and infrastructure, mostly Go. Self-hosted on one VPS.",
            21, x0, 274, "var(--bmuted)", 400, 0.0,
        )[0]
    )
    out.append('<g transform="translate(1318 132) scale(2.1)">__MARK__</g>')
    return "".join(out)


def banner_watermark():
    x0 = 408
    out = [f'<rect width="1584" height="396" fill="var(--bbg)"/>']
    # The mark blown up past the edge, in the line colour: texture, not a second logo.
    out.append('<g transform="translate(1276 -116) scale(9.4)">__WM__</g>')
    out.append(text_svg("Pawan Tamada", 48, x0, 176, "var(--btext)", 500, -0.025)[0])
    out.append(
        text_svg("Software Engineer  ·  Backend and infrastructure", 22, x0, 214,
                 "var(--bmuted)", 400, 0.0)[0]
    )
    out.append('<path d="M408 243h176" stroke="var(--baccent)" stroke-width="2"/>')
    out.append(
        text_svg("pawan67.dev   ·   github.com/pawan67", 19, x0, 282,
                 "var(--bmuted)", 400, 0.03)[0]
    )
    return "".join(out)


def banner_paper():
    out = [f'<rect width="1584" height="396" fill="var(--bbg)"/>']
    out.append('<g transform="translate(404 120) scale(1.5)">__MARK__</g>')
    out.append('<path d="M400 206h1084" stroke="var(--baccent)" stroke-width="1.5"/>')
    out.append(text_svg("PAWAN TAMADA", 30, 1484, 188, "var(--btext)", 500, 0.18, anchor="end")[0])
    out.append(
        text_svg("SOFTWARE ENGINEER  ·  MUMBAI  ·  PAWAN67.DEV", 16, 1484, 250,
                 "var(--bmuted)", 400, 0.22, anchor="end")[0]
    )
    return "".join(out)


BANNERS = [
    {
        "id": "statement",
        "name": "Statement",
        "theme": "dark",
        "why": "The claim first. It is the same sentence the home page opens with, which is the point — "
               "someone who clicks through from LinkedIn should land on a page that agrees with the banner.",
        "svg": banner_statement(),
    },
    {
        "id": "watermark",
        "name": "Watermark",
        "theme": "dark",
        "why": "Name-first, with the mark blown up past the right edge as texture. Cropping is the point: "
               "a logo that survives being cut in half is a logo that had a shape to begin with.",
        "svg": banner_watermark(),
    },
    {
        "id": "paper",
        "name": "Paper",
        "theme": "light",
        "why": "The quiet one. A single rule, small tracked caps, and nothing else — it reads as a letterhead "
               "rather than a header image, and it is the only layout here that survives LinkedIn's mobile crop intact.",
        "svg": banner_paper(),
    },
]


# --------------------------------------------------------------------------
# --ship: cut the production set.
#
# P67 is the chosen lockup. It is 1.56 times wider than it is tall, and every
# square slot in the world -- favicon, avatar, touch icon -- is 1:1, so shipping
# the horizontal lockup into those slots would hand each of them a mark at 64%
# of the height it could have had. The square cut is the same content stacked,
# which is not a second logo: it is the only way one is legible at 16px.
# --------------------------------------------------------------------------
SHIP_MARK = "counter"          # kept only so the exploration pages still resolve
FINAL = OUT / "final"
PUBLIC = ROOT / "web/public"
SERIF = ROOT / "web/public/fonts/instrument-serif-v1.woff2"


def serif_glyph(ch, colour, box=64, fill=0.74, dy=0.0):
    """One glyph outlined from the site's own display face.

    No shaping needed for a single character, so this skips HarfBuzz and reads
    the outline straight off the glyph table. Centred on its real ink box rather
    than the em square -- a lowercase p is mostly descender, and centring on the
    em would hang it low in every square it is ever put in."""
    from fontTools.pens.boundsPen import BoundsPen

    font = TTFont(SERIF)
    gs, cmap = font.getGlyphSet(), font.getBestCmap()
    if ord(ch) not in cmap:
        raise ValueError(f"{ch!r} is not in {SERIF.name}")
    name = cmap[ord(ch)]

    pen = SVGPathPen(gs, ntos=lambda v: f"{v:.1f}")
    bounds = BoundsPen(gs)
    flip = (1, 0, 0, -1, 0, 0)          # font units are y-up, SVG is y-down
    gs[name].draw(TransformPen(pen, flip))
    gs[name].draw(TransformPen(bounds, flip))
    x0, y0, x1, y1 = bounds.bounds

    scale = box * fill / (y1 - y0)
    tx = box / 2 - (x0 + x1) / 2 * scale
    ty = box / 2 - (y0 + y1) / 2 * scale + dy
    return (f'<g transform="translate({tx:.3f} {ty:.3f}) scale({scale:.5f})">'
            f'<path d="{pen.getCommands()}" fill="{colour}"/></g>')


def tile(fg, bg_c, rx=13, fill=0.60):
    """The square cut: the same p on a filled field in the site's background
    colour. A bare glyph on transparent disappears into a tab strip and into a
    GitHub repo list; a filled field does not. Colours are baked rather than
    left to inherit, because a transparent counter drops to 1.9:1 against a
    white tab strip -- exactly where a favicon has to work."""
    return (f'<rect width="64" height="64" rx="{rx}" fill="{bg_c}"/>'
            + serif_glyph("p", fg, fill=fill))


def ship():
    import pathlib
    import shutil
    import subprocess

    FINAL.mkdir(parents=True, exist_ok=True)
    # Stale exports from an earlier mark read as part of the set; clear them.
    for old in list(FINAL.glob("p67-*")) + list(FINAL.glob("p-*")) + list(FINAL.glob("avatar-*")):
        old.unlink()
    mark = next(m for m in MARKS_R1 if m["id"] == SHIP_MARK)
    vb = mark.get("vb", 64)
    P_ = PALETTE
    written = []

    def w(name, body, vbw=64, root=FINAL):
        path = root / name
        path.write_text(
            f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {vbw:g} 64" '
            f'width="{vbw:g}" height="64">{body}</svg>\n')
        written.append(path)
        return path

    # --- the lockup, four cuts --------------------------------------------
    for name, colour in {
        "p-dark": P_["text"],
        "p-light": P_["ink"],
    }.items():
        w(f"{name}.svg", serif_glyph("p", colour, fill=0.80))

    # --- the square cut ----------------------------------------------------
    w("p-tile.svg", tile(P_["text"], P_["bg"]))
    w("p-tile-ink.svg", tile(P_["ink"], P_["paper"]))
    w("p-tile-square.svg", tile(P_["text"], P_["bg"], rx=0))

    # --- favicon -----------------------------------------------------------
    # The bare glyph, not a filled tile: "the site's colour" means it should be
    # paper on a dark tab strip and ink on a light one, which is a media query
    # rather than a decision baked at build time.
    (PUBLIC / "favicon.svg").write_text(
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" width="64" '
        f'height="64">{serif_glyph("p", "currentColor", fill=0.82)}'
        f'<style>svg{{color:{P_["ink"]}}}'
        f'@media (prefers-color-scheme:dark){{svg{{color:{P_["text"]}}}}}</style></svg>\n')
    written.append(PUBLIC / "favicon.svg")

    # --- LinkedIn banners, with P67 in them --------------------------------
    (FINAL / "banners").mkdir(exist_ok=True)
    for b in BANNERS:
        pal = ("bg", "text", "muted", "line", "accent") if b["theme"] == "dark" \
            else ("paper", "ink", "ink_muted", "ink_line", "accent_ink")
        cols = dict(zip(("bbg", "btext", "bmuted", "bline", "baccent"), (P_[k] for k in pal)))
        off = -(vb - 64) / 2
        inner = b["svg"]
        inner = inner.replace("__MARK__", f'<g transform="translate({off:g} 0)">'
                              + serif_glyph("p", cols["btext"], fill=0.72)
                              + "</g>")
        inner = inner.replace("__WM__", f'<g transform="translate({off:g} 0)">'
                              + serif_glyph("p", cols["bline"], fill=0.72)
                              + "</g>")
        for k, v in cols.items():
            inner = inner.replace(f"var(--{k})", v)
        path = FINAL / "banners" / f'linkedin-{b["id"]}.svg'
        path.write_text('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1584 396" '
                        f'width="1584" height="396">{inner}</svg>\n')
        written.append(path)

    # --- rasters -----------------------------------------------------------
    # GitHub, LinkedIn and iOS all want bitmaps. Skipped with a warning rather
    # than failed if librsvg is not installed: the SVGs above are the source.
    rsvg = shutil.which("rsvg-convert")
    if not rsvg:
        print("! rsvg-convert not found -- SVGs written, PNGs skipped")
    else:
        def png(src, out, size, bg=None, root=FINAL):
            cmd = [rsvg, "-w", str(size), "-h", str(size), str(src), "-o", str(root / out)]
            if bg:
                cmd[1:1] = ["-b", bg]
            subprocess.run(cmd, check=True)
            written.append(root / out)

        png(FINAL / "p-tile-square.svg", "apple-touch-icon.png", 180, root=PUBLIC)
        magick = shutil.which("magick") or shutil.which("convert")
        if magick:
            src = FINAL / "_ico.png"
            subprocess.run([rsvg, "-w", "64", "-h", "64",
                            str(FINAL / "p-tile.svg"), "-o", str(src)], check=True)
            # 32 and 16 only. ImageMagick writes ICO frames as raw BMP below
            # 256px, so adding a 48 costs ~9KB for a legacy-only file that
            # modern browsers never fetch -- they take favicon.svg instead.
            subprocess.run([magick, str(src), "-define", "icon:auto-resize=32,16",
                            str(PUBLIC / "favicon.ico")], check=True)
            src.unlink()
            written.append(PUBLIC / "favicon.ico")
        else:
            print("! ImageMagick not found -- favicon.ico left as-is")
        png(FINAL / "p-tile.svg", "icon-192.png", 192, root=PUBLIC)
        png(FINAL / "p-tile.svg", "icon-512.png", 512, root=PUBLIC)
        png(FINAL / "p-tile.svg", "avatar-512.png", 512)
        png(FINAL / "p-tile.svg", "avatar-400.png", 400)
        for b in BANNERS:
            src = FINAL / "banners" / f'linkedin-{b["id"]}.svg'
            out = FINAL / "banners" / f'linkedin-{b["id"]}.png'
            subprocess.run([rsvg, "-w", "1584", "-h", "396", str(src), "-o", str(out)], check=True)
            written.append(out)

    for path in written:
        print(f"  {path.relative_to(ROOT)}")
    print(f"shipped {len(written)} files from mark '{SHIP_MARK}'")


if "--ship" in sys.argv:
    ship()
    sys.exit(0)


# --------------------------------------------------------------------------
# Review page.
# --------------------------------------------------------------------------
_uid = [0]


def mark_html(mark, px=None, cls="", square=False):
    """Both colour variants in one <svg>; CSS decides which is visible.

    px is the HEIGHT. A lockup wider than it is tall keeps its aspect here and
    only gets squashed into a square where the container insists on one."""
    _uid[0] += 1
    uid = str(_uid[0])
    vb = 64 if square else mark.get("vb", 64)
    mono = render(mark, "mono", "currentColor", "currentColor", uid + "m", square)
    acc = render(mark, "accent", "currentColor", "var(--accent)", uid + "a", square)
    dim = f' width="{px * vb / 64:g}" height="{px}"' if px else ""
    return (
        f'<svg class="mk {cls}" viewBox="0 0 {vb:g} 64"{dim} role="img" aria-label="{mark["name"]} mark">'
        f'<g class="v-mono">{mono}</g><g class="v-accent">{acc}</g></svg>'
    )


def banner_html(b):
    _uid[0] += 1
    uid = str(_uid[0])
    off = -(MARKS[0].get("vb", 64) - 64) / 2
    slot_mark = (
        f'<g transform="translate({off:g} 0)">'
        f'<g class="v-mono">{render(MARKS[0], "mono", "var(--btext)", "var(--btext)", uid + "bm")}</g>'
        f'<g class="v-accent">{render(MARKS[0], "accent", "var(--btext)", "var(--baccent)", uid + "ba")}</g>'
        f'</g>'
    )
    slot_wm = (f'<g transform="translate({off:g} 0)">'
               + render(MARKS[0], "mono", "var(--bline)", "var(--bline)", uid + "w") + '</g>')
    body = b["svg"].replace("__MARK__", f'<g class="slot-mark">{slot_mark}</g>')
    body = body.replace("__WM__", f'<g class="slot-wm">{slot_wm}</g>')
    return (
        f'<svg class="banner" data-banner="{b["id"]}" viewBox="0 0 1584 396" '
        f'xmlns="http://www.w3.org/2000/svg">{body}'
        '<g class="safe"><circle cx="200" cy="382" r="132" fill="none" stroke="#e8663d" '
        'stroke-width="3" stroke-dasharray="10 8"/>'
        '<rect x="2" y="2" width="1580" height="392" fill="none" stroke="#e8663d" '
        'stroke-width="2" opacity="0.35"/>'
        '<path d="M360 0v396" stroke="#e8663d" stroke-width="2" stroke-dasharray="8 8" opacity="0.6"/>'
        '</g></svg>'
    )


# Mark bodies as JS, so the banner and lockup slots can be swapped live.
def js_bodies():
    out = {}
    for m in MARKS:
        _uid[0] += 1
        uid = str(_uid[0])
        out[m["id"]] = {
            "name": m["name"],
            "vb": m.get("vb", 64),
            "mono": render(m, "mono", "__T__", "__T__", "__JS__"),
            "accent": render(m, "accent", "__T__", "__A__", "__JS__"),
            "sqMono": render(m, "mono", "__T__", "__T__", "__JS__", square=True),
            "sqAccent": render(m, "accent", "__T__", "__A__", "__JS__", square=True),
        }
    return json.dumps(out)


font_b64 = base64.b64encode(FONT.read_bytes()).decode()

P = PALETTE
sizes_strip = lambda m: "".join(
    f'<div class="sz"><div class="szbox">{mark_html(m, px=s)}</div><span>{s}</span></div>'
    for s in (48, 32, 24, 16)
)

cards = []
for i, m in enumerate(MARKS):
    cards.append(f'''
    <article class="card" id="mark-{m['id']}">
      <div class="card-stage">
        <div class="stage stage-dark">{mark_html(m, px=132)}<svg class="grid-ov" style="width:{132 * m.get("vb", 64) / 64:g}px" viewBox="0 0 {m.get("vb", 64):g} 64" aria-hidden="true"><defs><pattern id="g{i}" width="8" height="8" patternUnits="userSpaceOnUse"><path d="M8 0H0V8" fill="none" stroke="currentColor" stroke-width="0.25"/></pattern></defs><rect width="{m.get("vb", 64):g}" height="64" fill="url(#g{i})"/><path d="M{m.get("vb", 64) / 2:g} 0v64M0 32h{m.get("vb", 64):g}" stroke="currentColor" stroke-width="0.4"/><rect x="8" y="8" width="{m.get("vb", 64) - 16:g}" height="48" fill="none" stroke="currentColor" stroke-width="0.4" stroke-dasharray="2 2"/></svg></div>
        <div class="stage stage-light">{mark_html(m, px=84)}</div>
      </div>
      <div class="card-body">
        <div class="card-head">
          <span class="num">{i + 1:02d}</span>
          <h3>{m['name']}</h3>
          <span class="kicker">{m['kicker']}</span>
        </div>
        <p class="why">{m['why']}</p>
        <div class="sizes">{sizes_strip(m)}</div>
        <div class="acts">
          <button class="btn" data-copy="{m['id']}">Copy SVG</button>
          <button class="btn" data-dl="{m['id']}">Download</button>
          <button class="btn btn-ghost" data-use="{m['id']}">Use in banners ↓</button>
        </div>
      </div>
    </article>''')

squint = "".join(
    f'<div class="sq"><div class="sq-row sq-dark">{mark_html(m, px=16)}{mark_html(m, px=24)}{mark_html(m, px=40)}</div>'
    f'<div class="sq-row sq-light">{mark_html(m, px=16)}{mark_html(m, px=24)}{mark_html(m, px=40)}</div>'
    f'<span class="sq-name">{m["name"]}</span></div>'
    for m in MARKS
)

avatars = "".join(
    f'<figure class="av"><div class="av-round av-dark">{mark_html(m, px=104, square=True)}</div>'
    f'<figcaption>{m["name"]}</figcaption></figure>'
    for m in MARKS
)

wm_pawan, wm_pawan_w = text_svg("pawan", 100, 0, 74, "currentColor", 500, -0.02)
wm_full, wm_full_w = text_svg("Pawan Tamada", 100, 0, 74, "currentColor", 500, -0.02)

banners_html = "".join(f'''
    <figure class="bfig" data-theme-force="{b['theme']}">
      <div class="bwrap">{banner_html(b)}</div>
      <figcaption><strong>{b['name']}</strong> <span>1584 × 396</span><p>{b['why']}</p>
      <button class="btn" data-dlbanner="{b['id']}">Download SVG</button></figcaption>
    </figure>''' for b in BANNERS)



# --------------------------------------------------------------------------
# Per-round copy. The page scaffold is shared; only the argument changes.
# --------------------------------------------------------------------------
ROUNDS = [
    ("index.html", "One", "eight marks, curves included"),
    ("block.html", "Two", "the square-grid family"),
    ("p67.html", "Three", "p67 — the handle, anchored"),
]


def nav(current):
    """Every page carries the whole set, with the current one flagged. Three
    rounds in three files is only navigable if each one admits the others."""
    out = []
    for href, name, note in ROUNDS:
        cur = ' aria-current="page"' if href == current else ""
        out.append(f'<a href="{href}"{cur}><b>Round {name}</b><span>{note}</span></a>')
    return '<nav class="xref" aria-label="Rounds">' + "".join(out) + "</nav>"

SQUINT_DEFAULT = ('<p class="sec-note">Actual pixel sizes, not scaled-down previews. A favicon is 16px, '
                  'a GitHub avatar in a repo list is 20px, and a LinkedIn thumbnail is 40px — those three '
                  'sizes decide the logo, not the hero.</p>')
AV_R1 = ('<p class="sec-note">GitHub, LinkedIn and X all crop to a circle. Anything that fills its square '
         'corner to corner loses those corners here — which is exactly what <em>Slab</em> is designed for '
         'and what <em>Stamp</em> already anticipates.</p>')
AV_R2 = ('<p class="sec-note">GitHub, LinkedIn and X all crop to a circle, so the sharp corners this family '
         'is built on are the first thing to go. <em>Slab</em> and <em>Frame</em> are the two that were '
         'drawn expecting it.</p>')

COPY = {
    1: {
        "title": "Brand marks",
        "squintnote": SQUINT_DEFAULT,
        "avatarnote": AV_R1,
        "header": '<header><p class="eyebrow">Pawan Tamada · identity, round one</p>'
                  '<h1>Eight marks, one system.</h1>'
                  '<p class="lede">Every mark below is drawn on the same 64-unit grid with the same '
                  '7-unit stroke, so nothing here is winning on weight or padding. Six are the letter '
                  '<em>p</em> solved six ways; two throw the letter out entirely. The palette and the '
                  'typeface are lifted straight out of <code>global.css</code> — this is not a separate '
                  'identity, it is the site&rsquo;s identity given a shape that survives at 16 pixels.</p>'
                  + nav('index.html') + '</header>',
        "verdict": """<div class="verdict">
      <p>If you want one answer: <b>Counter</b> as the primary mark, <b>Slab</b> as the avatar cut, and the
        <b>Paper</b> banner on LinkedIn.</p>
      <ol>
        <li><b>Counter</b> is the only mark here with nothing to explain. A stem and a circle is a decision you
          cannot get tired of, it is unmistakably a <em>p</em> at 16px, and it survives one colour, engraving,
          and a 40px circle crop without a fallback.</li>
        <li><b>Slab</b> is not a second logo, it is the same mark inverted into a filled square. Use it for the
          GitHub and LinkedIn avatars, where a thin glyph on a transparent background vanishes into the page
          chrome. One shape, two cuts — that is a system, not two logos.</li>
        <li><b>Arch</b> is the one to pick instead if you want the mark to make an argument. It is the home
          page's first sentence drawn as geometry. It costs you legibility as an initial — nobody reads it as
          <em>P</em> — so it only works with the wordmark locked beside it.</li>
        <li><b>Sixty-Seven</b> is the sleeper. <code>pawan67</code> is already the consistent half of your
          identity across GitHub, LinkedIn and DNS, and a number reads faster in a sidebar than a letterform.
          Weakest alone, strongest at making three profiles obviously the same person.</li>
      </ol>
      <p style="margin-top:22px">Once you pick, the files to cut are: <code>favicon.svg</code> (mono, both
        colour schemes via a media query, replacing the Astro default that is still in
        <code>web/public/</code>), a 512px PNG avatar, an <code>apple-touch-icon</code> at 180px on a filled
        background, and the banner. Say the word and I will wire them into the build.</p>
    </div>""",
    },
    2: {
        "title": "Block family",
        "squintnote": SQUINT_DEFAULT,
        "avatarnote": AV_R2,
        "header": '<header><p class="eyebrow">Pawan Tamada · identity, round two</p>'
                  '<h1>Eight ways to build it square.</h1>'
                  '<p class="lede">You picked Block, so this round stays inside its rules and asks a '
                  'different question eight times. Right angles only. Everything on integers: stem 9 units '
                  'wide, left edge at 16, right at 48, bowl from 8 to 38, foot at 56. These are not curves '
                  'that got squared off — they are cut from one grid, which is why any two of them can share '
                  'an identity without looking like they came from different hands.</p>'
                  + nav('block.html') + '</header>',
        "verdict": """<div class="verdict">
      <p>If you want one answer: <b>Block</b> stays the primary, <b>Slab</b> is the avatar, and
        <b>Sixty-Seven</b> is the piece that makes the set read as a system rather than a logo.</p>
      <ol>
        <li><b>Block</b> earned it. It is the only one here that is plainly a <em>p</em>, survives one colour,
          and needs no explanation. Everything else in this round is a specialist.</li>
        <li><b>Capital</b> is the one to switch to if you keep setting the mark inside a square — favicon, app
          icon, sticker. No descender means no dead padding, so it renders noticeably larger in the same box.
          The cost is that <em>P</em> is a more crowded letter to own than <em>p</em>.</li>
        <li><b>Solid</b> and <b>Slab</b> are the same instinct at two temperatures. Solid keeps the letter and
          roughly doubles the ink; Slab throws the letter into reverse and owns the square. Slab wins in a repo
          list, Solid wins anywhere you cannot control the background.</li>
        <li><b>Chisel</b> is the only one with a signature — a single 45° cut in an otherwise orthogonal system.
          One diagonal reads as a decision. Add a second anywhere and it becomes a style, so if you take this,
          take exactly one.</li>
        <li><b>Module</b> is the most honest about what you build and the least durable. Tile grids date, and
          the gaps close below 20px so it degrades into Block anyway. Good as a secondary — a loading state, a
          sticker, a 404 — and risky as the mark itself.</li>
        <li><b>Frame</b> fixes a real problem: a bare glyph looks a different size in every container. Use it as
          the locked-up version for slides and print, and fall back to Slab at favicon size, where the 4-unit
          rule stops resolving.</li>
        <li><b>Sixty-Seven</b> is the one I would not skip. <code>pawan67</code> is already your handle on
          GitHub, LinkedIn and DNS, and built on this grid at this weight it is visibly the same hand as the p.
          Mark on the avatar, number on the banner — that is an identity, not an icon.</li>
      </ol>
      <p style="margin-top:22px">Pick a pair — one letter, one square — and the files to cut are:
        <code>favicon.svg</code> (mono, both colour schemes via a media query, replacing the Astro default still
        sitting in <code>web/public/</code>), a 512px PNG avatar, an <code>apple-touch-icon</code> at 180px on a
        filled background, and whichever banner. Say which and I will wire them into the build.</p>
    </div>""",
    },
    3: {
        "title": "p67",
        "squintnote": ('<p class="sec-note">Actual pixel sizes, not scaled-down previews — and this is where '
                       'a three-character lockup has to answer for itself. A favicon is 16px and a repo-list '
                       'avatar is 20px. Watch which of these are still readable there and which have quietly '
                       'become a grey smudge; that is the whole argument for keeping <em>Stack</em> or '
                       '<em>Slab</em> in the system.</p>'),
        "avatarnote": ('<p class="sec-note">GitHub, LinkedIn and X all crop to a circle, and a circle is the '
                       'worst possible container for something wider than it is tall. The horizontal lockups '
                       'are shown here scaled to fit, which is exactly what those platforms will do to them. '
                       '<em>Stack</em> and <em>Slab</em> are the two that were drawn for this.</p>'),
        "header": '<header><p class="eyebrow">Pawan Tamada · identity, round three</p>'
                  '<h1>67, with something to hold on to.</h1>'
                  '<p class="lede">You were right about the number — on its own, <em>67</em> is a number, and '
                  'nobody can tell whose. Putting the <em>p</em> back fixes that, and then the only question '
                  'left is what sits between them. That is what these nine are: one idea, nine punctuations. '
                  'All set in the site&rsquo;s own Geist at 500 and converted to outlines, so the mark is a '
                  'drawing rather than a font dependency.</p>'
                  + nav('p67.html') + '</header>',
        "verdict": """<div class="verdict">
      <p>If you want one answer: <b>Middot</b> as the lockup, <b>Stack</b> as the avatar, <code>pawan67</code>
        spelled out as the wordmark. One idea at three widths.</p>
      <ol>
        <li><b>Middot</b> is the one I would live with. The dot separates the letter from the number so 67 reads
          as a value rather than a suffix, it sits on the centre line so it never gets mistaken for a full stop
          at the end of a sentence, and it is <em>already</em> how your footer punctuates. The mark and the site
          would be using the same grammar rather than two different ones.</li>
        <li><b>Dot</b> — your <code>P.67</code> — is the same move with more formality. It reads as a clause or
          a version number, which genuinely suits a site whose argument is published measurements. The one thing
          to weigh: a period at the baseline is a terminator, so the mark reads slightly more like an
          abbreviation and slightly less like a name.</li>
        <li><b>Port</b> is the interesting one and the risky one. <code>p:67</code> reads as host and port to
          anyone who would hire you, and as nothing in particular to anyone who would not. That is not
          necessarily a bad trade for a backend engineer — it is a mark that filters — but it is a decision to
          make deliberately rather than by accident.</li>
        <li><b>Handle</b> and <b>Caps</b> are the no-separator baseline, and worth comparing directly: lowercase
          is unmistakably a username and therefore unmistakably yours; uppercase reads as a designation, more
          formal and more anonymous. Since the handle you actually own is lowercase everywhere, lowercase is the
          honest one.</li>
        <li><b>Stack</b> is the piece that makes the rest work. Every horizontal lockup here is roughly 1.6
          times wider than tall, and an avatar slot is 1:1 — which means on GitHub and LinkedIn the wide version
          renders small and soft. Stack is the same content turned vertical, and it is the only composition here
          that genuinely fills a circle crop. <b>Slab</b> is Stack inverted for the favicon, where outlined type
          at 16px stops resolving entirely.</li>
        <li><b>Chip</b> is the only mark in the round where the amber does structural work instead of tinting
          something. Worth keeping in the system even if it is not the primary — it is the version that holds up
          on a photograph or a busy header.</li>
        <li><b>Path</b> is honest about the repo (<code>pawan67/portfolio-2027</code>) but the slash is a
          diagonal in a lockup made of uprights, and it fights them. It needs the most extra spacing of anything
          here, which is usually the sign a separator is working too hard.</li>
      </ol>
      <p style="margin-top:22px">The system I would actually ship: <b>Stack</b> at avatar and favicon sizes,
        <b>Middot</b> anywhere horizontal, and <code>pawan67</code> spelled out where there is room for it —
        so the mark always has an expansion a stranger can read. Files to cut: <code>favicon.svg</code> (mono,
        both colour schemes via a media query, replacing the Astro default still sitting in
        <code>web/public/</code>), a 512px PNG avatar, an <code>apple-touch-icon</code> at 180px on a filled
        background, and whichever banner. Say which and I will wire them into the build.</p>
    </div>""",
    },
}

HTML = r"""<!doctype html>
<html lang="en" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>@@TITLE@@ — Pawan Tamada</title>
<style>
@font-face{font-family:"Geist";src:url(data:font/woff2;base64,@@FONT@@) format("woff2");font-weight:400 500;font-style:normal;font-display:block}
*,*::before,*::after{box-sizing:border-box}
html{-webkit-text-size-adjust:100%}
body{margin:0;font-family:"Geist",ui-sans-serif,system-ui,sans-serif;font-size:17px;line-height:1.6;
  background:var(--bg);color:var(--text);transition:background 220ms cubic-bezier(.22,1,.36,1),color 220ms cubic-bezier(.22,1,.36,1)}
body[data-theme="dark"]{--bg:@@BG@@;--surface:@@SURFACE@@;--line:@@LINE@@;--text:@@TEXT@@;--muted:@@MUTED@@;--accent:@@ACCENT@@}
body[data-theme="light"]{--bg:@@PAPER@@;--surface:#fff;--line:@@INKLINE@@;--text:@@INK@@;--muted:@@INKMUTED@@;--accent:@@ACCENTINK@@}
h1,h2,h3{font-weight:500;letter-spacing:-.02em;line-height:1.08;margin:0;text-wrap:balance}
p{margin:0;text-wrap:pretty}
a{color:inherit}
.wrap{max-width:1180px;margin:0 auto;padding:0 32px}
.eyebrow{font-size:11px;letter-spacing:.2em;text-transform:uppercase;color:var(--muted)}

/* ---- header + sticky control bar ---- */
header{padding:72px 0 40px}
header h1{font-size:clamp(2.2rem,1.2rem+3.4vw,3.6rem);letter-spacing:-.035em;margin:14px 0 0;max-width:16ch}
header .lede{margin-top:20px;max-width:56ch;color:var(--muted);font-size:1.08rem}
.xref{display:flex;flex-wrap:wrap;gap:8px;margin-top:30px}
.xref a{display:flex;flex-direction:column;gap:2px;border:1px solid var(--line);border-radius:7px;
  padding:9px 16px;text-decoration:none;color:var(--muted);
  transition:border-color 140ms cubic-bezier(.22,1,.36,1),color 140ms cubic-bezier(.22,1,.36,1)}
.xref a b{font-size:13px;font-weight:500;color:var(--text)}
.xref a span{font-size:11.5px;letter-spacing:.02em}
.xref a:hover{border-color:var(--accent)}
.xref a:hover b{color:var(--accent)}
.xref a[aria-current="page"]{background:var(--surface);border-color:var(--accent)}
.xref a[aria-current="page"] b{color:var(--accent)}
.bar{position:sticky;top:0;z-index:30;background:color-mix(in oklab,var(--bg) 88%,transparent);
  backdrop-filter:blur(14px);border-top:1px solid var(--line);border-bottom:1px solid var(--line);margin-top:44px}
.bar-in{display:flex;flex-wrap:wrap;gap:10px 28px;align-items:center;padding:12px 32px;max-width:1180px;margin:0 auto}
.seg{display:inline-flex;border:1px solid var(--line);border-radius:6px;overflow:hidden}
.seg button{appearance:none;background:transparent;border:0;border-right:1px solid var(--line);color:var(--muted);
  font:inherit;font-size:12.5px;letter-spacing:.04em;padding:6px 13px;cursor:pointer;transition:all 140ms cubic-bezier(.22,1,.36,1)}
.seg button:last-child{border-right:0}
.seg button[aria-pressed="true"]{background:var(--text);color:var(--bg)}
.seg button:hover:not([aria-pressed="true"]){color:var(--text)}
.bar label{font-size:12.5px;color:var(--muted);letter-spacing:.03em}

/* ---- sections ---- */
section{padding:84px 0 0}
.sec-head{display:flex;align-items:baseline;gap:18px;border-top:1px solid var(--line);padding-top:18px;margin-bottom:8px}
.sec-head h2{font-size:1.5rem}
.sec-head .eyebrow{margin-left:auto}
.sec-note{color:var(--muted);max-width:62ch;margin-top:12px;font-size:.96rem}

/* ---- mark cards ---- */
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(440px,1fr));gap:1px;background:var(--line);
  border:1px solid var(--line);border-radius:10px;overflow:hidden;margin-top:34px}
.card{background:var(--bg);padding:0;display:flex;flex-direction:column}
.card-stage{display:grid;grid-template-columns:1fr 170px;border-bottom:1px solid var(--line)}
.stage{position:relative;display:grid;place-items:center;min-height:212px}
.stage-dark{background:@@BG@@;color:@@TEXT@@;--accent:@@ACCENT@@}
.stage-light{background:@@PAPER@@;color:@@INK@@;--accent:@@ACCENTINK@@;border-left:1px solid var(--line)}
.grid-ov{position:absolute;width:132px;height:132px;color:@@ACCENT@@;opacity:0;pointer-events:none;
  transition:opacity 160ms cubic-bezier(.22,1,.36,1)}
body[data-grid="on"] .grid-ov{opacity:.42}
.card-body{padding:22px 24px 24px;display:flex;flex-direction:column;flex:1}
.card-head{display:flex;align-items:baseline;gap:10px;flex-wrap:wrap}
.num{font-variant-numeric:tabular-nums;font-size:11px;letter-spacing:.14em;color:var(--muted)}
.card-head h3{font-size:1.22rem}
.kicker{color:var(--muted);font-size:.86rem}
.why{margin-top:12px;color:var(--muted);font-size:.92rem;max-width:54ch}
.sizes{display:flex;gap:22px;align-items:flex-end;margin-top:20px;padding-top:18px;border-top:1px solid var(--line)}
.sz{display:flex;flex-direction:column;align-items:center;gap:7px}
.szbox{display:grid;place-items:center;height:48px}
.sz span{font-size:10px;letter-spacing:.1em;color:var(--muted);font-variant-numeric:tabular-nums}
.acts{display:flex;gap:8px;flex-wrap:wrap;margin-top:auto;padding-top:20px}
.btn{appearance:none;background:transparent;border:1px solid var(--line);border-radius:5px;color:var(--text);
  font:inherit;font-size:12.5px;padding:6px 12px;cursor:pointer;
  transition:border-color 140ms cubic-bezier(.22,1,.36,1),color 140ms cubic-bezier(.22,1,.36,1),transform 120ms cubic-bezier(.22,1,.36,1)}
.btn:hover{border-color:var(--accent);color:var(--accent)}
.btn:active{transform:scale(.97)}
.btn-ghost{border-color:transparent;color:var(--muted)}
.btn.ok{border-color:var(--accent);color:var(--accent)}

/* ---- variant + mark visibility ---- */
body[data-variant="mono"] .v-accent{display:none}
body[data-variant="accent"] .v-mono{display:none}
.mk{display:block;overflow:visible}

/* ---- squint test ---- */
.squint{display:grid;grid-template-columns:repeat(auto-fill,minmax(248px,1fr));gap:1px;background:var(--line);
  border:1px solid var(--line);border-radius:10px;overflow:hidden;margin-top:34px}
.sq{background:var(--bg);text-align:center}
.sq-row{display:flex;align-items:center;justify-content:center;gap:16px;padding:20px 8px}
.sq-dark{background:@@BG@@;color:@@TEXT@@;--accent:@@ACCENT@@}
.sq-light{background:@@PAPER@@;color:@@INK@@;--accent:@@ACCENTINK@@}
.sq-name{display:block;padding:9px 6px;font-size:11px;letter-spacing:.09em;color:var(--muted)}

/* ---- avatars ---- */
.avs{display:grid;grid-template-columns:repeat(auto-fill,minmax(230px,1fr));gap:30px;margin-top:34px}
.av{margin:0;text-align:center}
.av-round{width:140px;height:140px;margin:0 auto;border-radius:50%;display:grid;place-items:center;
  background:@@BG@@;color:@@TEXT@@;--accent:@@ACCENT@@;border:1px solid @@LINE@@}
.av figcaption{margin-top:11px;font-size:11.5px;letter-spacing:.07em;color:var(--muted)}

/* ---- live context mocks ---- */
.ctx{display:grid;grid-template-columns:repeat(auto-fit,minmax(320px,1fr));gap:26px;margin-top:34px;align-items:start}
.mock{border:1px solid var(--line);border-radius:10px;overflow:hidden;background:var(--surface)}
.mock h4{margin:0;padding:11px 15px;font-size:11px;letter-spacing:.14em;text-transform:uppercase;
  color:var(--muted);font-weight:400;border-bottom:1px solid var(--line)}
.mock-in{padding:20px;background:@@BG@@;color:@@TEXT@@;--accent:@@ACCENT@@}
.tabstrip{display:flex;gap:5px;align-items:flex-end}
.tab{display:flex;align-items:center;gap:8px;background:@@SURFACE@@;border-radius:8px 8px 0 0;padding:9px 14px;
  font-size:12.5px;color:@@MUTED@@;max-width:190px}
.tab.on{background:@@LINE@@;color:@@TEXT@@}
.tab .mk{flex:none}
.tab span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.repo{display:flex;align-items:center;gap:11px;padding:11px 0;border-bottom:1px solid @@LINE@@;font-size:13.5px}
.repo:last-child{border-bottom:0}
.repo .mk{flex:none;border-radius:4px}
.repo b{font-weight:500;color:@@TEXT@@}
.repo em{font-style:normal;color:@@MUTED@@;font-size:12px;margin-left:auto}
.ogcard{aspect-ratio:1200/630;background:@@BG@@;color:@@TEXT@@;--accent:@@ACCENT@@;border:1px solid @@LINE@@;
  border-radius:8px;padding:7%;display:flex;flex-direction:column;justify-content:space-between}
.ogcard .og-t{font-size:clamp(18px,3.3cqw,34px);letter-spacing:-.03em;line-height:1.1;max-width:15ch}
.og-foot{display:flex;align-items:center;gap:12px;font-size:12.5px;color:@@MUTED@@}

/* ---- lockups ---- */
.locks{display:grid;grid-template-columns:repeat(auto-fit,minmax(300px,1fr));gap:1px;background:var(--line);
  border:1px solid var(--line);border-radius:10px;overflow:hidden;margin-top:34px}
.lock{background:@@BG@@;color:@@TEXT@@;--accent:@@ACCENT@@;min-height:190px;display:grid;place-items:center;padding:30px}
.lock.pale{background:@@PAPER@@;color:@@INK@@;--accent:@@ACCENTINK@@}
.lh{display:flex;align-items:center;gap:16px}
.lh .rule{width:1px;height:38px;background:currentColor;opacity:.25}
.lh .sub{font-size:11px;letter-spacing:.16em;text-transform:uppercase;line-height:1.5;opacity:.65}
.lv{display:flex;flex-direction:column;align-items:center;gap:16px;text-align:center}
.wm{display:block;height:34px;width:auto;overflow:visible}
.wm-sm{height:23px}

/* ---- banners ---- */
.bfig{margin:0 0 42px}
.bwrap{border:1px solid var(--line);border-radius:10px;overflow:hidden;line-height:0}
.banner{display:block;width:100%;height:auto}
.bfig[data-theme-force="dark"]{--bbg:@@BG@@;--btext:@@TEXT@@;--bmuted:@@MUTED@@;--bline:@@LINE@@;--baccent:@@ACCENT@@}
.bfig[data-theme-force="light"]{--bbg:@@PAPER@@;--btext:@@INK@@;--bmuted:@@INKMUTED@@;--bline:@@INKLINE@@;--baccent:@@ACCENTINK@@}
.safe{opacity:0;transition:opacity 160ms cubic-bezier(.22,1,.36,1)}
body[data-safe="on"] .safe{opacity:1}
.bfig figcaption{display:flex;flex-wrap:wrap;align-items:baseline;gap:8px 14px;margin-top:14px}
.bfig figcaption strong{font-weight:500}
.bfig figcaption>span{font-size:11px;letter-spacing:.1em;color:var(--muted);font-variant-numeric:tabular-nums}
.bfig figcaption p{flex:1 1 380px;color:var(--muted);font-size:.9rem;margin:0}

/* ---- verdict ---- */
.verdict{border:1px solid var(--line);border-radius:10px;padding:32px;margin-top:34px;background:var(--surface)}
.verdict ol{margin:18px 0 0;padding-left:1.3em}
.verdict li{margin-bottom:12px;color:var(--muted)}
.verdict li b{color:var(--text);font-weight:500}
.verdict p{max-width:66ch;color:var(--muted)}
footer{padding:90px 0 70px;color:var(--muted);font-size:.86rem;border-top:1px solid var(--line);margin-top:90px}
.toast{position:fixed;left:50%;bottom:30px;transform:translate(-50%,16px);background:var(--text);color:var(--bg);
  padding:9px 18px;border-radius:6px;font-size:13px;opacity:0;pointer-events:none;
  transition:opacity 180ms cubic-bezier(.22,1,.36,1),transform 180ms cubic-bezier(.22,1,.36,1);z-index:60}
.toast.on{opacity:1;transform:translate(-50%,0)}
@media (max-width:760px){.wrap{padding:0 20px}.bar-in{padding:12px 20px}.card-stage{grid-template-columns:1fr}
  .stage-light{border-left:0;border-top:1px solid var(--line)}}
@media (prefers-reduced-motion:reduce){*{transition-duration:1ms!important}}
</style>
</head>
<body data-theme="dark" data-variant="accent" data-grid="off" data-safe="off">

<div class="wrap">
  @@HEADER@@
</div>

<div class="bar"><div class="bar-in">
  <div class="seg" role="group" aria-label="Surface">
    <button data-set="theme" data-val="dark" aria-pressed="true">Off-black</button>
    <button data-set="theme" data-val="light" aria-pressed="false">Paper</button>
  </div>
  <div class="seg" role="group" aria-label="Colour">
    <button data-set="variant" data-val="accent" aria-pressed="true">Two-tone</button>
    <button data-set="variant" data-val="mono" aria-pressed="false">One colour</button>
  </div>
  <div class="seg" role="group" aria-label="Overlays">
    <button data-set="grid" data-val="on" aria-pressed="false">Construction grid</button>
    <button data-set="safe" data-val="on" aria-pressed="false">Banner safe zones</button>
  </div>
  <label>Applied everywhere: <b id="current-mark">@@FIRSTNAME@@</b></label>
</div></div>

<div class="wrap">

  <section id="the-marks">
    <div class="sec-head"><h2>The marks</h2><span class="eyebrow">01 — @@COUNT@@</span></div>
    <p class="sec-note">Left panel is the mark on off-black, right panel is the same mark on paper. Switch
      to <em>One colour</em> in the bar above — a mark that only works in two tones is not a logo, it is an
      illustration.</p>
    <div class="grid">@@CARDS@@</div>
  </section>

  <section id="squint">
    <div class="sec-head"><h2>The squint test</h2><span class="eyebrow">16 / 24 / 40 px</span></div>
    @@SQUINTNOTE@@
    <div class="squint">@@SQUINT@@</div>
  </section>

  <section id="avatar">
    <div class="sec-head"><h2>As an avatar</h2><span class="eyebrow">Circle crop</span></div>
    @@AVATARNOTE@@
    <div class="avs">@@AVATARS@@</div>
  </section>

  <section id="context">
    <div class="sec-head"><h2>In the wild</h2><span class="eyebrow">Live — follows the selection</span></div>
    <p class="sec-note">These three follow whichever mark you picked with <em>Use in banners</em>. They are the
      places the mark actually has to work.</p>
    <div class="ctx">
      <div class="mock"><h4>Browser tab</h4><div class="mock-in">
        <div class="tabstrip">
          <div class="tab on"><span class="slot-ctx"></span><span>Pawan — I build systems…</span></div>
          <div class="tab"><span>Inbox (3)</span></div>
        </div>
      </div></div>
      <div class="mock"><h4>GitHub, starred repos</h4><div class="mock-in">
        <div class="repo"><span class="slot-ctx"></span><b>pawan67 / portfolio-2027</b><em>Go</em></div>
        <div class="repo"><span class="slot-ctx"></span><b>pawan67 / lift</b><em>TypeScript</em></div>
        <div class="repo"><span class="slot-ctx"></span><b>pawan67 / instamart-alerts</b><em>Go</em></div>
      </div></div>
      <div class="mock"><h4>Link preview · 1200 × 630</h4><div class="mock-in" style="padding:14px">
        <div class="ogcard" style="container-type:inline-size">
          <span class="slot-ctx-lg"></span>
          <div class="og-t">I build systems that hold up under load.</div>
          <div class="og-foot">pawan67.dev · self-hosted on one VPS</div>
        </div>
      </div></div>
    </div>
  </section>

  <section id="lockups">
    <div class="sec-head"><h2>Lockups</h2><span class="eyebrow">Outlined, not live text</span></div>
    <p class="sec-note">The wordmark is the site's own Geist at 500, tracked −0.02em, converted to outlines —
      a logo that depends on a font file being present is a logo that renders as Arial on someone else's slide.</p>
    <div class="locks">
      <div class="lock"><div class="lh"><span class="slot-lock"></span>
        <svg class="wm" viewBox="0 0 @@WMFULLW@@ 100" fill="none">@@WMFULL@@</svg></div></div>
      <div class="lock"><div class="lh"><span class="slot-lock"></span><span class="rule"></span>
        <span class="sub">Pawan Tamada<br>Software Engineer</span></div></div>
      <div class="lock pale"><div class="lv"><span class="slot-lock"></span>
        <svg class="wm wm-sm" viewBox="0 0 @@WMPAWANW@@ 100" fill="none">@@WMPAWAN@@</svg></div></div>
    </div>
  </section>

  <section id="banners">
    <div class="sec-head"><h2>LinkedIn banners</h2><span class="eyebrow">1584 × 396</span></div>
    <p class="sec-note">Turn on <em>Banner safe zones</em> above: the dashed circle is where LinkedIn drops your
      profile photo, and the dashed vertical is the line nothing important should cross. All three layouts start
      at x = 400 for that reason. They follow the mark you selected.</p>
    @@BANNERS@@
  </section>

  <section id="verdict">
    <div class="sec-head"><h2>What I would ship</h2><span class="eyebrow">Opinion</span></div>
    @@VERDICT@@
  </section>

</div>

<footer><div class="wrap">Generated by <code>design/brand/build.py</code>. Palette and typeface read from
  <code>web/src/styles/global.css</code> and <code>web/public/fonts/geist-sans-v1.woff2</code>.</div></footer>

<div class="toast" id="toast"></div>

<script>
const BODIES = @@BODIES@@;
const HEX = {
  dark:  {t:"@@TEXT@@", a:"@@ACCENT@@", bbg:"@@BG@@", btext:"@@TEXT@@", bmuted:"@@MUTED@@", bline:"@@LINE@@", baccent:"@@ACCENT@@"},
  light: {t:"@@INK@@",  a:"@@ACCENTINK@@", bbg:"@@PAPER@@", btext:"@@INK@@", bmuted:"@@INKMUTED@@", bline:"@@INKLINE@@", baccent:"@@ACCENTINK@@"}
};
const body = document.body;
let current = "@@FIRSTID@@";
let uid = 0;

function svgFor(id, variant, t, a, square){
  const key = square ? (variant === "mono" ? "sqMono" : "sqAccent") : variant;
  let s = BODIES[id][key];
  // Mask ids must be unique per instance or the first one in the document wins.
  // Both halves of a body (the mask and its url(#...)) must land on the same id.
  if (s.indexOf("__MID__") >= 0) s = s.split("__MID__").join("m" + (++uid));
  return s.replace(/__T__/g, t).replace(/__A__/g, a);
}
function inlineMark(id, px, t, a, square){
  // px is the height; a lockup wider than it is tall keeps its aspect.
  const v = body.dataset.variant, vb = square ? 64 : BODIES[id].vb;
  return '<svg class="mk" viewBox="0 0 '+vb+' 64" width="'+(px*vb/64)+'" height="'+px+'" aria-hidden="true">'
       + svgFor(id, v, t, a, square) + '</svg>';
}
function standalone(id, variant, t, a){
  const vb = BODIES[id].vb;
  return '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 '+vb+' 64" width="'+vb+'" height="64">'
       + svgFor(id, variant, t, a) + '</svg>\n';
}

function paint(){
  const vb = BODIES[current].vb;
  // Slots are anchored on where a 64-wide mark would have sat, so swapping in
  // a wider lockup grows it around that point instead of off to the right.
  const off = -(vb - 64) / 2;
  // Banner + lockup slots carry both variants so the toggle stays instant.
  document.querySelectorAll(".slot-mark").forEach(n => {
    n.innerHTML = '<g transform="translate('+off+' 0)">'
                + '<g class="v-mono">'  + svgFor(current,"mono","var(--btext)","var(--btext)") + '</g>'
                + '<g class="v-accent">'+ svgFor(current,"accent","var(--btext)","var(--baccent)") + '</g></g>';
  });
  document.querySelectorAll(".slot-wm").forEach(n => {
    n.innerHTML = '<g transform="translate('+off+' 0)">'
                + svgFor(current,"mono","var(--bline)","var(--bline)") + '</g>';
  });
  document.querySelectorAll(".slot-lock").forEach(n => {
    n.innerHTML = '<svg class="mk" viewBox="0 0 '+vb+' 64" width="'+(46*vb/64)+'" height="46">'
      + '<g class="v-mono">'  + svgFor(current,"mono","currentColor","currentColor") + '</g>'
      + '<g class="v-accent">'+ svgFor(current,"accent","currentColor","var(--accent)") + '</g></svg>';
  });
  // A favicon and a repo row are square whether the mark likes it or not.
  document.querySelectorAll(".slot-ctx").forEach(n => { n.innerHTML = inlineMark(current,16,"currentColor","var(--accent)",true); });
  document.querySelectorAll(".slot-ctx-lg").forEach(n => { n.innerHTML = inlineMark(current,54,"currentColor","var(--accent)",false); });
  document.getElementById("current-mark").textContent = BODIES[current].name;
}

document.querySelectorAll(".bar button").forEach(b => {
  b.addEventListener("click", () => {
    const k = b.dataset.set, v = b.dataset.val;
    const grp = b.closest(".seg").querySelectorAll("button");
    if (k === "grid" || k === "safe") {                    // independent toggles
      const on = body.dataset[k] === "on";
      body.dataset[k] = on ? "off" : "on";
      b.setAttribute("aria-pressed", String(!on));
    } else {
      body.dataset[k] = v;
      grp.forEach(o => { if (o.dataset.set === k) o.setAttribute("aria-pressed", String(o.dataset.val === v)); });
    }
    if (k === "variant") paint();
  });
});

let toastT;
function toast(msg){
  const el = document.getElementById("toast");
  el.textContent = msg; el.classList.add("on");
  clearTimeout(toastT); toastT = setTimeout(() => el.classList.remove("on"), 1600);
}
function download(name, text){
  const url = URL.createObjectURL(new Blob([text], {type:"image/svg+xml"}));
  const a = document.createElement("a");
  a.href = url; a.download = name; a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

document.addEventListener("click", e => {
  const t = e.target.closest("button"); if (!t) return;
  const theme = HEX[body.dataset.theme], variant = body.dataset.variant;
  if (t.dataset.copy){
    navigator.clipboard.writeText(standalone(t.dataset.copy, variant, theme.t, theme.a))
      .then(() => { t.classList.add("ok"); t.textContent = "Copied"; toast("SVG on your clipboard");
                    setTimeout(() => { t.classList.remove("ok"); t.textContent = "Copy SVG"; }, 1400); });
  }
  if (t.dataset.dl){
    const id = t.dataset.dl;
    download(id + "-" + variant + "-" + body.dataset.theme + ".svg", standalone(id, variant, theme.t, theme.a));
  }
  if (t.dataset.use){
    current = t.dataset.use; paint(); toast(BODIES[current].name + " applied");
    document.getElementById("context").scrollIntoView({behavior:"smooth", block:"start"});
  }
  if (t.dataset.dlbanner){
    const fig = t.closest(".bfig");
    const src = fig.querySelector(".banner");
    const clone = src.cloneNode(true);
    clone.querySelector(".safe")?.remove();
    // Strip the variant that is currently hidden, so the file is not double-drawn.
    clone.querySelectorAll(variant === "mono" ? ".v-accent" : ".v-mono").forEach(n => n.remove());
    clone.removeAttribute("class"); clone.removeAttribute("data-banner");
    clone.setAttribute("width","1584"); clone.setAttribute("height","396");
    const pal = HEX[fig.dataset.themeForce];
    let out = new XMLSerializer().serializeToString(clone);
    for (const k of ["bbg","btext","bmuted","bline","baccent"]) out = out.split("var(--"+k+")").join(pal[k]);
    download("banner-" + t.dataset.dlbanner + "-" + current + ".svg", out);
  }
});

paint();
</script>
</body></html>
"""

page = HTML
for key, val in {
    "@@FIRSTID@@": MARKS[0]["id"],
    "@@FIRSTNAME@@": MARKS[0]["name"],
    "@@COUNT@@": f"{len(MARKS):02d}",
    "@@TITLE@@": COPY[ROUND]["title"],
    "@@SQUINTNOTE@@": COPY[ROUND]["squintnote"],
    "@@AVATARNOTE@@": COPY[ROUND]["avatarnote"],
    "@@HEADER@@": COPY[ROUND]["header"],
    "@@VERDICT@@": COPY[ROUND]["verdict"],
    "@@FONT@@": font_b64,
    "@@CARDS@@": "".join(cards),
    "@@SQUINT@@": squint,
    "@@AVATARS@@": avatars,
    "@@BANNERS@@": banners_html,
    "@@BODIES@@": js_bodies(),
    "@@WMFULL@@": wm_full, "@@WMFULLW@@": f"{wm_full_w:.0f}",
    "@@WMPAWAN@@": wm_pawan, "@@WMPAWANW@@": f"{wm_pawan_w:.0f}",
    "@@BG@@": P["bg"], "@@SURFACE@@": P["surface"], "@@LINE@@": P["line"],
    "@@TEXT@@": P["text"], "@@MUTED@@": P["muted"], "@@ACCENT@@": P["accent"],
    "@@PAPER@@": P["paper"], "@@INK@@": P["ink"], "@@INKMUTED@@": P["ink_muted"],
    "@@INKLINE@@": P["ink_line"], "@@ACCENTINK@@": P["accent_ink"],
}.items():
    page = page.replace(key, val)

PAGE.write_text(page)

# Banner files, using the default mark. The page can export any combination.
for b in BANNERS:
    pal = ("bg", "text", "muted", "line", "accent") if b["theme"] == "dark" \
        else ("paper", "ink", "ink_muted", "ink_line", "accent_ink")
    m = dict(zip(("bbg", "btext", "bmuted", "bline", "baccent"), (P[k] for k in pal)))
    inner = b["svg"]
    inner = inner.replace("__MARK__", render(MARKS[0], "accent", m["btext"], m["baccent"], "bn" + b["id"]))
    inner = inner.replace("__WM__", render(MARKS[0], "mono", m["bline"], m["bline"], "wm" + b["id"]))
    for k, v in m.items():
        inner = inner.replace(f"var(--{k})", v)
    p = BANNER_DIR / f'linkedin-{b["id"]}.svg'
    p.write_text(
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1584 396" width="1584" height="396">{inner}</svg>\n'
    )
    written.append(p)

print(f"palette: {json.dumps(PALETTE, indent=0)}")
print(f"round {ROUND}: wrote {len(written)} svg files + {PAGE.name} ({PAGE.stat().st_size // 1024} KB)")
