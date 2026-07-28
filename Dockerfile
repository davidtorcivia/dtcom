# syntax=docker/dockerfile:1

# --- build stage ------------------------------------------------------------
FROM golang:1.25-alpine AS build
WORKDIR /src

# Dependencies first, so a source-only change doesn't re-download the module
# graph.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# VERSION is stamped into the binary. Pass it from CI:
#   docker build --build-arg VERSION=$(git rev-parse --short HEAD) .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X main.Version=${VERSION}" \
      -o /dtcom ./internal

# The runtime image is distroless — no shell — so directories the app writes to
# have to be created here, with the right ownership, and copied in.
#
# This matters more than it looks. The binary runs as uid 65532 while / is owned
# by root, so os.MkdirAll("/public") fails outright and the container exits on
# startup. Creating them here also fixes /data: Docker seeds a fresh named
# volume from the image path's contents *and ownership*, so the volume lands
# writable instead of root-owned.
RUN mkdir -p /skel/public /skel/data/images

# --- runtime stage ----------------------------------------------------------
# distroless static: no shell, no libc; works because CGO_ENABLED=0. It ships
# CA certificates (needed to fetch https RSS feeds) and zoneinfo.
FROM gcr.io/distroless/static:nonroot

COPY --from=build /dtcom /dtcom
# templates and static assets are baked in (also mountable as volumes in
# compose for live editing, but having them in the image means it runs
# standalone too)
COPY --chown=nonroot:nonroot templates/ /templates/
COPY --chown=nonroot:nonroot static/ /static/
COPY --chown=nonroot:nonroot content/ /content/
COPY --from=build --chown=nonroot:nonroot /skel/public /public
COPY --from=build --chown=nonroot:nonroot /skel/data /data

# The default dir paths are relative ("content", "public", …), so the working
# directory decides where they resolve. Setting it explicitly documents that.
WORKDIR /

USER nonroot:nonroot
EXPOSE 8080

# No HEALTHCHECK instruction: distroless has no shell or curl to run one with.
# The app serves GET /healthz for whatever sits in front of it to poll.
ENTRYPOINT ["/dtcom"]
