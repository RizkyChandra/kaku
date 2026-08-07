# Kaku 書く

A headless CMS in Go. Ghost's authoring half, nothing else — markdown posts and pages, tags, media,
users, and a read-only Content API. No themes, no members, no newsletters.

Single static binary, no CGO. SQLite for storage, S3-compatible object storage for media.

## What it does

- **Posts and pages** in markdown, with a live-preview editor, drafts, scheduling, and revision history
- **Tags**, with inline editing
- **Media library** backed by S3 (or MinIO, or the bundled fake for development)
- **Staff accounts** with owner/admin/editor/author/contributor roles
- **Full-text search** over your writing
- **Content API** — a read-only JSON API, key-authenticated, for whatever front end you like

What it deliberately does not do: themes, members, newsletters, or comments. Kaku stores your writing
and hands it to you over HTTP; the front end is your business.

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

## Content API

Create a key under **Settings → API keys**. It is shown once.

```sh
curl -H 'Authorization: Kaku <key>' http://localhost:8080/api/v1/posts
```

| Endpoint | Returns |
| --- | --- |
| `GET /api/v1/posts?page=&limit=&tag=` | published posts, newest first |
| `GET /api/v1/posts/{slug}` | one post |
| `GET /api/v1/pages`, `GET /api/v1/pages/{slug}` | the same for pages |
| `GET /api/v1/tags` | every tag |

```jsonc
{
  "posts": [{
    "uuid": "…", "title": "…", "slug": "…",
    "html": "<p>…</p>", "excerpt": "…", "feature_image": "…",
    "published_at": "2026-08-08T09:00:00Z", "author": "…", "tags": [ … ]
  }],
  "meta": { "page": 1, "limit": 10, "total": 42, "pages": 5 }
}
```

Only published, public content is ever returned. `limit` defaults to 10 and caps at 100. Errors are
JSON: `{"error": "…"}`. CORS is open — the API is read-only and key-gated.

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
