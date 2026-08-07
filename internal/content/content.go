// Package content turns authored markdown into the HTML, excerpt and slug that
// get stored on a post, and publishes scheduled posts when they come due.
//
// It has no database dependency: the two operations that need one (slug
// collision checks and flipping due posts) are passed in as functions.
package content

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	stdhtml "html"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gosimple/slug"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

// Built once: both are expensive to construct and safe for concurrent use.
var (
	md = goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.Typographer),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		// Raw HTML is let through here and removed again by the policy below.
		// Rendering unsafe without sanitising is a stored-XSS hole.
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)

	policy = newPolicy()

	// Plain-text extraction for excerpts.
	stripper = bluemonday.StrictPolicy()
)

// Only hosts that cannot execute script in our origin and that authors actually embed.
var embedSrc = regexp.MustCompile(`^https://(www\.youtube-nocookie\.com|player\.vimeo\.com)/`)

func newPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("class").OnElements("code", "span", "div", "pre")
	p.AllowAttrs("id").OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	p.AllowElements("figure", "figcaption")
	p.AllowAttrs("loading", "width", "height").OnElements("img")
	p.AllowAttrs("src").Matching(embedSrc).OnElements("iframe")
	p.AllowAttrs("allowfullscreen").OnElements("iframe")
	// GFM task lists; disabled checkboxes carry no behaviour.
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	p.AllowAttrs("checked", "disabled").OnElements("input")
	return p
}

// Render converts markdown to sanitised HTML, safe to store and serve verbatim.
func Render(markdown string) string {
	var buf bytes.Buffer
	if err := md.Convert([]byte(markdown), &buf); err != nil {
		return ""
	}
	return policy.Sanitize(buf.String())
}

// Excerpt returns a plain-text summary of at most max runes, cut on a word
// boundary and suffixed with an ellipsis when it had to cut.
func Excerpt(markdown string, max int) string {
	var buf bytes.Buffer
	if err := md.Convert([]byte(markdown), &buf); err != nil {
		return ""
	}
	text := strings.Join(strings.Fields(stdhtml.UnescapeString(stripper.Sanitize(buf.String()))), " ")

	r := []rune(text)
	if max <= 0 || len(r) <= max {
		return text
	}
	cut := string(r[:max])
	// No space at all (e.g. Japanese) means no word boundary to fall back to.
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:!?-") + "…"
}

// Slugify makes a URL-safe slug, transliterating accented and CJK titles.
// Never returns "": an empty slug would collide on the UNIQUE index.
func Slugify(title string) string {
	if s := slug.Make(title); s != "" {
		return s
	}
	return "post-" + uuid.NewString()[:8]
}

// UniqueSlug appends -2, -3, ... to base until exists reports the slug free.
func UniqueSlug(ctx context.Context, base string, exists func(ctx context.Context, slug string) (bool, error)) (string, error) {
	for i := 1; i <= 100; i++ {
		s := base
		if i > 1 {
			s = fmt.Sprintf("%s-%d", base, i)
		}
		taken, err := exists(ctx, s)
		if err != nil {
			return "", err
		}
		if !taken {
			return s, nil
		}
	}
	return "", errors.New("content: no free slug for " + base)
}

// Scheduler flips scheduled posts to published once their published_at is due.
type Scheduler struct {
	// PublishDue publishes every due post and reports how many it published.
	PublishDue func(context.Context) (int64, error)
	// Interval defaults to a minute.
	Interval time.Duration
}

// Run ticks until ctx is cancelled, starting with an immediate tick.
func (s *Scheduler) Run(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if n, err := s.PublishDue(ctx); err != nil {
			slog.Error("scheduler: publishing due posts", "error", err)
		} else if n > 0 {
			slog.Info("scheduler: published due posts", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
