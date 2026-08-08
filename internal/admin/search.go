package admin

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/RizkyChandra/kaku/internal/db"
	"github.com/RizkyChandra/kaku/internal/i18n"
	"github.com/RizkyChandra/kaku/internal/web/view"
)

const searchLimit = 20

// minTrigramLen is where the index stops being able to help. posts_fts is
// tokenized as trigrams, so a query of fewer than three characters produces no
// token and matches nothing at all, however common the substring is. Two
// characters is a whole word in Japanese, so those queries take the scan below
// rather than returning an empty page.
const minTrigramLen = 3

func (h *Handler) mountSearch(r chi.Router) {
	r.Get("/search", h.search)
}

// search answers with the whole page, or with just the results when htmx asks
// for them. Anything a signed-in user could reach from the post list they can
// find here: the list shows every post to every role, so this does too.
func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	res := view.SearchResults{
		Query: strings.TrimSpace(r.URL.Query().Get("q")),
		Lang:  strings.ToLower(strings.TrimSpace(r.URL.Query().Get("lang"))),
		Page:  page,
	}
	if res.Query != "" {
		rows, total, err := h.searchPage(r.Context(), res.Query, res.Lang, page)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		res.Rows = rows
		res.Pages = int((total + searchLimit - 1) / searchLimit)
	}
	if r.Header.Get("HX-Request") != "" {
		render(w, r, view.SearchHits(res))
		return
	}
	render(w, r, view.Search(h.page(r, i18n.T(r.Context(), "search.title"), "search"), res))
}

// searchPage returns one page of hits and the total behind it, both filtered by
// lang, where "" means every language. The two have to agree or the pager lies.
func (h *Handler) searchPage(ctx context.Context, query, lang string, page int) ([]view.SearchRow, int64, error) {
	off := int64((page - 1) * searchLimit)
	var rows []view.SearchRow

	if utf8.RuneCountInString(query) < minTrigramLen {
		total, err := h.q.CountSearchPostsShort(ctx, db.CountSearchPostsShortParams{Needle: query, Lang: lang})
		if err != nil {
			return nil, 0, fmt.Errorf("count search posts: %w", err)
		}
		hits, err := h.q.SearchPostsShort(ctx, db.SearchPostsShortParams{
			Needle: query, Lang: lang, Lim: searchLimit, Off: off,
		})
		if err != nil {
			return nil, 0, fmt.Errorf("search posts: %w", err)
		}
		for _, p := range hits {
			rows = append(rows, view.SearchRow{
				ID: p.ID, Title: p.Title, Status: p.Status,
				Author: p.AuthorName, Excerpt: p.Excerpt,
			})
		}
		return rows, total, nil
	}

	expr := escapeFTS(query)
	if expr == "" {
		return nil, 0, nil
	}
	total, err := h.q.CountSearchPosts(ctx, db.CountSearchPostsParams{Query: expr, Lang: lang})
	if err != nil {
		return nil, 0, fmt.Errorf("count search posts: %w", err)
	}
	hits, err := h.q.SearchPosts(ctx, db.SearchPostsParams{
		Query: expr, Lang: lang, Lim: searchLimit, Off: off,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("search posts: %w", err)
	}
	for _, p := range hits {
		rows = append(rows, view.SearchRow{
			ID: p.ID, Title: p.Title, Status: p.Status,
			Author: p.AuthorName, Excerpt: p.Excerpt, Snippet: p.Snippet,
		})
	}
	return rows, total, nil
}

// escapeFTS turns what someone typed into an FTS5 query expression. The input
// is attacker-controlled and FTS5's syntax is not: a bare quote or "*" is a
// syntax error, "NOT" is an operator. Quoting every term as a phrase makes all
// of it literal text.
//
// Quotes the user typed are dropped rather than doubled. A trigram phrase
// matches as a raw substring, so a doubled quote would be text to search for
// instead of the separator it was under unicode61 -- and a term that is nothing
// but quotes is not a search at all, so it goes too.
//
// No trailing "*" either: trigrams match inside a word, so a half-typed word
// already finds results and the prefix operator would only be a literal star.
// Returns "" when nothing is left to search for.
func escapeFTS(q string) string {
	var terms []string
	for _, t := range strings.Fields(q) {
		if t = strings.ReplaceAll(t, `"`, ""); t != "" {
			terms = append(terms, `"`+t+`"`)
		}
	}
	return strings.Join(terms, " ")
}
