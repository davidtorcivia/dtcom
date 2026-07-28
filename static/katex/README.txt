KaTeX v0.18.1 — https://katex.org
MIT licensed; see LICENSE in this directory.

Vendored from the upstream release tarball, with two changes:

  1. Only the .woff2 fonts are kept. Upstream ships .ttf and .woff as well,
     which triples the payload for formats no browser in use still needs.
  2. katex.min.css has the matching ttf/woff entries stripped from each
     @font-face src, so nothing points at a file we do not serve.

Only katex.min.js, katex.min.css and fonts/ are vendored — not the contrib
auto-render extension. Math is located by class from static/math.js instead of
by scanning the document for $ delimiters, which would match prose.

To update: download the release tarball from
https://github.com/KaTeX/KaTeX/releases, then re-apply both changes above.
