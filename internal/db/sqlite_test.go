package db_test

import (
	"context"
	"testing"

	"github.com/RizkyChandra/kaku/internal/db"
)

// Open applies every migration, is safe to run twice, and round-trips the
// DATETIME columns as time.Time.
func TestOpenAndMigrate(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sqlDB.Close()

	if err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("second migrate should be a no-op: %v", err)
	}

	q := db.New(sqlDB)
	n, err := q.CountUsers(ctx)
	if err != nil || n != 0 {
		t.Fatalf("CountUsers = %d, %v; want 0, nil", n, err)
	}

	u, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: "a@example.com", PasswordHash: "x", Name: "A", Role: "owner",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.CreatedAt.IsZero() {
		t.Error("created_at did not decode into a time.Time")
	}

	// Email lookup must be case-insensitive, like the UNIQUE COLLATE NOCASE index.
	got, err := q.GetUserByEmail(ctx, "A@EXAMPLE.COM")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("GetUserByEmail returned %d, want %d", got.ID, u.ID)
	}

	if err := q.SetSetting(ctx, db.SetSettingParams{Key: "title", Value: "Kaku"}); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := q.SetSetting(ctx, db.SetSettingParams{Key: "title", Value: "Kaku 2"}); err != nil {
		t.Fatalf("SetSetting upsert: %v", err)
	}
	v, err := q.GetSetting(ctx, "title")
	if err != nil || v != "Kaku 2" {
		t.Errorf("GetSetting = %q, %v; want \"Kaku 2\", nil", v, err)
	}
}
