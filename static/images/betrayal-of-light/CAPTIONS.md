# Figures for "On the Betrayal of Light"

Two figures, sized for the 1080px reading column and exported at 2x. Both have
transparent backgrounds, so they sit on whichever surface the theme toggle has
chosen rather than carrying a background of their own.

## Figure 1 - the portrait

`fig1-portrait.webp` (498 KB) or `fig1-portrait.png` (3.8 MB), 2160x1440.

Suggested caption:

> The same frame twice. On the left, an SDR image decoded by Windows 11 with the
> sRGB curve; on the right, the same signal through Gloam's gamma 2.2 LUT.
> Nothing is stylized and the candle flames are identical in both: the only
> difference is the decode curve, and 86% of the frame is lifted by more than
> four code values. Detail from Georges de La Tour, *The Penitent Magdalen*,
> ca. 1640, Metropolitan Museum of Art Open Access (CC0).

The pair is not a simulation. Each frame was pushed through the real signal
path, per pixel and per channel: sRGB-encoded source, PQ-encoded at a 200-nit
SDR white level, quantized to the 10-bit wire, the right-hand branch run
through the 1024-entry GPU LUT with the same linear interpolation the hardware
does, then decoded back to light and re-encoded for an ordinary display. The
generator is `scripts/render-comparison-images.py` in the Gloam repo, and its
math is ported 1:1 from the shipping `TransferFunctions.cs` and `LutGenerator.cs`.

## Figure 2 - the two transfer functions

`fig2-transfer.svg` (67 KB) to inline, or `fig2-transfer-dark.webp` /
`fig2-transfer-light.webp` (~70 KB) to embed as an image.

Suggested caption:

> Both curves map the same signal to light. Across the full range they are
> nearly indistinguishable, which is why the mismatch went unnoticed for so
> long. Below about 40% signal the sRGB curve sits above gamma 2.2, and in the
> shadows the gap is enormous: 2.9x the light at 5% signal, 1.6x at 10%, and
> still 1.14x at 20%. The shaded area is the light Windows adds to work that
> was never meant to have it.

Curves are computed from IEC 61966-2-1 and the 2.2 power law, not traced. The
ratios are exact to the figures quoted: 2.866x at 5%, 1.589x at 10%, 1.142x at
20%, reaching unity at about 40%. Regenerate with `python make_chart.py`.

### Embedding

The SVG uses `currentColor` for its ink and `var(--accent)` for the sRGB curve,
so **inlined** it follows the theme toggle with no second file:

```html
<figure>
  <!-- paste the contents of fig2-transfer.svg here -->
  <figcaption>...</figcaption>
</figure>
```

As an `<img>` it cannot see the toggle, so pick by scheme, or just use the dark
one since that is the site default:

```html
<picture>
  <source srcset="/images/betrayal-of-light/fig2-transfer-light.webp"
          media="(prefers-color-scheme: light)">
  <img src="/images/betrayal-of-light/fig2-transfer-dark.webp" alt="...">
</picture>
```

Markdown, matching how the other posts embed:

```markdown
![The same frame twice, decoded by Windows and by Gloam](/images/betrayal-of-light/fig1-portrait.webp)
```

### Alt text

- Figure 1: "A candlelit painting shown twice side by side. The left frame,
  decoded by Windows with the sRGB curve, has a flat gray haze over the
  background and drapery. The right frame, corrected to gamma 2.2, returns the
  same areas to deep black while the candle flames stay identical."
- Figure 2: "Two transfer functions plotted against signal level. Across the
  full range they are nearly identical. Below 40% signal the sRGB curve sits
  above gamma 2.2, emitting 2.9 times the light at 5% signal and 1.6 times at
  10%."
