package state

import (
	"testing"
)

func TestDB_OldItems_returns_aged_items(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	bookmarkID := int64(42)
	// Insert directly with a past timestamp to bypass timing sensitivity.
	if _, err := db.conn.Exec(
		`INSERT INTO sent_items (guid, bookmark_id, sent_at) VALUES (?, ?, ?)`,
		"old-guid", bookmarkID, "2020-01-01 00:00:00",
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	items, err := db.OldItemsOutside(1, nil)
	if err != nil {
		t.Fatalf("OldItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].GUID != "old-guid" {
		t.Errorf("GUID: got %q", items[0].GUID)
	}
	if items[0].BookmarkID == nil || *items[0].BookmarkID != bookmarkID {
		t.Errorf("BookmarkID: got %v, want %d", items[0].BookmarkID, bookmarkID)
	}
}

func TestDB_OldItems_nil_bookmark_id_for_pre_migration_rows(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// Simulate a pre-retention row (no bookmark_id).
	if _, err := db.conn.Exec(
		`INSERT INTO sent_items (guid, sent_at) VALUES (?, ?)`,
		"old-no-id", "2020-01-01 00:00:00",
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	items, err := db.OldItemsOutside(1, nil)
	if err != nil {
		t.Fatalf("OldItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].BookmarkID != nil {
		t.Errorf("BookmarkID: expected nil, got %v", items[0].BookmarkID)
	}
}

func TestDB_OldItems_skips_recent_items(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if err := db.MarkSent("recent"); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}

	items, err := db.OldItemsOutside(1, nil) // 1 day threshold — just-inserted item is not old
	if err != nil {
		t.Fatalf("OldItems: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("got %d items, want 0 (recent item must not be returned)", len(items))
	}
}

func TestDB_OldItems_skips_archived_items(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// Insert an old item that has already been archived.
	if _, err := db.conn.Exec(
		`INSERT INTO sent_items (guid, bookmark_id, sent_at, archived_at) VALUES (?, ?, ?, ?)`,
		"already-archived", int64(10), "2020-01-01 00:00:00", "2020-01-02 00:00:00",
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	items, err := db.OldItemsOutside(1, nil)
	if err != nil {
		t.Fatalf("OldItems: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("got %d items, want 0 (archived item must be excluded)", len(items))
	}
}

func TestDB_ArchivedItems_returns_old_archived_items(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	bookmarkID := int64(77)
	// Insert an item that was archived long ago.
	if _, err := db.conn.Exec(
		`INSERT INTO sent_items (guid, bookmark_id, sent_at, archived_at) VALUES (?, ?, ?, ?)`,
		"old-archived", bookmarkID, "2020-01-01 00:00:00", "2020-01-02 00:00:00",
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	items, err := db.ArchivedItemsOutside(30, nil)
	if err != nil {
		t.Fatalf("ArchivedItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].GUID != "old-archived" {
		t.Errorf("GUID: got %q", items[0].GUID)
	}
	if items[0].BookmarkID == nil || *items[0].BookmarkID != bookmarkID {
		t.Errorf("BookmarkID: got %v, want %d", items[0].BookmarkID, bookmarkID)
	}
}

func TestDB_ArchivedItems_skips_recently_archived(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// MarkArchived uses CURRENT_TIMESTAMP — item is too new to appear.
	if _, err := db.conn.Exec(
		`INSERT INTO sent_items (guid, bookmark_id, sent_at) VALUES (?, ?, ?)`,
		"fresh", int64(1), "2020-01-01 00:00:00",
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db.MarkArchived("fresh"); err != nil {
		t.Fatalf("MarkArchived: %v", err)
	}

	items, err := db.ArchivedItemsOutside(1, nil)
	if err != nil {
		t.Fatalf("ArchivedItems: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("got %d items, want 0 (recently archived must not be returned)", len(items))
	}
}

func TestDB_ArchivedItems_skips_non_archived_items(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// Old item with no archived_at — should not appear in ArchivedItems.
	if _, err := db.conn.Exec(
		`INSERT INTO sent_items (guid, bookmark_id, sent_at) VALUES (?, ?, ?)`,
		"not-archived", int64(2), "2020-01-01 00:00:00",
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	items, err := db.ArchivedItemsOutside(1, nil)
	if err != nil {
		t.Fatalf("ArchivedItems: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("got %d items, want 0 (non-archived item must not appear)", len(items))
	}
}

func TestDB_Open_migrates_existing_db(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.db"

	// First open: create table without bookmark_id (simulate pre-retention DB).
	db, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	// Insert a pre-migration row with a past timestamp directly.
	if _, err := db.conn.Exec(
		`INSERT INTO sent_items (guid, sent_at) VALUES (?, ?)`,
		"pre-migration", "2020-01-01 00:00:00",
	); err != nil {
		t.Fatalf("insert pre-migration: %v", err)
	}
	db.Close()

	// Second open: migration should add bookmark_id column without error.
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second open (migration): %v", err)
	}
	defer db2.Close()

	// Insert directly with past timestamp via the now-migrated column.
	if _, err := db2.conn.Exec(
		`INSERT INTO sent_items (guid, bookmark_id, sent_at) VALUES (?, ?, ?)`,
		"post-migration", int64(99), "2020-01-01 00:00:00",
	); err != nil {
		t.Fatalf("insert after migration: %v", err)
	}

	items, err := db2.OldItemsOutside(1, nil)
	if err != nil {
		t.Fatalf("OldItems after migration: %v", err)
	}
	// Should return both: the pre-migration row (no bookmark_id) and the new one.
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
}

func TestDB_per_feed_selection(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	for _, r := range [][]any{{"a", "feedA"}, {"b", "feedB"}, {"gone", "removed"}, {"legacy", nil}} {
		if _, err := db.conn.Exec(
			`INSERT INTO sent_items (guid, feed_url, sent_at, archived_at) VALUES (?, ?, '2020-01-01 00:00:00', NULL)`, r...,
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	guids := func(items []SentItem, err error) string {
		if err != nil {
			t.Fatal(err)
		}
		s := ""
		for _, i := range items {
			s += i.GUID + ","
		}
		return s
	}
	if got := guids(db.OldItems(1, "feedA")); got != "a," {
		t.Errorf("OldItems feedA: %q", got)
	}
	if got := guids(db.OldItemsOutside(1, []string{"feedA", "feedB"})); got != "gone,legacy," {
		t.Errorf("OldItemsOutside: %q", got)
	}
	if _, err := db.conn.Exec(`UPDATE sent_items SET archived_at = '2020-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}
	if got := guids(db.ArchivedItems(1, "feedB")); got != "b," {
		t.Errorf("ArchivedItems feedB: %q", got)
	}
	if got := guids(db.ArchivedItemsOutside(1, []string{"feedA", "feedB"})); got != "gone,legacy," {
		t.Errorf("ArchivedItemsOutside: %q", got)
	}
}

func TestDB_feed_url_migration(t *testing.T) {
	path := t.TempDir() + "/s.db"
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path) // reopen: ALTER must be idempotent
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()
	if err := db.MarkSentWithID("g", 1, "feedA"); err != nil {
		t.Fatal(err)
	}
	var u string
	if err := db.conn.QueryRow(`SELECT feed_url FROM sent_items WHERE guid='g'`).Scan(&u); err != nil || u != "feedA" {
		t.Errorf("feed_url: %q, %v", u, err)
	}
}
