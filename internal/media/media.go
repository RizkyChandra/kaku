// Package media stores uploaded images in S3-compatible object storage.
// It returns the metadata for a row in the media table; writing that row is
// the caller's job.
package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/RizkyChandra/kaku/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// MaxSize is the largest upload accepted. The caller should also cap the
// request body; this is the second line of defence.
const MaxSize = 10 << 20

var (
	ErrTooLarge        = errors.New("media: file too large")
	ErrUnsupportedType = errors.New("media: unsupported file type")
)

// allowed is checked against the sniffed type, never the declared one.
// SVG is deliberately absent: it is XML that browsers execute, so serving one
// from our own origin is a stored-XSS hole. It sniffs as text/xml anyway.
var allowed = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

type Store struct {
	s3  *s3.Client
	cfg config.S3
}

type Object struct {
	Key      string
	Filename string
	URL      string
	MIME     string
	Size     int64
}

func New(ctx context.Context, cfg config.S3) (*Store, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("media: no S3 bucket configured")
	}
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if cfg.Endpoint != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("media: load aws config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		// A custom endpoint means MinIO or our fake, neither of which does
		// virtual-host addressing.
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = true
		}
	})
	return &Store{s3: client, cfg: cfg}, nil
}

// Upload validates and stores r. declaredMIME is accepted for symmetry with
// the multipart form but deliberately ignored: it is attacker-controlled.
func (s *Store) Upload(ctx context.Context, filename string, r io.Reader, size int64, declaredMIME string) (Object, error) {
	if size > MaxSize {
		return Object{}, ErrTooLarge
	}
	// Read one byte past the limit so a lying Content-Length is caught too.
	body, err := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if err != nil {
		return Object{}, fmt.Errorf("media: read upload: %w", err)
	}
	if int64(len(body)) > MaxSize {
		return Object{}, ErrTooLarge
	}
	mime, _, _ := strings.Cut(http.DetectContentType(body), ";")
	if !allowed[mime] {
		return Object{}, ErrUnsupportedType
	}

	key := fmt.Sprintf("%s/%s-%s", time.Now().UTC().Format("2006/01"), strings.ToLower(rand.Text()), sanitise(filename))
	_, err = s.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       &s.cfg.Bucket,
		Key:          &key,
		Body:         bytes.NewReader(body),
		ContentType:  &mime,
		CacheControl: aws.String("public, max-age=31536000, immutable"), // keys are unique, so content never changes
	})
	if err != nil {
		return Object{}, fmt.Errorf("media: put %s: %w", key, err)
	}
	return Object{Key: key, Filename: sanitise(filename), URL: s.url(key), MIME: mime, Size: int64(len(body))}, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	if _, err := s.s3.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.cfg.Bucket, Key: &key}); err != nil {
		return fmt.Errorf("media: delete %s: %w", key, err)
	}
	return nil
}

// EnsureBucket creates the bucket if it is missing. Only useful against the
// fake or a local MinIO; on real AWS the bucket is provisioned out of band and
// the credentials usually cannot create one, so an existing bucket is success.
func (s *Store) EnsureBucket(ctx context.Context) error {
	_, err := s.s3.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &s.cfg.Bucket})
	var owned *types.BucketAlreadyOwnedByYou
	var exists *types.BucketAlreadyExists
	if err == nil || errors.As(err, &owned) || errors.As(err, &exists) {
		return nil
	}
	return fmt.Errorf("media: create bucket %s: %w", s.cfg.Bucket, err)
}

func (s *Store) url(key string) string {
	switch {
	case s.cfg.PublicURL != "":
		return s.cfg.PublicURL + "/" + key
	case s.cfg.Endpoint != "":
		return s.cfg.Endpoint + "/" + s.cfg.Bucket + "/" + key
	default:
		return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", s.cfg.Bucket, s.cfg.Region, key)
	}
}

// sanitise reduces a client-supplied filename to a path-free, conservative
// token. A hostile name must not escape the key prefix or contain "..".
func sanitise(name string) string {
	name = name[strings.LastIndexAny(name, `/\`)+1:]
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		}
		return '-'
	}, name)
	for strings.Contains(name, "..") {
		name = strings.ReplaceAll(name, "..", ".")
	}
	if len(name) > 80 {
		name = name[:80]
	}
	name = strings.Trim(name, ".-")
	if name == "" {
		name = "file"
	}
	return name
}
