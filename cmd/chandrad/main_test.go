package main

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestCountDBAllowedUsers(t *testing.T) {
	f, err := os.CreateTemp("", "chandra-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	db, err := sql.Open("sqlite3", f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE allowed_users (
        channel_id TEXT NOT NULL,
        user_id    TEXT NOT NULL,
        username   TEXT,
        source     TEXT,
        added_at   INTEGER
    )`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	const testChannelID = "1234567890123456789" // realistic Discord channel ID (snowflake)

	// Empty DB → countDBAllowedUsers should return 0.
	count, err := countDBAllowedUsers(f.Name(), testChannelID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 users, got %d", count)
	}

	// Insert one user → count should be 1.
	db, _ = sql.Open("sqlite3", f.Name())
	db.Exec("INSERT INTO allowed_users (channel_id, user_id, source) VALUES (?, 'u1', 'test')", testChannelID)
	db.Close()

	count, err = countDBAllowedUsers(f.Name(), testChannelID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 user, got %d", count)
	}
}
