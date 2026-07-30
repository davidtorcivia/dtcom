# davidtorcivia.com v2 — Design Spec

**Date:** 2026-07-27
**Status:** Approved (with additions noted below)
**Branch target:** master

## Purpose

A fast, static-first personal site for David Torcivia with a single-user admin
backend, an LLM-editable MCP server, a REST API, view-counting dashboard, and
RSS integration — built on the existing brutalist print design system already
in the repo.

## Decisions locked during brainstorming

| Decision | Choice |
|---|---|
| Hosting | Local Linux server, Docker, accessible via Cloudflare Tunnel |
| Runtime | Go (single binary) |
| Content store | Hybrid: markdown files are source of truth; SQLite is derived index + structured data |
| Public-site architecture | Static site generator pattern — pre-built HTML served as static files |
| Markdown editor | Markdown textarea with toggle-to-preview (Obsidian-style, not dual-pane) |
| RSS import | Automatic background polling |
| Admin auth | Password + TOTP 2FA, session cookie |
| Markdown flavor | GFM + `==highlight==` and footnotes |
| Images | Both local uploads (auto-resize) and external URLs |
| Search scope | Articles + links |
| Scope at launch | Core + dashboard + MCP + REST |

## Additions made during design approval

1. **MCP and REST cover site structure too** — bio, nav, and social links
   (sourced from `content/site.yml`) are read/write via the API and MCP, not
   just articles and links. Writes go to `site.yml` and trigger a rebuild.
2. **Markdown variants of pages for agents** — every article is also served
   as raw markdown at `/posts/<slug>.md`, via content negotiation on
   `Accept: text/markdown`, and advertised in the HTML head with
   `<link rel="alternate" type="text/markdown" href="...">`. Markdown
   variants apply only to markdown-sourced pages (articles); home, links,
   and search are not markdown-sourced and do not get `.md` variants.

---

## 1. Architecture

A single Go binary (`dtcom`) runs three jobs in one process:

- **Public site** — serves pre-built static HTML from `public/` via
  `http.FileServer`. Only two dynamic public endpoints: the search API
  (SQLite FTS5) and the view-tracking beacon.
- **Admin** (`/admin/*`) — login (password + TOTP), markdown editor,
  links manager, site-config editor, dashboard, manual regenerate.
- **MCP + REST** (`/mcp`, `/api/v1/*`) — bearer-token authenticated
  interfaces for LLM-driven and curl-driven editing of all site content.

**Build pipeline.** Markdown files and `site.yml` are the source of truth.
On startup and on any content change, the binary:

1. Parses markdown + frontmatter → renders HTML into `public/`
2. Reads `site.yml` → injects bio/nav/social into the home template
3. Reads links from SQLite → renders `public/links/index.html`
4. Updates the SQLite FTS5 index for articles + links
5. Regenerates `feed.xml`, `sitemap.xml`

Public traffic never runs rendering logic.

---

## 2. Content storage model

| Content | Source of truth | Storage | In git |
|---|---|---|---|
| Articles (markdown + frontmatter) | Authored long-form | `content/posts/*.md` | Yes |
| Site identity, bio, nav, social links, RSS feed list | Static config | `content/site.yml` | Yes |
| Custom links | Structured records | SQLite `links` table | No |
| RSS-imported links | Dynamic, polled | SQLite `links` table | No |
| View counts | Ephemeral derived data | SQLite `views` table | No |
| Search index | Derived | SQLite FTS5 virtual table | No |
| Uploaded images | User content | `data/images/` (Docker volume) | No |
| Generated public HTML | Derived | `public/` | No (gitignored) |
| SQLite database | Derived + structured | `data/dtcom.db` | No |

The SQLite DB and uploaded images live in a Docker volume for backup; they are
not committed. Articles and `site.yml` are committed for readable history.

### `content/site.yml` schema

```yaml
title: David Torcivia
author: David Torcivia
base_url: https://davidtorcivia.com
description: Work, art, writing, and research from David Torcivia.
bio:
  - "nyc based colorist, photographer, artist, swe, researcher, and writer."
  - "Color work available at color.davidtorcivia.com, photos at photo.davidtorcivia.com..."
nav:
  - { label: Search, href: "/search" }
  - { label: Links,  href: "/links"  }
social:
  - { label: X,          href: "https://x.com/davidtorcivia",          icon: x }
  - { label: Instagram,  href: "https://instagram.com/davidtorcivia",  icon: instagram }
  - { label: GitHub,     href: "https://github.com/dtorcivia",         icon: github }
  - { label: Substack,   href: "https://substack.com",                 icon: substack }
  - { label: Contact,    href: "mailto:david@davidtorcivia.com",       icon: email }
rss_feeds:
  - { url: "https://davidtorcivia.substack.com/feed", label: Substack, enabled: true }
footer_left:
  - "DAVID TORCIVIA 2026"
  - "NYC"
```

SVG icons for social links are kept as inline templates keyed by `icon` name,
preserving the existing crisp vector SVG aesthetic from the scaffold.

---

## 3. Repository layout

```
dtcom/
├── content/                    # source of truth (committed)
│   ├── site.yml
│   └── posts/
│       └── 2026-01-31-schedlock.md
├── static/                     # immutable assets (committed)
│   ├── style.css               # existing brutalist stylesheet, kept & extended
│   ├── app.js                  # theme toggle + search + view beacon, extended
│   └── images/                 # default OG image, favicon
├── public/                     # GENERATED, gitignored
├── data/                       # SQLite DB + uploaded images (volume, gitignored)
│   └── dtcom.db
├── templates/                  # Go html/template (committed)
│   ├── layout.html
│   ├── home.html
│   ├── article.html
│   ├── links.html
│   └── search.html
├── internal/                   # Go source (committed)
│   ├── main.go
│   ├── build/                  # markdown→HTML, frontmatter, image processing
│   ├── store/                  # SQLite: articles index, links, views, FTS5
│   ├── server/                 # http handlers: public, admin, api
│   ├── mcp/                    # MCP server (Streamable HTTP)
│   ├── auth/                   # password + TOTP + session cookie
│   └── feeds/                  # RSS poller + feed.xml/sitemap.xml generation
├── Dockerfile
├── docker-compose.yml
├── go.mod
└── docs/
```

---

## 4. Article format (markdown + YAML frontmatter)

```markdown
---
title: SchedLock
date: 2026-01-31
slug: schedlock                  # optional; derived from filename if absent
description: Short SEO/summary…
tags: [ai, security]
cover: images/schedlock.jpg      # optional; local or external URL
draft: false
---

You may have noticed the Cambrian explosion…
```

Markdown rendered by **Goldmark** with extensions: GFM tables, strikethrough,
linkify, footnotes, task lists, auto-heading-IDs, plus a custom `==highlight==`
renderer (the scaffold uses `<mark>`). Code blocks get the language badge the
scaffold already styles (`.code-type-badge`).

---

## 5. Public pages & routing

| Route | Source | Notes |
|---|---|---|
| `/` | rendered `public/index.html` | bio + article index (existing design) |
| `/posts/<slug>` | rendered per-article HTML | clean URLs |
| `/posts/<slug>.md` | raw markdown (frontmatter + body) | for agents |
| `/links` | rendered `public/links/index.html` | custom + RSS links merged, date-sorted |
| `/search` | static shell + JS hitting `/api/search` | |
| `/feed.xml`, `/sitemap.xml`, `/robots.txt` | generated | |
| `/static/*` | immutable assets | long cache headers |
| `/images/*` | uploaded images from `data/images/` | |
| `/api/search` | dynamic (SQLite FTS5) | JSON, unauthed read-only (public search box) |
| `/api/track` | dynamic | view beacon, no cookies, unauthed |

> **Note on `/api/search` vs `/api/v1/*`:** The public search box needs an
> unauthed read endpoint, so it lives at `/api/search` (no `/v1`, no auth).
> The authed REST API under `/api/v1/*` does **not** duplicate search — MCP's
> `search_articles` tool hits the same FTS5 backend internally. This keeps one
> unauthed read path and one authed write surface, no collision.

All public pages inherit the existing brutalist design system (Inter / JetBrains
Mono / Newsreader, yellow accent, light/dark/auto theme). The current `style.css`
and `app.js` are kept and extended, not rewritten. The current `index.html` and
`article.html` become the templates the renderer fills.

### Markdown variants & content negotiation

- `GET /posts/<slug>.md` → serves the raw source file (frontmatter + body) with
  `Content-Type: text/markdown; charset=utf-8`.
- `GET /posts/<slug>` with `Accept: text/markdown` → same raw markdown, served
  dynamically (a thin handler reads the source file).
- HTML response advertises the markdown variant via
  `<link rel="alternate" type="text/markdown" href="/posts/<slug>.md">` in `<head>`.
- `robots.txt` does not block `.md` variants, so agents can discover them.

---

## 6. Admin (`/admin/*`)

- `/admin/login` — password + TOTP code; sets signed session cookie
- `/admin` — dashboard: total views, views/article, views/day
- `/admin/posts` — list / create / edit / delete articles
- `/admin/posts/new`, `/admin/posts/<slug>/edit` — **Obsidian-style editor**:
  single markdown textarea with a toggle button that switches the same pane
  to rendered preview (not dual-pane). Frontmatter edited as raw YAML above
  the body.
- `/admin/links` — list / add / remove custom links; view RSS-imported links
  (read-only, refreshed by poller)
- `/admin/site` — edit bio, nav, social, RSS feeds, footer (writes to `site.yml`)
- `/admin/regenerate` — force a full rebuild

Admin HTML is server-rendered with `html/template`, minimal JS, no build step.

**Auth.** Password (bcrypt-hashed, from env `DTCOM_ADMIN_PASSWORD_HASH`) + TOTP
via `github.com/pquerna/otp` (secret from env `DTCOM_TOTP_SECRET`, set up once).
Session cookie HMAC-signed (secret from `DTCOM_SESSION_KEY`). Cookie is
`HttpOnly`, `Secure` (behind Cloudflare Tunnel TLS), `SameSite=Lax`. Default
session TTL 7 days, sliding renewal.

---

## 7. MCP server (`/mcp`)

Streamable HTTP transport (current MCP spec), so a remote LLM client connects
over the tunnel. Bearer-token auth (token from `DTCOM_API_TOKEN`, shared with
REST API). Built on `github.com/modelcontextprotocol/go-sdk`.

*Amended 2026-07-30:* the server was moved to the `2026-07-28` spec revision,
which removed the `initialize` handshake and protocol-level sessions. The
endpoint is now sessionless — each request carries its own protocol version in
`_meta` — while older clients back to `2024-11-05` are still answered on the
same URL. See decision 2 under Open items for why the library changed.

Tools exposed:

**Articles**
- `list_articles` → list slugs, titles, dates, draft status
- `get_article` (slug) → frontmatter + markdown body
- `create_article` (frontmatter, body) → writes file, rebuilds
- `update_article` (slug, frontmatter?, body?) → updates file, rebuilds
- `delete_article` (slug) → removes file, rebuilds
- `search_articles` (query) → FTS5 search results

**Links**
- `list_links` → all custom + RSS links
- `add_link` (label, href, note?) → inserts to SQLite, rebuilds links page
- `remove_link` (id) → removes from SQLite, rebuilds links page

**Site structure** (from `site.yml`)
- `get_site` → returns the full parsed `site.yml` (bio, nav, social, feeds, footer)
- `update_bio` (bio[]) → updates bio section, rewrites `site.yml`, rebuilds
- `update_nav` (nav[]) → updates nav
- `update_social` (social[]) → updates social links
- `update_rss_feeds` (rss_feeds[]) → updates RSS feed list, restarts poller

**Operations**
- `regenerate` → force full rebuild
- `get_stats` → view counts from dashboard

Every write tool writes the canonical source (file or DB), then triggers the
same rebuild path as the admin UI — a single full rebuild (see §15). No
content-type-specific bypass paths.

---

## 8. REST API (`/api/v1/*`)

Bearer-token auth (same `DTCOM_API_TOKEN`). Endpoints mirror MCP tools:

**Articles**
- `GET    /api/v1/articles`          → list
- `GET    /api/v1/articles/<slug>`   → one (JSON: frontmatter + body)
- `POST   /api/v1/articles`          → create
- `PUT    /api/v1/articles/<slug>`   → update
- `DELETE /api/v1/articles/<slug>`   → delete

**Links**
- `GET    /api/v1/links`
- `POST   /api/v1/links`
- `DELETE /api/v1/links/<id>`

**Site structure** (read/write to `site.yml`)
- `GET /api/v1/site`                 → full parsed site config
- `PUT /api/v1/site/bio`
- `PUT /api/v1/site/nav`
- `PUT /api/v1/site/social`
- `PUT /api/v1/site/rss_feeds`       → also restarts poller

**Operations**
- `POST /api/v1/regenerate`
- `GET  /api/v1/stats`
- `POST /api/v1/feeds/refresh`       → force-fetch RSS feeds now

Every write operation writes the canonical source (file or DB), then triggers
a full rebuild (see §15 — full rebuild is sub-second at this scale, so there is
no incremental-rebuild code path to maintain).

---

## 9. RSS

- **Outbound** (`/feed.xml`): your articles, generated at build time. Standard
  RSS 2.0.
- **Inbound**: background poller (interval from `DTCOM_RSS_INTERVAL`,
  default 30 min) reads each feed in `site.yml` via
  `github.com/mmcdole/gofeed`, stores new items in the `links` table flagged
  `source='rss'`, and they flow into `/links`. De-dup by link URL.
  `POST /api/v1/feeds/refresh` and `update_rss_feeds` MCP tool force a fetch.

---

## 10. SEO / OG

Per-page: `<title>`, `<meta description>`, canonical, `og:title/description/
url/type/image`, `twitter:card`, and JSON-LD `BlogPosting` schema on articles.
OG image = article `cover` if set, else `static/images/og-default.jpg`.
`sitemap.xml` lists all public pages; `robots.txt` points to it and disallows
`/admin`, `/api`, `/mcp`. Markdown variants are allowed in `robots.txt` so
agents can discover them.

---

## 11. Images

- **Local uploads** → `data/images/`, referenced in markdown as
  `![alt](/images/foo.jpg)`. At build, generate a max-1600px-wide resized
  variant; serve original format (JPEG/PNG). WebP deferred — a clean
  pure-Go encoder isn't available, and a CGO/libwebp dependency is not
  worth the Docker complexity for v2.
- **External URLs** → used as-is, no processing.
- Article `cover` (local or external) → OG image.

---

## 12. Go dependencies (kept minimal)

- `goldmark` — markdown rendering (GFM + extensions)
- `modernc.org/sqlite` — pure-Go SQLite, **no CGO** (clean Docker builds, FTS5 included)
- `github.com/mmcdole/gofeed` — RSS parsing
- `github.com/pquerna/otp` — TOTP
- `github.com/modelcontextprotocol/go-sdk` — MCP server
- `gopkg.in/yaml.v3` — `site.yml` parsing
- `golang.org/x/crypto/bcrypt` — password hashing
- stdlib `net/http` (Go 1.22+ routing), `log/slog`, `html/template`

No web framework, no JS framework, no frontend build step.

---

## 13. Docker & deployment

`Dockerfile` (multi-stage): build the Go binary in a `golang` stage, copy to
a scratch/distroless final image. `docker-compose.yml` mounts two volumes —
`./content` + `./static` from git, `./data` for the DB + uploaded images —
and exposes one port that Cloudflare Tunnel points at.

Configuration via env vars in compose:

| Env var | Purpose |
|---|---|
| `DTCOM_BASE_URL` | Canonical site URL (for SEO, RSS, OG) |
| `DTCOM_ADMIN_PASSWORD_HASH` | bcrypt hash of admin password |
| `DTCOM_TOTP_SECRET` | TOTP shared secret (set up once) |
| `DTCOM_SESSION_KEY` | HMAC key for signing session cookies |
| `DTCOM_API_TOKEN` | Bearer token shared by MCP + REST |
| `DTCOM_RSS_INTERVAL` | RSS poll interval (default `30m`) |
| `DTCOM_LISTEN_ADDR` | Listen address (default `:8080`) |

Zero external services required beyond the box itself.

---

## 14. View tracking

The public beacon `POST /api/track` (unauthed, no cookies) accepts `{ path }`
from a tiny JS snippet included on every public page. The handler:

- Ignores bots (matches a small User-Agent blocklist) and `OPTIONS`/preflight
- Writes one row to `views` per real page hit, keyed by path + day
- De-dups obvious refreshes within a short window (e.g. same path + IP hash
  within 60s counted once)

`GET /api/v1/stats` (authed) and the dashboard aggregate `views` into:

- Total views (all-time)
- Views per article (top N)
- Views per day (last 30 days, for a sparkline)

No per-visitor tracking, no cookies set on visitors, no PII stored beyond a
hashed IP for short-window de-dup (not retained beyond the de-dup window).

---

## 15. Rebuild model

**Full rebuild only — no incremental path.** A rebuild:

1. Walks `content/posts/*.md`, parses each, renders HTML to `public/posts/<slug>/index.html` and copies `.md` to `public/posts/<slug>.md`
2. Re-renders `public/index.html` (home) from `site.yml` + article list
3. Re-renders `public/links/index.html` from SQLite links
4. Reindexes all articles + links into FTS5 (drop + rebuild)
5. Regenerates `feed.xml`, `sitemap.xml`

At the expected scale (dozens of articles), a full rebuild is sub-second, so
incremental dependency tracking would add complexity for no gain. The rebuild
runs on startup, on every content change (file watcher, admin save, API/MCP
write), and on manual `/admin/regenerate`. Rebuilds are serialized via a
single mutex; concurrent triggers coalesce.

---

## 16. Design system preservation

The existing brutalist design is preserved wholesale:

- Fonts: Inter (sans), JetBrains Mono (mono), Newsreader (serif)
- Colors: yellow `#FACC15` accent, red `#DC2626`/`#FF453A` secondary
- Layout: 1080px container, `::` index separator, crisp vector SVG icons
- Light / dark / auto theme toggle persisted in `localStorage`
- Fully responsive (table card-stacking on mobile, etc.)

The existing `style.css` and `app.js` are kept and extended. The existing
`index.html` and `article.html` are converted into Go `html/template` files
that fill the same DOM structure.

---

## Open items & judgment calls

1. **Custom links in SQLite** (not `content/links.yml`). Chosen because
   RSS-imported links are inherently dynamic and the API/MCP manage them.
   Easy to revisit.
2. **MCP library** is `modelcontextprotocol/go-sdk` (*amended 2026-07-30*;
   was `mark3labs/mcp-go`). The `2026-07-28` spec revision is a large enough
   break — sessionless lifecycle, `server/discover`, standard request headers,
   cacheable list results — that following it means following whichever library
   the spec authors ship it in. `mark3labs/mcp-go` was still on `2025-11-25` at
   v0.57.0 with no dated plan to move. Could hand-roll if a dependency-free
   core is desired, but that is now considerably more work than it was.
3. **WebP deferred** — no clean pure-Go encoder. Revisit if/when one ships.
4. **pure-Go `modernc.org/sqlite`** chosen over CGO `mattn/go-sqlite3`
   for clean Docker builds. Marginal performance cost is irrelevant for a
   single-user site.
5. **Go floor raised to 1.25.5** (was 1.22 in the original spec) because
   `modernc.org/sqlite` v1.54+ requires it. Dockerfile uses
   `golang:1.25-alpine`. No downside for a single-user Docker deployment.

---

## Deferred follow-ups (from final code review, post-implementation)

These were identified in the final cross-cutting review. None block merge;
tracked here so they aren't lost.

- **`build.ResizeImage` is implemented + tested but not wired into the
  rebuild pipeline.** Spec §11 envisioned scanning article markdown for
  local image refs, resizing into a cache, and rewriting refs. Currently
  local uploads ship at full size via the `/images/` file server. Either
  wire it in or remove the dead code — leaving it advertising a feature
  the site doesn't have is the worst option.
- **`internal/main.go` lives under `internal/` as `package main`.** Works
  (`go build ./internal`) but the Go convention is `cmd/<name>/main.go`.
  Cosmetic; defer unless other binaries are added.
- **Watcher doesn't add new subdirectories** created after startup. Fine
  for the current flat `content/posts/*.md` layout; revisit if articles
  get nested (`content/posts/drafts/`).
- **`http.FileServer` enables directory listing** for `/static/` and
  `/images/`. Harmless but unintentional; suppress with a custom handler
  if exposure of the upload dir matters.
- **Session cookie `Secure: true`** blocks local non-HTTPS dev (cookie
  not sent over `http://localhost`). Correct for production; document the
  TLS-proxy requirement for local admin testing.
- **`findArticleBySlug` loads + parses every article on each single-read
  request.** Fine at dozens-of-articles scale; first performance
  bottleneck if the corpus grows.
- **TOTP has no documented recovery path** (no backup codes, no bypass).
  Clock drift > 30s locks out the admin. Document a recovery procedure
  (e.g. temporarily unset `DTCOM_TOTP_SECRET` to skip the second factor).

---

## Success criteria

- `docker compose up` brings the site live on the configured port
- Home page renders the existing brutalist design, populated from markdown
  articles and `site.yml`
- Each article renders full typography (h2, lists, blockquote, table, code
  block with language badge) and is also available as `.md`
- `/admin` allows login (password + TOTP), editing articles in markdown with
  Obsidian-style toggle preview, editing links, editing site config
- Dashboard shows view counts (total, per-article, per-day)
- `/api/v1/*` and `/mcp` both allow an LLM (or curl) to create/update/delete
  articles, links, and site structure, with each write triggering a full
  rebuild that regenerates all affected pages + reindexes search
- Background RSS poller pulls configured feeds into `/links`
- `/feed.xml`, `/sitemap.xml`, `/robots.txt` generated correctly
- Per-page SEO/OG tags present, JSON-LD `BlogPosting` on articles
- No React, no Python, no frontend build step
