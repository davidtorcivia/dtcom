"""Emit the transfer-function figure as inline-ready SVG.

Curves are computed from the same functions the app ships:
  sRGB EOTF  - IEC 61966-2-1, ported from src/Gloam.Core/TransferFunctions.cs
  gamma 2.2  - the power law SDR content is mastered against
Ink is currentColor and the highlight is var(--accent), so the figure follows
the page's theme toggle when inlined rather than shipping two files.
"""
import numpy as np

def srgb(s):
    s = np.clip(s, 0, 1)
    return np.where(s <= 0.04045, s / 12.92, ((s + 0.055) / 1.055) ** 2.4)

def g22(s):
    return np.clip(s, 0, 1) ** 2.2

W, H = 1080, 520
PL, PR, PT, PB = 62, 26, 96, 62          # plot padding inside each panel
GAP = 76
PW = (W - GAP) / 2                        # panel width

def panel_x(i):  return i * (PW + GAP)

def path(xs, ys, x0, x1, y0, y1, ox):
    """Map data to a panel's plot box and emit an SVG path."""
    px = ox + PL + (xs - x0) / (x1 - x0) * (PW - PL - PR)
    py = PT + (1 - (ys - y0) / (y1 - y0)) * (H - PT - PB)
    d = [f"M {px[0]:.2f} {py[0]:.2f}"]
    d += [f"L {x:.2f} {y:.2f}" for x, y in zip(px[1:], py[1:])]
    return " ".join(d), px, py

def xy(x, y, x0, x1, y0, y1, ox):
    return (ox + PL + (x - x0) / (x1 - x0) * (PW - PL - PR),
            PT + (1 - (y - y0) / (y1 - y0)) * (H - PT - PB))

s = np.linspace(0, 1, 1400)
out = []
A = f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}"
     role="img" aria-labelledby="tf-title tf-desc" font-family="Inter, system-ui, sans-serif">
  <title id="tf-title">Windows decodes SDR with the sRGB curve; the content was mastered for gamma 2.2</title>
  <desc id="tf-desc">Two transfer functions plotted against signal level. Across the full range they
  are nearly identical. Below about 40 percent signal the sRGB curve sits above gamma 2.2, and in the
  deep shadows it emits far more light: 2.9 times as much at 5 percent signal and 1.6 times at 10 percent.</desc>
  <style>
    .ink   {{ stroke: currentColor; }}
    .inkf  {{ fill: currentColor; }}
    .hot   {{ stroke: var(--accent, #DC2626); }}
    .hotf  {{ fill: var(--accent, #DC2626); }}
    .grid  {{ stroke: currentColor; opacity: .13; stroke-width: 1; }}
    .axis  {{ stroke: currentColor; opacity: .38; stroke-width: 1; }}
    .curve {{ fill: none; stroke-width: 2.25; stroke-linecap: round; stroke-linejoin: round; }}
    .lbl   {{ fill: currentColor; font-size: 12.5px; }}
    .mut   {{ fill: currentColor; opacity: .55; font-size: 11.5px; }}
    .num   {{ font-family: 'JetBrains Mono', ui-monospace, monospace; font-size: 11.5px; }}
    .head  {{ fill: currentColor; font-size: 13px; font-weight: 500; letter-spacing: .01em; }}
    .kick  {{ fill: currentColor; opacity: .5; font-size: 10.5px; letter-spacing: .12em;
              font-family: 'JetBrains Mono', ui-monospace, monospace; }}
  </style>
'''
out.append(A)

for i, (x1v, y1v, head, kick) in enumerate([
        (1.0, 1.0,   "The whole range, where nothing looks wrong", "01 / SIGNAL 0-100%"),
        (0.20, 0.05,  "The shadows, where all of it lives",        "02 / SIGNAL 0-20%")]):
    ox = panel_x(i)
    x0 = y0 = 0.0
    L, R = ox + PL, ox + PW - PR
    T, B = PT, H - PB
    out.append(f'  <g>')
    out.append(f'    <text class="kick" x="{L}" y="{T-34}">{kick}</text>')
    out.append(f'    <text class="head" x="{L}" y="{T-15}">{head}</text>')

    # recessive grid + ticks
    xt = ([0, .25, .5, .75, 1.0] if i == 0 else [0, .05, .10, .15, .20])
    yt = ([0, .25, .5, .75, 1.0] if i == 0 else [0, .01, .02, .03, .04, .05])
    for t in yt:
        _, py = xy(0, t, x0, x1v, y0, y1v, ox)[0], xy(0, t, x0, x1v, y0, y1v, ox)[1]
        out.append(f'    <line class="grid" x1="{L}" y1="{py:.1f}" x2="{R}" y2="{py:.1f}"/>')
        out.append(f'    <text class="mut num" x="{L-9}" y="{py+3.8:.1f}" text-anchor="end">{t*100:g}%</text>')
    for t in xt:
        px = xy(t, 0, x0, x1v, y0, y1v, ox)[0]
        out.append(f'    <text class="mut num" x="{px:.1f}" y="{B+19}" text-anchor="middle">{t*100:g}%</text>')
    out.append(f'    <line class="axis" x1="{L}" y1="{B}" x2="{R}" y2="{B}"/>')
    out.append(f'    <text class="kick" x="{R}" y="{B+38}" text-anchor="end">SIGNAL LEVEL</text>')
    out.append(f'    <text class="kick" transform="translate({L-46},{T}) rotate(-90)" '
               f'text-anchor="end">LIGHT EMITTED</text>')

    m = s <= x1v
    ss, a, b = s[m], srgb(s[m]), g22(s[m])
    if i == 1:   # the light Windows adds, as an area
        pa, _, _ = path(ss, a, x0, x1v, y0, y1v, ox)
        pb, bx, by = path(ss[::-1], b[::-1], x0, x1v, y0, y1v, ox)
        out.append(f'    <path class="hotf" opacity=".11" d="{pa} {pb.replace("M","L",1)} Z"/>')
    da, _, _ = path(ss, b, x0, x1v, y0, y1v, ox)
    db, _, _ = path(ss, a, x0, x1v, y0, y1v, ox)
    out.append(f'    <path class="curve ink" opacity=".9" stroke-width="3.4" d="{da}"/>')
    out.append(f'    <path class="curve hot" stroke-width="2" d="{db}"/>')

    if i == 0:   # show where panel 2 is looking
        rx, ry = xy(0, 0.05, x0, x1v, y0, y1v, ox)
        rw = xy(0.20, 0, x0, x1v, y0, y1v, ox)[0] - L
        out.append(f'    <rect class="hotf" opacity=".07" x="{L:.1f}" y="{ry:.1f}" '
                   f'width="{rw:.1f}" height="{B-ry:.1f}"/>')
        out.append(f'    <rect fill="none" stroke="currentColor" opacity=".45" stroke-dasharray="3 3" '
                   f'x="{L:.1f}" y="{ry:.1f}" width="{rw:.1f}" height="{B-ry:.1f}"/>')
        # label above the box, aligned to its left edge, with a short leader
        # Short, and in the kicker voice, so it echoes the "02 /" heading rather
        # than restating it. A longer caption either ran back over the y axis
        # when centred, or into the curve when left-aligned.
        out.append(f'    <text class="kick" x="{L:.1f}" y="{ry-10:.1f}">PANEL 02</text>')
    else:        # the two numbers the essay quotes
        for sig, mult in [(0.05, "2.9"), (0.10, "1.6")]:
            ax, ay = xy(sig, float(srgb(np.array(sig))), x0, x1v, y0, y1v, ox)
            bx2, by2 = xy(sig, float(g22(np.array(sig))), x0, x1v, y0, y1v, ox)
            out.append(f'    <line class="hot" opacity=".55" x1="{ax:.1f}" y1="{ay:.1f}" x2="{bx2:.1f}" y2="{by2:.1f}"/>')
            out.append(f'    <circle class="hotf" cx="{ax:.1f}" cy="{ay:.1f}" r="3"/>')
            out.append(f'    <circle class="inkf" cx="{bx2:.1f}" cy="{by2:.1f}" r="3" opacity=".85"/>')
            # right-aligned into the empty wedge above the curve, so the text
            # never crosses a mark or the shaded area
            out.append(f'    <text class="lbl num" x="{ax-11:.1f}" y="{ay-9:.1f}" text-anchor="end">{mult}× the light</text>')
            out.append(f'    <text class="mut" x="{ax-11:.1f}" y="{ay+5:.1f}" text-anchor="end">at {sig*100:g}% signal</text>')
    out.append('  </g>')

# one legend, top right, marks carry identity and the text stays ink
out.append(f'  <g transform="translate({PL},22)">')
out.append('    <line class="curve hot" stroke-width="2" x1="0" y1="0" x2="20" y2="0"/>')
out.append('    <text class="lbl" x="28" y="4">Windows: sRGB decode</text>')
out.append('    <line class="curve ink" opacity=".9" stroke-width="3.4" x1="176" y1="0" x2="196" y2="0"/>')
out.append('    <text class="lbl" x="203" y="4">Gamma 2.2, as the work was mastered</text>')
out.append('  </g>')
out.append('</svg>')

open(r'fig2-transfer.svg', 'w', encoding='utf-8').write("\n".join(out))
print('wrote fig2-transfer.svg')
