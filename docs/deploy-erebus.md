# Deploying dtcom on erebus

The internal LAN deployment, and the checklist for pointing
`davidtorcivia.com` at it.

## Current state

| | |
|---|---|
| Path | `/nvme-mirror/apps/dtcom` |
| URL (LAN) | `http://192.168.1.131:8102` |
| Container | `dtcom-dtcom-1`, image `dtcom:latest` |
| Published port | `0.0.0.0:8102 -> 8080` |
| Runs as | uid/gid `1000:1000` |

Port 8102 was picked because 8080–8101 are all taken on erebus. The
container always listens on 8080 internally; only the host half of the
mapping moved, via `DTCOM_PORT`.

The site it replaces is still live: `fx-davidtorcivia` on port 2999, out
of `/nvme-mirror/apps/davidtorcivia.com`. Nothing here touches it.

## The uid trap

This is the one non-obvious thing about the deployment, and the local
`docker-compose.override.yml` exists solely because of it. That file is
untracked, so this section is its only documentation — recreate it from
here if the checkout is ever rebuilt.

The image runs as uid 65532 (the distroless `nonroot` user) and creates
`/data` and `/public` owned by that uid. That works fine on a host where
the app owns its own directories. It does not work here:

- `./content` on erebus is owned by uid 1000, and the container must
  write to it (admin UI, REST API, and MCP all create markdown there).
  So the container has to run as uid 1000 — hence `DTCOM_UID=1000`.
- But under that uid it can write neither `/data` nor `/public`, because
  both come from the image owned by 65532. Docker also seeds a fresh
  named volume from the image path's *ownership*, so the `dtcom-data`
  volume inherits the problem. The container dies on startup with
  `unable to open database file (14)`, and once past that,
  `mkdir public/posts: permission denied`.
- The obvious fix — chown the host directories to 65532 — needs root,
  and there is no passwordless sudo on erebus.

A bind mount takes the *host* directory's ownership rather than the image
path's, which sidesteps all of it without root. So
`docker-compose.override.yml` replaces the named volume with bind mounts:

```yaml
services:
  dtcom:
    volumes:
      - ./content:/content
      - ./static:/static:ro
      - ./templates:/templates:ro
      - ./data:/data
      - ./public:/public
```

`./data` and `./public` are both gitignored. Having `data/` as a plain
directory rather than a docker volume also makes the one thing that needs
backing up trivial to back up.

If you ever recreate this from scratch: `mkdir -p data/images public`
before the first `docker compose up`.

## Content

`content/posts/` holds the real archive, migrated out of the fx SQLite
database (`/nvme-mirror/apps/davidtorcivia.com/data/db.sqlite`, `posts`
table). fx stores only `created`, `updated`, and `content` — no title,
slug, description, or tags — so those were supplied during migration:

- **four-maxims-for-technology** (2025-11-14) — had no title in fx.
- **schedlock** (2026-01-31) — its body opened with an `# SchedLock` H1,
  dropped because dtcom renders the title from frontmatter. Its lead
  image is carried as `cover` for the OG card as well as staying inline.

Bodies are otherwise byte-for-byte what fx serves. Descriptions and tags
were drafted, not migrated; they are worth a read-through.

`rss_feeds` in `site.yml` is empty. The live fx blogroll polls
`https://divination.disinfo.zone/index.xml` if you want it back —
subscribe from `/admin/links` rather than editing the file.

Old fx URLs (`/posts/<id>/<slug>`) are not redirected; dtcom serves
`/posts/<slug>`. Two posts, so this was judged not worth the code.

## Secrets

`.env` on erebus, mode 600, generated on the host. Not in git.

`DTCOM_TRUST_PROXY=true` while the port is published on `0.0.0.0`. Be
aware of what that combination means: the server reads the client address
from `CF-Connecting-IP` / `X-Forwarded-For`, so anything on the LAN can
forge those headers to inflate view counts and bypass the login rate
limiter. It is the correct setting once Cloudflare Tunnel is the only
path in; until then the port is directly reachable.

Rotate by editing `.env` and running `docker compose up -d`. The API
token can also be read from `/admin/integrations`, which additionally
mints revocable per-client tokens — prefer those for anything but the
bootstrap path.

## Updating

```bash
cd /nvme-mirror/apps/dtcom
git pull
DTCOM_VERSION=$(git rev-parse --short HEAD) docker compose build
docker compose up -d
```

`content/` is **not** tracked in git, so posts and site.yml written
through `/admin` on erebus are invisible to `git pull` and survive it
untouched. That is deliberate: the admin save path rewrites site.yml
through a YAML round trip, so tracking it meant every edit on the running
site produced a diff and every deploy risked a conflict.

The flip side is that a `git clone` gives you an empty site. The binary
seeds a default `site.yml` when it finds none and comes up serving
nothing, which is the correct starting state for a new deployment and a
data-loss event for an existing one. Restore `content/` from backup
before starting a rebuilt checkout.

Templates and static assets are bind-mounted, and the engine re-reads
templates on every rebuild, so editing those takes effect without a
rebuild of the image.

## Cutover checklist

When you are satisfied with what is on 8102:

1. Edit `.env`: `DTCOM_BASE_URL=https://davidtorcivia.com`.
   This is what puts the real hostname into canonical links, `feed.xml`,
   `sitemap.xml`, and OG tags, and it turns the session cookie's `Secure`
   flag on. Once set, admin login over plain `http://192.168.1.131:8102`
   stops working — reach `/admin` through the tunnel from then on.
2. Optionally set `DTCOM_BIND=127.0.0.1` so the port is no longer exposed
   to the LAN. With the tunnel as the only ingress, this is what makes
   `DTCOM_TRUST_PROXY=true` safe.
3. `docker compose up -d` to apply.
4. Repoint the Cloudflare tunnel from port 2999 to port 8102.
5. Verify `https://davidtorcivia.com/healthz`, one post, `/feed.xml`, and
   an admin login.
6. Stop the old site: `cd /nvme-mirror/apps/davidtorcivia.com && docker
   compose down`. Keep `data/db.sqlite` — it is the only copy of the
   original posts.

## Backups

Neither of these is in git. Both are irreplaceable.

- `content/` — markdown posts and `site.yml`. Not in git by design (see
  Updating above). This is the only copy.
- `data/` — `dtcom.db` (search index, view counts, links) and
  `data/images/`. Not reconstructible.

`public/` is regenerated from the other two and can be deleted freely.
