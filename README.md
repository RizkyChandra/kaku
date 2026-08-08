# Kaku 書く

A headless CMS in Go. Ghost's authoring half, nothing else — markdown posts and pages, tags, media,
users, and a read-only Content API. No themes, no members, no newsletters.

Single static binary, no CGO. SQLite for storage, S3-compatible object storage for media.

## What it does

- **Posts and pages** in markdown, with a live-preview editor, drafts, scheduling, and revision history
- **Tags**, with inline editing
- **Media library** backed by S3 (or MinIO, or the bundled fake for development)
- **Staff accounts** with owner/admin/editor/author/contributor roles
- **Full-text search** over your writing, including Japanese and other CJK text
- **Multi-language**: a translated admin interface, and posts that can exist in several languages
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
| `KAKU_LOCALES_DIR` | — | extra admin translations, see below |
| `KAKU_S3_*` | — | media storage, see `.env.example` |

## Languages

Kaku ships English, Bahasa Indonesia and 日本語, and **adding a language does not need a
rebuild**. A language is a directory of JSON files:

```
locales/
  fr/
    locale.json    {"name": "Français", "dateFormat": "2 Jan 2006"}
    nav.json       {"nav.posts": "Articles", "nav.tags": "Étiquettes"}
```

Point `KAKU_LOCALES_DIR` at that directory and `Français` appears in the picker. Files are
overlaid on the built-in ones **per file**, so you can correct one screen without carrying a whole
translation forward on every upgrade. Keys you leave out fall back to English, so a partial
translation is useful immediately — copy `internal/i18n/locales/en/` and translate as you go.

Staff pick their own language; the site default is a setting. Otherwise Kaku follows the browser's
`Accept-Language`.

Content is separate: a post carries its own language, and translations of one post are linked as a
group. Tags are shared across languages with a translated label each, so a tag filter returns posts
in every language.

## Content API

Create a key under **Settings → API keys**. It is shown once.

```sh
curl -H 'Authorization: Kaku <key>' http://localhost:8080/api/v1/posts
```

| Endpoint | Returns |
| --- | --- |
| `GET /api/v1/posts?page=&limit=&tag=&lang=` | published posts, newest first |
| `GET /api/v1/posts/{slug}` | one post |
| `GET /api/v1/pages`, `GET /api/v1/pages/{slug}` | the same for pages |
| `GET /api/v1/tags` | every tag |

```jsonc
{
  "posts": [{
    "uuid": "…", "title": "…", "slug": "…",
    "html": "<p>…</p>", "excerpt": "…", "feature_image": "…",
    "published_at": "2026-08-08T09:00:00Z", "author": "…",
    "lang": "en", "translations": { "en": "hello", "ja": "konnichiwa" },
    "tags": [ … ]
  }],
  "meta": { "page": 1, "limit": 10, "total": 42, "pages": 5 }
}
```

`lang` filters to one language; **omit it and you get every language**, unchanged from v0.2.0.
`translations` maps a language to that post's slug in it, so a reader can build a language switcher
without a second request — only published translations appear.

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
