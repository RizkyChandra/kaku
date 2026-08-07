package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/RizkyChandra/kaku/internal/config"
	"github.com/RizkyChandra/kaku/internal/db"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

// newBackup starts a real gofakes3 server and a real database holding one
// row, so the tests exercise the actual SDK and SQLite paths.
func newBackup(t *testing.T) (*Backup, *sql.DB) {
	t.Helper()
	// os.TempDir honours TMPDIR, so the snapshot lands somewhere we can check
	// it was cleaned up.
	t.Setenv("TMPDIR", t.TempDir())

	srv := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
	t.Cleanup(srv.Close)

	path := filepath.Join(t.TempDir(), "kaku.db")
	sqlDB, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if _, err := sqlDB.ExecContext(t.Context(),
		`CREATE TABLE canary (v TEXT); INSERT INTO canary (v) VALUES ('alive')`); err != nil {
		t.Fatal(err)
	}

	b, err := New(t.Context(), config.Config{
		DBPath: path,
		S3: config.S3{
			Endpoint:  srv.URL,
			Region:    "us-east-1",
			Bucket:    "kaku",
			AccessKey: "kaku",
			SecretKey: "kakukaku",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.s3.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String("kaku")}); err != nil {
		t.Fatal(err)
	}
	return b, sqlDB
}

func put(t *testing.T, b *Backup, key string) {
	t.Helper()
	if _, err := b.s3.PutObject(t.Context(), &s3.PutObjectInput{
		Bucket: aws.String(b.cfg.S3.Bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader("x"),
	}); err != nil {
		t.Fatal(err)
	}
}

func list(t *testing.T, b *Backup, prefix string) []string {
	t.Helper()
	out, err := b.s3.ListObjectsV2(t.Context(), &s3.ListObjectsV2Input{
		Bucket: aws.String(b.cfg.S3.Bucket),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, o := range out.Contents {
		keys = append(keys, aws.ToString(o.Key))
	}
	sort.Strings(keys)
	return keys
}

func TestOnceUploadsUsableDatabase(t *testing.T) {
	b, _ := newBackup(t)

	res, err := b.Once(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !keyPattern.MatchString(res.Key) {
		t.Fatalf("Key = %q, want backups/kaku-<stamp>.db", res.Key)
	}
	if res.Size <= 0 {
		t.Errorf("Size = %d, want > 0", res.Size)
	}

	obj, err := b.s3.GetObject(t.Context(), &s3.GetObjectInput{
		Bucket: aws.String(b.cfg.S3.Bucket),
		Key:    aws.String(res.Key),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Body.Close()
	body, err := io.ReadAll(obj.Body)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(body)) != res.Size {
		t.Errorf("downloaded %d bytes, Size says %d", len(body), res.Size)
	}
	if !bytes.HasPrefix(body, []byte("SQLite format 3\x00")) {
		t.Fatal("uploaded object is not a SQLite database")
	}

	// The whole point of VACUUM INTO: the copy must reopen and still hold the
	// data.
	restored := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(restored, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(t.Context(), restored)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var v string
	if err := sqlDB.QueryRowContext(t.Context(), `SELECT v FROM canary`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != "alive" {
		t.Errorf("canary = %q, want alive", v)
	}
}

func TestOnceRemovesTempFile(t *testing.T) {
	b, _ := newBackup(t)
	if _, err := b.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	left, err := filepath.Glob(filepath.Join(os.TempDir(), "kaku-backup-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("temp files survived: %v", left)
	}
}

func TestRetention(t *testing.T) {
	b, _ := newBackup(t)
	b.Keep = 3

	old := []string{
		"backups/kaku-20200101T000000Z.db",
		"backups/kaku-20200102T000000Z.db",
		"backups/kaku-20200103T000000Z.db",
		"backups/kaku-20200104T000000Z.db",
		"backups/kaku-20200105T000000Z.db",
	}
	for _, k := range old {
		put(t, b, k)
	}
	// Neither of these is ours to delete.
	put(t, b, "media/2020/01/cat.png")
	put(t, b, "backups/README.txt")

	res, err := b.Once(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"backups/README.txt", "media/2020/01/cat.png", old[3], old[4], res.Key}
	sort.Strings(want)
	if got := list(t, b, ""); !slices.Equal(got, want) {
		t.Errorf("bucket contents = %v, want %v", got, want)
	}
}

func TestNoBucket(t *testing.T) {
	b, err := New(t.Context(), config.Config{DBPath: "/nonexistent/kaku.db"})
	if !errors.Is(err, ErrNoBucket) {
		t.Fatalf("err = %v, want ErrNoBucket", err)
	}
	if b != nil {
		t.Fatal("want nil Backup")
	}
	if _, err := b.Once(t.Context()); !errors.Is(err, ErrNoBucket) {
		t.Fatalf("Once err = %v, want ErrNoBucket", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	b.Run(ctx) // must return rather than panic or spin
}
