# dtcom

A single-binary website for a single author. Markdown files are the source of truth; a Go binary renders them to HTML, indexes them into SQLite for search, exposes a REST API and an MCP server for programmatic editing, and serves everything on port 8080. Built to be edited by hand, through a browser, or by an LLM — all three paths write to the same files.

## Architecture

```
                         ┌─────────────┐
   content/posts/*.md ──▶│   engine    │──▶ public/*.html  (served read-only)
   content/site.yml   ──▶│  (goldmark) │──▶ feed.xml, sitemap.xml
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

## Quickstart

```bash
cp .env.example .env
# fill in the five required secrets (see .env.example for how to generate each)
docker compose up -d
```

The site is now on `http://localhost:8080`. Put it behind Cloudflare Tunnel (or any reverse proxy) by pointing the tunnel at `localhost:8080`; nothing in dtcom terminates TLS itself.

## Adding content

Three ways, all writing to the same place.

**Markdown file.** Drop `YYYY-MM-DD-my-post.md` into `content/posts/` with frontmatter:

```markdown
---
title: My Post
date: 2026-07-27
description: One line.
tags: [essay, color]
---

Body text in markdown.
```

The watcher rebuilds within ~500ms. Drafts (`draft: true`) are excluded from the build.

**Admin UI.** `/admin` — log in with the bcrypt password and a TOTP code. The post editor has an Obsidian-style Write/Preview toggle: Preview POSTs the textarea to `/admin/posts/preview` and renders the markdown.

**API / MCP.** See the two sections below. These are the paths an LLM client uses.

## Site configuration

`content/site.yml` holds everything that is not a post: title, author, bio paragraphs, nav links, social links, the inbound RSS feed list, and the footer text. Edit the file directly (the watcher picks it up) or through the admin Site page or the `get_site` / `update_*` MCP tools. The schema matches the YAML exactly — there is one key per section, no nesting beyond what the file shows.

## MCP integration

The `/mcp` endpoint speaks MCP over Streamable HTTP with bearer-token auth (the same `DTCOM_API_TOKEN` the REST API uses). Point any MCP client at it:

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

Sixteen tools are exposed, grouped by what they touch:

| Group | Tools |
|-------|-------|
| Articles | `list_articles`, `get_article`, `create_article`, `update_article`, `delete_article`, `search_articles` |
| Links    | `list_links`, `add_link`, `remove_link` |
| Site     | `get_site`, `update_bio`, `update_nav`, `update_social`, `update_rss_feeds` |
| Ops      | `regenerate`, `get_stats`, `refresh_feeds` |

Every write tool saves to disk and triggers a rebuild, so a `create_article` call is fully published by the time it returns.

## RSS

Outbound: the site serves `/feed.xml` with the most recent published posts.

Inbound: each `rss_feeds` entry in `content/site.yml` is polled on `DTCOM_RSS_INTERVAL` (default 30m). New items land in the links table and appear on `/links`. The poller dedupes by URL, so re-running it is safe. `refresh_feeds` triggers an immediate poll.

## Backup

Two things matter:

- `content/` — markdown source and `site.yml`. Keep it in git.
- the `data/` volume — `dtcom.db` (search index, view counts, links) and `data/images/` (uploaded images). Back this up; it is the only state the binary maintains.

`public/` is regenerated from the other two and can be deleted freely.

## Development

```bash
go test ./...                          # all packages
go vet ./...
gofmt -w .
DTCOM_BASE_URL=http://localhost:8080 \
DTCOM_ADMIN_PASSWORD_HASH='$2a$10$...' \
DTCOM_TOTP_SECRET=<base32> \
DTCOM_SESSION_KEY=$(openssl rand -hex 32) \
DTCOM_API_TOKEN=$(openssl rand -hex 24) \
go run ./internal
```

Where things live:

```
internal/
  build/      engine: markdown → HTML, feeds, sitemap, image handling
  server/     HTTP: public routes, admin UI, REST API (/api/v1), MCP (/mcp)
  store/      SQLite layer: articles, links, views
  feeds/      inbound RSS poller
  watcher/    fsnotify wrapper with debounce
  auth/       password+TOTP login, HMAC session cookies
  markdown/   goldmark setup
  siteconfig/ site.yml struct + load/save
  config/     env-var configuration
content/      markdown source (the source of truth)
templates/    Go text/html templates (public + admin/)
static/       app.js, editor.js, style.css, admin.css
```
