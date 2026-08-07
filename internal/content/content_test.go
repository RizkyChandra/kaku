package content

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRenderDocument(t *testing.T) {
	got := Render("# Hi there\n\n" +
		"- a\n- b\n\n" +
		"```go\nfunc main() {}\n```\n\n" +
		"[link](https://example.com)\n\n" +
		"| a | b |\n|---|---|\n| 1 | 2 |\n")

	for _, want := range []string{
		`<h1 id="hi-there">Hi there</h1>`,
		"<ul>",
		"<li>a</li>",
		`<pre><code class="language-go">`,
		`<a href="https://example.com"`,
		"<table>",
		"<td>1</td>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderSanitises(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		notWant  []string
		wantSome []string
	}{
		{
			name:    "script tag",
			in:      "<script>alert(1)</script>",
			notWant: []string{"<script", "alert(1)"},
		},
		{
			name:    "img onerror",
			in:      `<img src=x onerror="alert(1)">`,
			notWant: []string{"onerror", "alert"},
		},
		{
			name:    "javascript href",
			in:      "[click](javascript:alert(1))",
			notWant: []string{"javascript:"},
		},
		{
			name:    "raw javascript anchor",
			in:      `<a href="javascript:alert(1)">click</a>`,
			notWant: []string{"javascript:"},
		},
		{
			name:    "untrusted iframe",
			in:      `<iframe src="http://evil.com"></iframe>`,
			notWant: []string{"<iframe", "evil.com"},
		},
		{
			name:    "iframe on lookalike host",
			in:      `<iframe src="https://www.youtube-nocookie.com.evil.com/x"></iframe>`,
			notWant: []string{"<iframe", "evil.com"},
		},
		{
			name:     "allowed video embed",
			in:       `<iframe src="https://www.youtube-nocookie.com/embed/abc" allowfullscreen></iframe>`,
			wantSome: []string{"<iframe", "https://www.youtube-nocookie.com/embed/abc", "allowfullscreen"},
		},
		{
			name:     "figure and image attrs",
			in:       `<figure><img src="/a.png" loading="lazy" width="10" height="20"><figcaption>cap</figcaption></figure>`,
			wantSome: []string{"<figure>", "<figcaption>cap</figcaption>", `loading="lazy"`, `width="10"`},
		},
		{
			name:     "highlighting classes survive",
			in:       `<pre class="chroma"><code class="language-go"><span class="kw">func</span></code></pre>`,
			wantSome: []string{`class="chroma"`, `class="language-go"`, `class="kw"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Render(tt.in)
			for _, s := range tt.notWant {
				if strings.Contains(got, s) {
					t.Errorf("output still contains %q:\n%s", s, got)
				}
			}
			for _, s := range tt.wantSome {
				if !strings.Contains(got, s) {
					t.Errorf("output missing %q:\n%s", s, got)
				}
			}
		})
	}
}

func TestExcerpt(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{
			name: "strips markdown and truncates on a word boundary",
			in:   "# Title\n\nThe quick brown fox jumps over the lazy dog.",
			max:  20,
			want: "Title The quick…",
		},
		{
			name: "short input is returned whole",
			in:   "Just a **short** note.",
			max:  100,
			want: "Just a short note.",
		},
		{
			name: "counts runes not bytes",
			// 10 runes is 30 bytes here; a byte-based cut would slice mid-rune.
			in:   "これは日本語のテキストです。とても長い文章になります。",
			max:  10,
			want: "これは日本語のテキス…",
		},
		{
			name: "links and code become plain text",
			in:   "See [the docs](https://example.com) and `go build`.",
			max:  100,
			want: "See the docs and go build.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Excerpt(tt.in, tt.max); got != tt.want {
				t.Errorf("Excerpt() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  string
	}{
		{"ascii", "Hello, World!", "hello-world"},
		{"accented latin", "Café Crème à Paris", "cafe-creme-a-paris"},
		{"cjk", "書く", "shu-ku"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Slugify(tt.title); got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.title, got, tt.want)
			}
		})
	}

	// An empty slug would break the UNIQUE index, so punctuation-only titles
	// must still produce something.
	for _, title := range []string{"", "!!! ??? ---", "。。。"} {
		got := Slugify(title)
		if got == "" {
			t.Errorf("Slugify(%q) returned empty", title)
		}
		if strings.TrimLeft(got, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
			t.Errorf("Slugify(%q) = %q, not URL-safe", title, got)
		}
	}
}

func TestUniqueSlug(t *testing.T) {
	ctx := context.Background()

	taken := map[string]bool{"hello": true, "hello-2": true}
	got, err := UniqueSlug(ctx, "hello", func(_ context.Context, s string) (bool, error) {
		return taken[s], nil
	})
	if err != nil || got != "hello-3" {
		t.Fatalf("UniqueSlug() = %q, %v; want hello-3, nil", got, err)
	}

	got, err = UniqueSlug(ctx, "free", func(context.Context, string) (bool, error) { return false, nil })
	if err != nil || got != "free" {
		t.Fatalf("UniqueSlug() = %q, %v; want free, nil", got, err)
	}

	if _, err := UniqueSlug(ctx, "x", func(context.Context, string) (bool, error) { return true, nil }); err == nil {
		t.Error("UniqueSlug() with everything taken: want error, got nil")
	}

	boom := errors.New("boom")
	if _, err := UniqueSlug(ctx, "x", func(context.Context, string) (bool, error) { return false, boom }); !errors.Is(err, boom) {
		t.Errorf("UniqueSlug() err = %v, want %v", err, boom)
	}
}

func TestSchedulerTicksImmediatelyAndStops(t *testing.T) {
	ticked := make(chan struct{}, 4)
	s := &Scheduler{
		// Long enough that only the immediate tick can fire.
		Interval: time.Hour,
		PublishDue: func(context.Context) (int64, error) {
			ticked <- struct{}{}
			return 1, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	select {
	case <-ticked:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not tick immediately")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not stop on context cancellation")
	}
	if len(ticked) != 0 {
		t.Errorf("scheduler ticked %d extra times", len(ticked))
	}
}
