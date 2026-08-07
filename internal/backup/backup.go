// Package backup copies the SQLite database to S3-compatible object storage,
// on a schedule or on demand. There is one destination: the bucket in the
// config. With no bucket configured every entry point is a clean no-op.
package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"time"

	"github.com/RizkyChandra/kaku/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	_ "modernc.org/sqlite"
)

const (
	prefix      = "backups/"
	stamp       = "20060102T150405Z" // UTC, and sorts lexicographically
	defaultKeep = 7
)

// ErrNoBucket means object storage is not configured. Kaku runs fine without
// it, so callers turn this into a message rather than a failure.
var ErrNoBucket = errors.New("backup: no S3 bucket configured")

// keyPattern matches only the keys this package writes. Retention checks
// every candidate against it, so anything else under backups/ is left alone.
var keyPattern = regexp.MustCompile(`^backups/kaku-\d{8}T\d{6}Z\.db$`)

type Backup struct {
	s3  *s3.Client
	cfg config.Config

	Interval time.Duration // defaults to 24h
	Keep     int           // backups retained, defaults to 7
}

// Result describes one uploaded backup.
type Result struct {
	Key  string
	Size int64
	Time time.Time
}

// New returns nil and ErrNoBucket when there is no bucket to back up to. The
// nil *Backup is usable: Run and Once are no-ops on it.
func New(ctx context.Context, cfg config.Config) (*Backup, error) {
	if cfg.S3.Bucket == "" {
		return nil, ErrNoBucket
	}
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.S3.Region)}
	if cfg.S3.Endpoint != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.S3.AccessKey, cfg.S3.SecretKey, "")))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("backup: load aws config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		// A custom endpoint means MinIO or our fake, neither of which does
		// virtual-host addressing.
		if cfg.S3.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.S3.Endpoint)
			o.UsePathStyle = true
		}
	})
	return &Backup{s3: client, cfg: cfg}, nil
}

// Run backs up every Interval until ctx is cancelled, starting with an
// immediate one. A failed backup is logged and the loop continues.
func (b *Backup) Run(ctx context.Context) {
	if b == nil {
		return
	}
	interval := b.Interval
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if res, err := b.Once(ctx); err != nil {
			slog.Error("backup: failed", "error", err)
		} else {
			slog.Info("backup: uploaded", "key", res.Key, "bytes", res.Size)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Once takes and uploads a single backup.
func (b *Backup) Once(ctx context.Context) (Result, error) {
	if b == nil {
		return Result{}, ErrNoBucket
	}
	now := time.Now().UTC()
	key := prefix + "kaku-" + now.Format(stamp) + ".db"

	f, err := os.CreateTemp("", "kaku-backup-*.db")
	if err != nil {
		return Result{}, fmt.Errorf("backup: temp file: %w", err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path) // covers every path below, snapshot failures included
	// VACUUM INTO will not write to a file that already exists, so keep the
	// reserved name and drop the empty placeholder.
	os.Remove(path)

	if err := b.snapshot(ctx, path); err != nil {
		return Result{}, err
	}

	snap, err := os.Open(path)
	if err != nil {
		return Result{}, fmt.Errorf("backup: open snapshot: %w", err)
	}
	defer snap.Close()
	st, err := snap.Stat()
	if err != nil {
		return Result{}, fmt.Errorf("backup: stat snapshot: %w", err)
	}

	// Streamed, not buffered: a snapshot is as big as the database.
	if _, err := b.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        &b.cfg.S3.Bucket,
		Key:           &key,
		Body:          snap,
		ContentLength: aws.Int64(st.Size()),
		ContentType:   aws.String("application/vnd.sqlite3"),
	}); err != nil {
		return Result{}, fmt.Errorf("backup: put %s: %w", key, err)
	}

	res := Result{Key: key, Size: st.Size(), Time: now}
	// The backup is safe at this point; failing to tidy old ones does not
	// undo that, so it is logged rather than returned.
	if err := b.prune(ctx, key); err != nil {
		slog.Warn("backup: retention", "error", err)
	}
	return res, nil
}

// snapshot writes a consistent copy of the database to path. Copying the file
// instead would risk a torn backup: a live WAL database is more than one file
// and its pages change underneath a reader mid-copy.
func (b *Backup) snapshot(ctx context.Context, path string) error {
	// Its own connection: the app's pool is capped at one, and vacuuming
	// through that would stall every request for the duration.
	sqlDB, err := sql.Open("sqlite", b.cfg.DBPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("backup: open %s: %w", b.cfg.DBPath, err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return fmt.Errorf("backup: vacuum into %s: %w", path, err)
	}
	return nil
}

// prune deletes all but the newest Keep backups. keep is the key just
// uploaded, which counts towards the total and is never a deletion candidate.
func (b *Backup) prune(ctx context.Context, keep string) error {
	n := b.Keep
	if n <= 0 {
		n = defaultKeep
	}
	// ponytail: one page, 1000 keys. Since keys sort oldest-first this can
	// only ever leave extra old backups for the next run, never delete a
	// recent one. Paginate if that is ever untrue.
	out, err := b.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &b.cfg.S3.Bucket,
		Prefix: aws.String(prefix),
	})
	if err != nil {
		return fmt.Errorf("backup: list %s: %w", prefix, err)
	}
	var keys []string
	for _, o := range out.Contents {
		// Deletion is irreversible, so a candidate must be under our prefix,
		// match our own naming, and not be the backup just written.
		if k := aws.ToString(o.Key); keyPattern.MatchString(k) && k != keep {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys) // the timestamp format makes this chronological
	excess := len(keys) - (n - 1)
	for _, k := range keys[:max(excess, 0)] {
		if _, err := b.s3.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &b.cfg.S3.Bucket, Key: &k}); err != nil {
			return fmt.Errorf("backup: delete %s: %w", k, err)
		}
		slog.Info("backup: removed old backup", "key", k)
	}
	return nil
}
