# Fonts

The site's typography is self-hosted from `static/fonts/`. It used to be an
`@import` from `fonts.googleapis.com`.

Three reasons it moved:

- **Speed.** The `@import` sat inside `style.css`, so the browser had to fetch
  and parse the stylesheet before it even learned the font URLs, then open a
  connection to a second origin. Self-hosted files are same-origin and
  discovered immediately.
- **Privacy.** Every visitor's IP reached Google on every page load.
- **CSP.** The policy needed `https://fonts.googleapis.com` in `style-src` and
  `https://fonts.gstatic.com` in `font-src`. Both exceptions are gone; every
  source is now `'self'` apart from `img-src`.

## What is shipped

One file per family and style, latin and latin-ext only:

| File | Family | Weights |
|---|---|---|
| `inter-normal-{latin,latin-ext}.woff2` | Inter | 100–900 |
| `jetbrainsmono-normal-{latin,latin-ext}.woff2` | JetBrains Mono | 100–800 |
| `newsreader-normal-{latin,latin-ext}.woff2` | Newsreader | 200–800 |
| `newsreader-italic-{latin,latin-ext}.woff2` | Newsreader italic | 200–800 |

These are **variable** fonts: one file covers the whole weight range. The first
attempt at this downloaded a file per weight and got four byte-identical copies
of Inter — Google serves the same variable file for every weight you ask for.
Requesting explicit ranges (`wght@100..900`) makes that obvious in the CSS it
returns.

The `unicode-range` on each `@font-face` means a browser downloads `latin-ext`
only if the page actually uses a codepoint from it. Total on disk is ~635 KB;
a typical English page pulls ~220 KB of it.

Newsreader's italic is a real italic, not a synthesised oblique. Body prose sits
at weight 500, and without the matching italic face the browser would slant the
roman for every `<em>` and for the blockquote rule.

## Refreshing them

```bash
curl -s -A "Mozilla/5.0 (Chrome/120)" \
  "https://fonts.googleapis.com/css2?family=Inter:wght@100..900&display=swap"
```

That returns one `@font-face` block per subset, each preceded by a
`/* subset */` comment. Keep the `latin` and `latin-ext` blocks, download the
`.woff2` each one points at, and rewrite the `src:` to `/static/fonts/<file>`.
The rules live at the top of `static/style.css`.

The other two families use:

```
family=JetBrains+Mono:wght@100..800
family=Newsreader:ital,opsz,wght@0,6..72,200..800;1,6..72,200..800
```

## The OG card fonts are separate

`internal/build/ogfonts/` holds `Inter-Regular.ttf` and `Inter-Bold.ttf`,
embedded into the binary with `go:embed` and used to draw social preview cards.

They are duplicated rather than shared with the web fonts for two reasons:
`x/image` can only parse raw TrueType, not woff2, and it has no support for
variable-font axes — so a static instance per weight is required. They come
from the same Google Fonts CDN, latin-subset, ~66 KB each:

```bash
curl -s -A "Mozilla/4.0" "https://fonts.googleapis.com/css?family=Inter:400,700"
```

An old `User-Agent` is what makes the API serve `.ttf` instead of `.woff2`.

Embedding matters: the runtime image is distroless and has no system fonts at
all, so anything read from disk at render time would not exist in production.

## Licensing

Inter, JetBrains Mono, and Newsreader are all under the SIL Open Font License
1.1. The full text and each family's copyright line are in
`static/fonts/OFL.txt`, which is served with the site — the OFL requires the
notice to travel with the fonts.
