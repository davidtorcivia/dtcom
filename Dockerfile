# build stage
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /dtcom ./internal

# runtime stage — distroless static (no shell, no libc; works because CGO_ENABLED=0)
FROM gcr.io/distroless/static:nonroot
COPY --from=build /dtcom /dtcom
# templates and static assets are baked in (also mounted as volumes in compose
# for live editing, but having them in the image means it runs standalone too)
COPY templates/ /templates/
COPY static/ /static/
COPY content/ /content/
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/dtcom"]
