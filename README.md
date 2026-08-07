# Kaku 書く

A headless CMS in Go. Ghost's authoring half, nothing else — markdown posts and pages, tags, media,
users, and a read-only Content API. No themes, no members, no newsletters.

Single static binary. SQLite for storage, S3 for media.

## Stack

Go · [chi](https://github.com/go-chi/chi) · [templ](https://templ.guide) · [htmx 4](https://htmx.org) ·
Tailwind CSS · [sqlc](https://sqlc.dev) · SQLite ([modernc](https://modernc.org/sqlite), pure Go) ·
[goldmark](https://github.com/yuin/goldmark)

## Quick start

```sh
docker run -p 8080:8080 -v kaku-data:/data \
  -e KAKU_ROOT_EMAIL=you@example.com \
  -e KAKU_ROOT_PASSWORD=change-me \
  ghcr.io/rizkychandra/kaku:latest
```

Admin at <http://localhost:8080/admin>.

## Configuration

All configuration is environment variables. See [`.env.example`](.env.example).

| Variable | Default | Notes |
| --- | --- | --- |
| `KAKU_ADDR` | `:8080` | listen address |
| `KAKU_URL` | `http://localhost:8080` | public base URL |
| `KAKU_ENV` | `production` | `development` relaxes cookie `Secure` |
| `KAKU_DB_PATH` | `/data/kaku.db` | SQLite file |
| `KAKU_ROOT_EMAIL` | — | owner account, created on first boot |
| `KAKU_ROOT_PASSWORD` | — | owner password |
| `KAKU_S3_*` | — | media storage, see `.env.example` |

## Development

```sh
make dev      # fakes3 + tailwind watch + templ watch + server
make test
make build
```

Requires Go 1.26+. `templ` comes from `go tool`; `sqlc` and the Tailwind standalone CLI are pinned
binaries the Makefile fetches into `bin/`. No Node required.

## Contributing

Work happens on `dev`. Branch from it, PR into it. `main` is release-only and tagged.

## License

MIT — see [LICENSE](LICENSE).
