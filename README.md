# dtcom

A single-binary website for a single author. Markdown files are the source of truth; a Go binary renders them to HTML, indexes them into SQLite for search, exposes a REST API and an MCP server for programmatic editing, and serves everything on port 8080. Built to be edited by hand, through a browser, or by an LLM — all three paths write to the same files.

## Architecture

```
                         ┌─────────────┐
   content/posts/*.md ──▶│   engine    │──▶ public/*.html  (served read-only)
   content/site.yml   ──▶│  (goldmark) │──▶ feed.xml, sitemap.xml, 404.html
                         └──────┬──────┘
                                │ indexes
                                ▼
                         ┌─────────────┐    full-text     ┌──────────────┐
                         │   SQLite    │◀────────────────▶│  /api/search │
                         │  (search +  │   view beacons   │  /api/track  │
                         │  views +    │                  └──────────────┘
                         │  links)     │
                         └──────┬──────┘
                                │
              ┌─────────────────┼──────────────────┐
              ▼                 ▼                  ▼
        /admin (UI)       /api/v1 (REST)      /mcp (MCP)
       password + TOTP    bearer token        bearer token
              │                 │                  │
              └─────────────────┴──────────────────┘
                                │
                    fsnotify watcher → rebuild
```

One process. One binary (pure Go, CGO disabled — `modernc.org/sqlite` provides the SQLite driver). The file watcher debounces changes under `content/` and rebuilds in the background, so dropping a markdown file in `content/posts/` is enough to publish it.

A rebuild writes the new output first and prunes stale files afterwards, so every page stays readable while it runs — a rebuild fires on each RSS poll and each admin save, and the site must not blink out during them.

## Quickstart

```bash
cp .env.example .env
# fill in the five required secrets (see .env.example for how to generate each)
docker compose up -d
```

The site is now on `http://localhost:8080`. Put it behind Cloudflare Tunnel (or any reverse proxy) by pointing the tunnel at `localhost:8080`; nothing in dtcom terminates TLS itself. The compose file binds the published port to loopback for exactly that reason — set `DTCOM_BIND=0.0.0.0` if you really want it reachable directly.

`GET /healthz` answers for a proxy or orchestrator health check.

**Behind a proxy, set `DTCOM_TRUST_PROXY=true`** (the compose file defaults it on). Without it the server sees only the proxy's address, so every visitor shares one view-dedup bucket and one rate-limit bucket. With it set while the port is exposed directly, any client can forge `X-Forwarded-For` and evade both — so only turn it on when a proxy is genuinely in front.

The process refuses to start on a weak configuration: the password must be a bcrypt hash, the session key at least 32 characters, the API token at least 24, the TOTP secret valid base32, and the base URL an absolute http(s) URL.

## Adding content

Four ways, all writing to the same place.

**Markdown file.** Drop `YYYY-MM-DD-my-post.md` into `content/posts/` with frontmatter:

```markdown
---
title: My Post
date: 2026-07-27
description: One line.
tags: [essay, color]
cover: /images/optional-social-preview.jpg
---

Body text in markdown.
```

The watcher rebuilds once writes settle (~500ms). Drafts (`draft: true`) are excluded from the build, and un-drafting or deleting a post removes its published pages on the next rebuild.

Beyond GFM, a post body gets footnotes (`[^1]`), `==highlight==`, syntax-highlighted code fences, LaTeX math (`$inline$` and `$$display$$`, typeset by KaTeX, loaded only on pages that have some), and figures:

```markdown
![A caption](/images/chart.png)

![The transfer function](/images/fig-light.png#light)
![The transfer function](/images/fig-dark.png#dark)
```

An image alone in its paragraph becomes a `<figure>` and its alt text becomes the visible caption — markdown has no caption syntax, and alt is the one place a description already belongs. An image used mid-sentence is left as prose.

Each stored image also gets a set of renditions — 480, 768, 1080, 1440 and 2048 pixels wide — and a post references all of them at once through `srcset`, so a phone fetches a copy cut for a phone. Renditions of a lossless master are offered as WebP as well, through a `<picture>`, and the browser takes whichever it can read. Photographs skip WebP: lossless WebP of a photograph is several times the size of the same JPEG, and this pipeline has no lossy WebP, because a lossy encode resamples the colour beneath every partly-transparent pixel and these figures have transparent backgrounds.

An image with more resolution than the column shows becomes clickable and opens full size in a lightbox — measured against the master, not against the rendition on the page, since the rendition is by design the one that fits. It opens on the copy the page already had, so the picture is there in the same frame as the dialog, and the master is fetched behind it and swapped in when it arrives. Pinch, double-tap and the scroll wheel zoom; the zoom is baked into the element's layout size once a gesture settles, which is what makes a magnified picture redraw sharp instead of stretching. It is a `<dialog>`, so Escape and the backdrop are the browser's job.

Tag a pair of URLs `#light` and `#dark` and they collapse into a single figure that swaps with the theme. The swap is CSS keyed on `data-theme`, not a `<picture>` element with `prefers-color-scheme`: that media query follows the operating system only, so it would ignore the toggle in the site header. Both files are in the markup and the browser fetches both, which is the cost of honouring a manual toggle.

**Admin UI.** `/admin` — log in with the bcrypt password and a TOTP code. The post editor has an Obsidian-style Write/Preview toggle, and takes images by button, paste, or drag-and-drop; each raster upload is re-encoded (which strips EXIF), downscaled to 2560px, stored under a content-derived name, and cut into the renditions described above. An upload that carries transparency is kept lossless whatever it arrived as — the decoded pixels decide that, not the file extension. SVG is accepted too and stored as written — there is nothing to resample in a vector — after being validated as a real SVG document.

**Dashboard.** `/admin` — views over time, and what got read. Both panels take a range (7 days, 30, 90, 12 months, all time) and the two are independent, so you can hold a year of traffic beside last week's most-read list. The chart draws one bar per day up to about three months and switches to one per month past that; hovering a bar names the day and its count. Every day in the window gets a column, including the ones nobody visited — grouping in SQL returns only the days that have a row, which made a month with five busy days render as five bars side by side and read as a steady week.

**Backups.** `/admin/backups` — `content/` and `data/` are not in git, by design, so the machine the site runs on holds the only copy of every post and every picture. An archive of all of it, plus a consistent snapshot of the database, is taken daily and on demand, and the last 30 are kept.

An archive is only written when something has actually changed. Copying a site identical to the one already saved protects nothing, and under a keep-the-last-30 rule it would push a genuinely different state off the end. Page views do not count as a change; posts, images, `site.yml`, links and tokens do.

Uploaded pictures are named after a hash of their own bytes and are never rewritten, so they are stored once beside the archives and shared by every archive that refers to them — an archive is a few hundred KB rather than the megabytes its images weigh, and on one filesystem the shared copy is a hard link and costs nothing at all. A download is reassembled whole, images and all, because that is the copy meant to survive the disk. Renditions and `public/` are in neither place: both are regenerated on restore.

Restoring is on the same page, behind typing the archive's date, and takes a fresh archive of the current state first — so a restore chosen in error is itself undoable. `DTCOM_BACKUP_DIR` moves the archives to another mount, `DTCOM_BACKUP_KEEP` changes how many are kept, and `DTCOM_BACKUP_INTERVAL=0` leaves them to be taken by hand.

**API / MCP.** See the two sections below. These are the paths an LLM client uses. `/admin/integrations` shows the live token, a ready-to-paste MCP client config, and the full endpoint list.

## Site configuration

`content/site.yml` holds everything that is not a post: title, author, bio paragraphs, nav links, social links, the inbound RSS feed list, the footer text, and how `/links` renders (`links_style: full` shows each entry's summary line, `minimal` is date :: title only). Edit the file directly (the watcher picks it up), or through the admin Site page, the admin Links page (for feeds), or the `get_site` / `update_*` MCP tools. The schema matches the YAML exactly — one key per section, no nesting beyond what the file shows.

The canonical URL is the one exception: `DTCOM_BASE_URL` wins over `site.yml`'s `base_url`, so the feed, sitemap, and OG tags can't disagree with the deployment.

### Analytics

An optional third-party tracker, set on the admin Site page or in `site.yml` directly. Umami, Plausible, Fathom and GoatCounter are all one script tag with some `data-*` attributes, so that is what the config models rather than a list of providers:

```yaml
analytics:
  script_url: https://analytics.example.com/script.js
  data:
    website-id: 0e1f…
```

That renders `<script defer src="…" data-website-id="0e1f…"></script>` into every public page. Two things happen for you. The script's origin is added to the site's `script-src` and `connect-src` — the policy is otherwise `'self'`, so without that the browser would refuse to run the tag and the tracker would record nothing while looking correctly configured. And `/admin` keeps the strict policy regardless: the tag only ever goes on public pages, so there is no reason to let third-party script run on the authenticated surface.

A `script_url` that isn't `http(s)` is refused on save, as is an attribute name containing anything but letters, digits and dashes — that name is written straight into the tag and, unlike a value, cannot be escaped after the fact.

The built-in view counter keeps running alongside it. They are not measuring the same thing: this one counts one view per page, per address, per day, excludes bots and your own logged-in reads, and stores only a keyed hash of the address. A tracker sees sessions and referrers. The dashboard says which numbers it is showing.

## API tokens

Two kinds of bearer credential authenticate the REST API and MCP server, and both are accepted everywhere:

- **The bootstrap token**, `DTCOM_API_TOKEN` from the environment. It cannot be revoked from the admin UI on purpose — it is the way back in if a managed token is withdrawn by mistake. Rotate it by changing the variable and restarting.
- **Managed tokens**, minted on `/admin/integrations`. Give each client its own — an MCP config, a script, a phone shortcut — so one can be revoked without disturbing the others. Only a SHA-256 digest is stored, so a copy of the database yields no usable credentials, and a token's value is shown exactly once, at creation. The admin page lists each token's name, prefix, creation date, and last use.

## MCP integration

The `/mcp` endpoint speaks MCP over Streamable HTTP with bearer-token auth (the same tokens the REST API uses), at the `2026-07-28` revision of the spec. Point any MCP client at it:

```json
{
  "mcpServers": {
    "dtcom": {
      "url": "https://your-site.example/mcp",
      "headers": { "Authorization": "Bearer <DTCOM_API_TOKEN>" }
    }
  }
}
```

For Claude Desktop, drop that block into `claude_desktop_config.json`. The transport is HTTP, not stdio — there is no `command`.

Seventeen tools are exposed, grouped by what they touch:

| Group | Tools |
| ------- | ------- |
| Articles | `list_articles`, `get_article`, `create_article`, `update_article`, `delete_article`, `search_articles` |
| Links | `list_links`, `add_link`, `remove_link` |
| Site | `get_site`, `update_bio`, `update_nav`, `update_social`, `update_rss_feeds` |
| Ops | `regenerate`, `get_stats`, `refresh_feeds` |

`/admin/integrations` renders the config block above with your actual URL and a token, ready to copy.

Every write tool saves to disk and triggers a rebuild, so a `create_article` call is fully published by the time it returns. `update_article` distinguishes an omitted field from an empty one — omit a key to keep it, pass `""` to clear it.

**Each tool says what it does to the site.** Six are marked read-only, two destructive (`delete_article`, `remove_link`), the rest as writes that take nothing away — with idempotency where it holds, so a client knows that replacing the bio twice is the same as replacing it once and that adding a post twice is not. `refresh_feeds` is the only one that contacts anybody else's server. These are the hints a client consults before deciding whether to ask you first, and they have to be stated: `destructiveHint` and `openWorldHint` both default to *true*, so a server that says nothing is claiming every tool might wreck something.

**Answers are structured.** Every tool publishes an output schema and returns `structuredContent` validated against it, with the same JSON in a text block for older clients. So a client gets `{"slug": "…", "status": "created"}` as data, not as prose to be parsed.

**Posts are also resources.** Each one is addressable as `dtcom://article/<slug>`, listed with its title and description, and reads back as the markdown source — frontmatter and all, the same bytes `/posts/<slug>.md` serves. A tool is a verb the model chooses to use; a resource is a noun you can hand it. Attaching a post as context should not require the model to decide to go and fetch it. Drafts are listed too, marked as such. The list is rebuilt from disk on each `resources/list`, because posts change through these tools, through `/admin`, and through an editor writing a file — there is no one hook that would keep a fixed list honest.

**Protocol version.** The server runs the `2026-07-28` revision, which dropped the `initialize` handshake and protocol-level sessions: each request stands alone and carries its own protocol version in `_meta`, and no `Mcp-Session-Id` is issued. Clients still speaking `2025-11-25` or earlier are answered on the same endpoint — the SDK keeps the old handshake working — so nothing needs upgrading in lockstep. Two consequences worth knowing: `GET /mcp` returns 405, because there is no standing server-to-client stream to open, and a dropped response stream loses that request outright, so the client must retry it with a fresh id rather than resume.

Behind a reverse proxy or tunnel, set `DTCOM_TRUST_PROXY=true` (see below). The MCP handler applies DNS-rebinding protection to requests that arrive on a loopback listener bearing a non-loopback `Host` — the exact shape of a legitimate proxied request — and that flag is what tells it a proxy really is in front.

## REST API

Bearer-authenticated under `/api/v1`. Full list on `/admin/integrations`; the shape most clients need:

```bash
curl -H "Authorization: Bearer $DTCOM_API_TOKEN" https://your-site.example/api/v1/articles

curl -X POST https://your-site.example/api/v1/articles \
  -H "Authorization: Bearer $DTCOM_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"A new post","body":"Body text.","tags":["essay"]}'

curl -X POST https://your-site.example/api/v1/images \
  -H "Authorization: Bearer $DTCOM_API_TOKEN" \
  -F "file=@photo.jpg"
```

Two endpoints are public and unauthenticated: `GET /api/search?q=` and `POST /api/track`. Both are rate limited per client address, and the beacon only records paths that correspond to pages that exist.

## RSS

Outbound: the site serves `/feed.xml` with the most recent published posts.

Inbound: each `rss_feeds` entry in `content/site.yml` is polled on `DTCOM_RSS_INTERVAL` (default 30m, minimum 1m). New items land in the links table and appear on `/links`. Subscribe from the admin Links page — paste a feed URL, and it is polled immediately and then on the interval. Feeds can be paused or removed there too.

The poller dedupes by URL, so re-running it is safe, and one dead feed never blocks the others. Because feeds are third-party input, each fetch is bounded (20s timeout, 8 MB, 100 items, 5 redirects), item text is stripped of markup and truncated, and items whose link uses a scheme like `javascript:` are dropped rather than rendered.

Removing a feed leaves its already-imported links in place; they are part of the published archive.

## Security posture

Worth knowing before this faces the internet:

- **Admin**: password (bcrypt) + TOTP, HMAC-signed session cookie, `SameSite=Lax` plus a `Sec-Fetch-Site`/`Origin` check on every write. A TOTP code is accepted once — it cannot be replayed inside its 30-second window. Login is rate limited per IP and globally, since bcrypt is expensive and a six-digit code is guessable.
- **API/MCP**: constant-time comparison for the bootstrap token, hashed lookup for managed ones; failed attempts are rate limited per IP. Managed tokens are stored as digests, never in the clear.
- **Destructive actions** in the admin are behind a styled confirmation dialog. It is a real `<dialog>` driven from `admin.js` rather than an inline `onsubmit="return confirm(...)"` — the CSP forbids inline handlers, so an inline confirm would not run at all and the delete would go straight through.
- **Headers**: `Content-Security-Policy` with a strict `script-src` (no page executes an inline script), `nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy`, `Strict-Transport-Security` on https deployments (the proxy in front does not add it for you). Every source is `'self'` apart from `img-src`, which allows `https:` so posts can reference remote images. Typography is self-hosted from `static/fonts/`, so there are no third-party font hosts to allow.
- **Markdown** renders with raw HTML enabled, because posts are author-written. Everything downstream treats rendered output as untrusted anyway: the search index strips tags, and search excerpts are escaped before their highlight markers are restored.
- **Views** store a keyed hash of the client address, not the address. The key is `DTCOM_SESSION_KEY`, so the stored value is meaningless without it.
- **Raster uploads** are decoded, dimension-checked against a decompression-bomb limit, and re-encoded, so the served bytes are always a real image in a format we chose.
- **SVG uploads** cannot be re-encoded — an SVG is a document, not a raster — so they are stored as written. Two things bound that. They are validated by parsing rather than by extension or a magic prefix, so a file that merely looks like SVG cannot be stored and later served as `image/svg+xml`. And they are served under their own `Content-Security-Policy` of `default-src 'none'; style-src 'unsafe-inline'; sandbox`, which matters because navigating straight to an SVG makes the browser parse it as a document: the policy leaves it unable to run script or fetch anything. That costs nothing in normal use, since a response CSP does not apply to an SVG drawn through `<img>`, and that path disables scripting outright in every browser anyway.

## Backup

Two things matter, and **neither is in this repo**:

- `content/` — markdown posts and `site.yml`. Gitignored on purpose: it is the author's data, not the project's source, and it is rewritten by `/admin`, the REST API, and the MCP tools as well as by hand. Tracking it meant every post and every feed subscription showed up as a diff against the repo. Back it up — the running site holds the only copy. (Keeping it in a *separate* git repo of your own is a perfectly good backup.)
- the `data/` volume — `dtcom.db` (search index, view counts, links) and `data/images/` (uploaded images). Back this up; it is the only state the binary maintains.

A fresh clone therefore has no content at all. The binary seeds a default `site.yml` when it finds none, so it comes up serving an empty site you can edit from `/admin` — restore from backup instead if you meant to redeploy an existing one.

`public/` is regenerated from the other two and can be deleted freely.

## Development

```bash
go test ./...                          # all packages
go vet ./...
gofmt -w .
node scripts/editor-shortcuts.test.mjs # editor shortcuts + image upload (needs node)
node scripts/lightbox.test.mjs         # article lightbox
DTCOM_BASE_URL=http://localhost:8080 \
DTCOM_ADMIN_PASSWORD_HASH='$2a$10$...' \
DTCOM_TOTP_SECRET=<base32> \
DTCOM_SESSION_KEY=$(openssl rand -hex 32) \
DTCOM_API_TOKEN=$(openssl rand -hex 24) \
go run ./internal
```

An `http://` base URL turns the session cookie's `Secure` flag off automatically, so admin login works over plain HTTP locally.

Templates and static assets are re-read on every rebuild, so editing them takes effect without a restart — which is what the compose bind mounts are for. Static asset URLs carry a content hash, so they can be cached indefinitely and still update the moment the file changes.

Where things live:

```
internal/
  build/      engine: markdown → HTML, feeds, sitemap, images
  server/     HTTP: public routes, admin UI, REST API (/api/v1), MCP (/mcp)
  store/      SQLite layer: articles (FTS5), links, views
  feeds/      outbound feed rendering + inbound RSS poller
  watcher/    fsnotify wrapper with debounce
  auth/       password+TOTP login, HMAC session cookies
  assets/     content-hashed static asset URLs
  markdown/   goldmark setup
  siteconfig/ site.yml struct + load/save
  config/     env-var configuration and validation
content/      markdown source (the source of truth; gitignored — see Backup)
templates/    Go html/template (public + admin/)
static/       theme.js, app.js, editor.js, admin.js, style.css, admin.css
```

Stamp a version into the binary with `-ldflags "-X main.Version=$(git rev-parse --short HEAD)"`; the Dockerfile takes it as a `VERSION` build arg.
