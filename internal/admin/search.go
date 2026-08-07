package admin

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/RizkyChandra/kaku/internal/db"
	"github.com/RizkyChandra/kaku/internal/web/view"
)

const searchLimit = 20

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
	res := view.SearchResults{Query: strings.TrimSpace(r.URL.Query().Get("q")), Page: page}
	if expr := escapeFTS(res.Query); expr != "" {
		ctx := r.Context()
		total, err := h.q.CountSearchPosts(ctx, expr)
		if err != nil {
			h.fail(w, r, fmt.Errorf("count search posts: %w", err))
			return
		}
		res.Pages = int((total + searchLimit - 1) / searchLimit)
		hits, err := h.q.SearchPosts(ctx, db.SearchPostsParams{
			PostsFts: expr,
			Limit:    searchLimit,
			Offset:   int64((page - 1) * searchLimit),
		})
		if err != nil {
			h.fail(w, r, fmt.Errorf("search posts: %w", err))
			return
		}
		for _, p := range hits {
			res.Rows = append(res.Rows, view.SearchRow{
				ID: p.ID, Title: p.Title, Status: p.Status,
				Author: p.AuthorName, Excerpt: p.Excerpt, Snippet: p.Snippet,
			})
		}
	}
	if r.Header.Get("HX-Request") != "" {
		render(w, r, view.SearchHits(res))
		return
	}
	render(w, r, view.Search(h.page(r, "Search", "search"), res))
}

// escapeFTS turns what someone typed into an FTS5 query expression. The input
// is attacker-controlled and FTS5's syntax is not: a bare quote or "*" is a
// syntax error, "NOT" is an operator. Quoting every term as a phrase makes all
// of it literal text; the last one gets a trailing "*" so results appear while
// the word is still being typed. Returns "" when nothing is left to search for.
func escapeFTS(q string) string {
	terms := strings.Fields(q)
	if len(terms) == 0 {
		return ""
	}
	for i, t := range terms {
		terms[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	terms[len(terms)-1] += "*"
	return strings.Join(terms, " ")
}
