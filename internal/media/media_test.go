package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RizkyChandra/kaku/internal/config"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

// newStore starts a real gofakes3 server, so the tests exercise the actual
// SDK request path rather than a mock.
func newStore(t *testing.T) *Store {
	t.Helper()
	srv := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
	t.Cleanup(srv.Close)

	s, err := New(t.Context(), config.S3{
		Endpoint:  srv.URL,
		Region:    "us-east-1",
		Bucket:    "kaku",
		AccessKey: "kaku",
		SecretKey: "kakukaku",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUploadPNGRoundTrip(t *testing.T) {
	s := newStore(t)
	want := pngBytes(t)

	obj, err := s.Upload(t.Context(), "Photo Of A Cat.PNG", bytes.NewReader(want), int64(len(want)), "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	if obj.MIME != "image/png" {
		t.Errorf("MIME = %q, want image/png", obj.MIME)
	}
	if obj.Size != int64(len(want)) {
		t.Errorf("Size = %d, want %d", obj.Size, len(want))
	}
	if prefix := time.Now().UTC().Format("2006/01") + "/"; !strings.HasPrefix(obj.Key, prefix) {
		t.Errorf("Key = %q, want prefix %q", obj.Key, prefix)
	}
	if !strings.HasSuffix(obj.Key, "-photo-of-a-cat.png") {
		t.Errorf("Key = %q, want sanitised filename suffix", obj.Key)
	}

	resp, err := http.Get(obj.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", obj.URL, resp.StatusCode)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("fetched bytes differ from uploaded bytes")
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("served Content-Type = %q, want image/png", ct)
	}
}

func TestUploadRejects(t *testing.T) {
	s := newStore(t)
	tests := []struct {
		name     string
		filename string
		body     []byte
		want     error
	}{
		{"script disguised as png", "innocent.png", []byte("#!/bin/sh\nrm -rf /\n"), ErrUnsupportedType},
		{"svg is an xss vector", "logo.svg", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), ErrUnsupportedType},
		{"oversize", "big.png", append(pngBytes(t), bytes.Repeat([]byte{0}, MaxSize)...), ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Pass size 0 so the oversize case is caught by the reader limit
			// rather than the declared length.
			if _, err := s.Upload(t.Context(), tt.filename, bytes.NewReader(tt.body), 0, "image/png"); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestUploadHostileFilename(t *testing.T) {
	s := newStore(t)
	b := pngBytes(t)
	obj, err := s.Upload(t.Context(), `../../etc/passwd`, bytes.NewReader(b), int64(len(b)), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(obj.Key, "..") {
		t.Errorf("Key %q contains ..", obj.Key)
	}
	parts := strings.Split(obj.Key, "/")
	if len(parts) != 3 {
		t.Fatalf("Key %q has %d segments, want 3", obj.Key, len(parts))
	}
	if !strings.HasSuffix(parts[2], "-passwd") {
		t.Errorf("Key %q did not keep only the base name", obj.Key)
	}
}

func TestSanitise(t *testing.T) {
	for in, want := range map[string]string{
		`../../etc/passwd`:                "passwd",
		`..`:                              "file",
		`....//..`:                        "file",
		`C:\Windows\evil..`:               "evil",
		``:                                "file",
		`a b?c*.jpg`:                      "a-b-c-.jpg",
		strings.Repeat("x", 200) + ".png": strings.Repeat("x", 80),
	} {
		if got := sanitise(in); got != want {
			t.Errorf("sanitise(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDelete(t *testing.T) {
	s := newStore(t)
	b := pngBytes(t)
	obj, err := s.Upload(t.Context(), "gone.png", bytes.NewReader(b), int64(len(b)), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(t.Context(), obj.Key); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(obj.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET after delete = %d, want 404", resp.StatusCode)
	}
}

func TestNewRequiresBucket(t *testing.T) {
	if _, err := New(context.Background(), config.S3{Region: "us-east-1"}); err == nil {
		t.Fatal("want error for missing bucket")
	}
}
