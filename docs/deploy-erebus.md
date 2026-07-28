# Deploying dtcom on erebus

This is production. `davidtorcivia.com` serves this deployment.

## Current state

| | |
|---|---|
| Path | `/nvme-mirror/apps/dtcom` |
| URL | `https://davidtorcivia.com` |
| Container | `dtcom-dtcom-1`, image `dtcom:latest` |
| Published port | `127.0.0.1:8102 -> 8080` |
| Ingress | Cloudflare Tunnel (`cloudflared`, systemd) → `127.0.0.1:8102` |
| Runs as | uid/gid `1000:1000` |

Port 8102 was picked because 8080–8101 are all taken on erebus. The
container always listens on 8080 internally; only the host half of the
mapping moved, via `DTCOM_PORT`.

**The port is bound to loopback, not the LAN.** The tunnel connects over
`127.0.0.1`, so nothing else needs to reach it — and that is what makes
`DTCOM_TRUST_PROXY=true` correct rather than dangerous. The server reads
the client address from `CF-Connecting-IP`/`X-Forwarded-For`; if the port
were reachable from the LAN, anything on the network could forge those
headers to inflate view counts and walk past the login rate limiter.
The two settings only make sense together.

The consequence is that there is no `http://192.168.1.131:8102` any more.
Reach the site and `/admin` through the domain. To get a LAN-reachable
instance back for testing, set `DTCOM_BIND=0.0.0.0` **and**
`DTCOM_TRUST_PROXY=false` together, never one without the other.

The site this replaced, `fx-davidtorcivia` on port 2999, is stopped and
its compose project torn down. Its database — the only copy of the two
original posts in their source form — is preserved in two places:
`/nvme-mirror/apps/davidtorcivia.com/data/db.sqlite` (in place) and a
checksum-verified copy under
`/nvme-mirror/apps/fx-davidtorcivia-final-backup-<timestamp>/`.

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

`site.yml` now polls `https://disinfozone.substack.com/feed`. The old fx
blogroll also polled `https://divination.disinfo.zone/index.xml` if you
want that back — subscribe from `/admin/links` rather than editing the
file, since the admin rewrites site.yml wholesale.

Old fx URLs (`/posts/<id>/<slug>`) are not redirected; dtcom serves
`/posts/<slug>`. Two posts, so this was judged not worth the code.

## Secrets

`.env` on erebus, mode 600, generated on the host. Not in git.

`DTCOM_TRUST_PROXY=true` paired with the loopback bind — see Current
state for why those two travel together.

Anything here is rotated by editing `.env` and running
`docker compose up -d`.

The admin password and TOTP secret were generated during setup and shown
once in plain text at that point. The admin panel is now reachable from
the public internet, so if that transcript still exists anywhere it
should not, rotate both: generate a new bcrypt hash and base32 secret,
put them in `.env`, restart, and re-enroll the authenticator.

`DTCOM_API_TOKEN` is the bootstrap token and deliberately cannot be
revoked from the UI — it is the way back in if a managed token is
withdrawn by mistake. Its current value is visible on
`/admin/integrations`, which also mints revocable per-client tokens;
prefer those for anything that is not the bootstrap path.

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

## Editing CSS on a running site

Worth knowing, because it bit once. `static/` is bind-mounted, so a
`git pull` changes the stylesheet the server hands out immediately — but
the content hash in `<link href="/static/style.css?v=…">` is computed
during a rebuild and does **not** update on its own. Pages then keep
pointing at the old URL, and any cache holding that URL keeps serving the
old bytes.

Trigger a rebuild after pulling a CSS-only change: the admin's Regenerate
button, `POST /api/v1/regenerate`, or the `regenerate` MCP tool. Confirm
with:

```bash
curl -s http://127.0.0.1:8102/ | grep -oE 'style\.css\?v=[a-z0-9]+'
sha256sum static/style.css | cut -c1-8   # must match the ?v= value
```

Go changes need a full `docker compose build` regardless, which rebuilds
and refreshes the hash as a side effect.

## Rolling back

The old site is stopped, not deleted. To put it back:

```bash
cd /nvme-mirror/apps/davidtorcivia.com && docker compose up -d
```

That returns `fx` to port 2999; repoint the tunnel there. Do this before
touching `dtcom`'s `content/`, since the two do not share storage and
anything written through the new admin exists only under
`/nvme-mirror/apps/dtcom/content/`.

## Backups

Neither of these is in git. Both are irreplaceable.

- `content/` — markdown posts and `site.yml`. Not in git by design (see
  Updating above). This is the only copy.
- `data/` — `dtcom.db` (search index, view counts, links) and
  `data/images/`. Not reconstructible.

`public/` is regenerated from the other two and can be deleted freely.
