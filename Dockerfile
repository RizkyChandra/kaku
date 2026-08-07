# syntax=docker/dockerfile:1
# Builds from a clean checkout: generated files (*_templ.go, tailwind.css) are committed,
# so no templ/sqlc/tailwind toolchain is needed here.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
ARG VERSION=docker
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /kaku ./cmd/kaku

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /kaku /kaku
# SQLite lives here; mount a volume.
VOLUME /data
USER nonroot:nonroot
EXPOSE 8080
ENV KAKU_DB_PATH=/data/kaku.db
ENTRYPOINT ["/kaku"]
