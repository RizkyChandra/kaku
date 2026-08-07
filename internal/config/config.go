// Package config reads Kaku's configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Addr         string
	URL          string
	Env          string // "production" or "development"
	DBPath       string
	RootEmail    string
	RootPassword string
	S3           S3
}

type S3 struct {
	Endpoint  string // empty = real AWS
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	PublicURL string // base URL media is served from
}

func (c Config) IsDev() bool { return c.Env == "development" }

// Load reads the KAKU_* environment variables. It does not fail on a missing
// root account: the server decides, since an existing database may already
// have users.
func Load() (Config, error) {
	c := Config{
		Addr:         env("KAKU_ADDR", ":8080"),
		URL:          strings.TrimSuffix(env("KAKU_URL", "http://localhost:8080"), "/"),
		Env:          env("KAKU_ENV", "production"),
		DBPath:       env("KAKU_DB_PATH", "/data/kaku.db"),
		RootEmail:    os.Getenv("KAKU_ROOT_EMAIL"),
		RootPassword: os.Getenv("KAKU_ROOT_PASSWORD"),
		S3: S3{
			Endpoint:  os.Getenv("KAKU_S3_ENDPOINT"),
			Region:    env("KAKU_S3_REGION", "us-east-1"),
			Bucket:    os.Getenv("KAKU_S3_BUCKET"),
			AccessKey: os.Getenv("KAKU_S3_ACCESS_KEY"),
			SecretKey: os.Getenv("KAKU_S3_SECRET_KEY"),
			PublicURL: strings.TrimSuffix(os.Getenv("KAKU_S3_PUBLIC_URL"), "/"),
		},
	}
	if c.Env != "production" && c.Env != "development" {
		return c, fmt.Errorf("KAKU_ENV must be production or development, got %q", c.Env)
	}
	if c.RootPassword != "" && len(c.RootPassword) < 8 {
		return c, fmt.Errorf("KAKU_ROOT_PASSWORD must be at least 8 characters")
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
