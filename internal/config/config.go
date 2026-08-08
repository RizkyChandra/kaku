// Package config reads Kaku's configuration from the environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Addr         string
	URL          string
	Env          string // "production" or "development"
	DBPath       string
	RootEmail    string
	RootPassword string
	// LocalesDir holds extra admin translations. Files here are loaded on top
	// of the embedded ones, so a language can be added or corrected without a
	// rebuild.
	LocalesDir string
	S3         S3
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
		DBPath:       env("KAKU_DB_PATH", filepath.Join(env("RAILWAY_VOLUME_MOUNT_PATH", "/data"), "kaku.db")),
		RootEmail:    os.Getenv("KAKU_ROOT_EMAIL"),
		RootPassword: os.Getenv("KAKU_ROOT_PASSWORD"),
		LocalesDir:   os.Getenv("KAKU_LOCALES_DIR"),
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
	if err := checkOnVolume(c.DBPath, os.Getenv("RAILWAY_VOLUME_MOUNT_PATH")); err != nil {
		return c, err
	}
	return c, nil
}

// checkOnVolume refuses a database path that lies outside an attached volume.
// Opening the database creates missing directories, so a relative or stray
// KAKU_DB_PATH quietly writes to the container's disk instead: the site works,
// then loses every post on the next deploy. Failing to boot is the kinder
// failure. mount is empty when no volume is attached, and nothing is checked.
func checkOnVolume(dbPath, mount string) error {
	if mount == "" {
		return nil
	}
	mount = filepath.Clean(mount)
	if p := filepath.Clean(dbPath); p != mount && !strings.HasPrefix(p, mount+string(filepath.Separator)) {
		return fmt.Errorf("KAKU_DB_PATH %q is outside the volume mounted at %s, so the database would be erased on the next deploy: unset KAKU_DB_PATH, or set it to %s",
			dbPath, mount, filepath.Join(mount, "kaku.db"))
	}
	return nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
