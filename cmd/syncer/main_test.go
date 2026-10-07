package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danielgroothuis/rss-feed-to-instapaper/internal/config"
	"github.com/danielgroothuis/rss-feed-to-instapaper/internal/feed"
	"github.com/danielgroothuis/rss-feed-to-instapaper/internal/state"
)

func TestSortPending_order(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)

	items := []pendingItem{
		{item: feed.Item{GUID: "newest", PublishedAt: &t3}},
		{item: feed.Item{GUID: "nodate", PublishedAt: nil}},
		{item: feed.Item{GUID: "oldest", PublishedAt: &t1}},
		{item: feed.Item{GUID: "middle", PublishedAt: &t2}},
	}

	sortPending(items)

	want := []string{"nodate", "oldest", "middle", "newest"}
	for i, w := range want {
		if items[i].item.GUID != w {
			t.Errorf("position %d: got %q, want %q", i, items[i].item.GUID, w)
		}
	}
}

func TestSortPending_all_nil(t *testing.T) {
	items := []pendingItem{
		{item: feed.Item{GUID: "a", PublishedAt: nil}},
		{item: feed.Item{GUID: "b", PublishedAt: nil}},
	}
	sortPending(items)
	// order between nil-date items is undefined; just verify no panic
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
}

func TestSortPending_empty(t *testing.T) {
	sortPending(nil) // must not panic
}

func loadFeed(t *testing.T, feedYAML string) config.Feed {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("feeds:\n"+feedYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg.Feeds[0]
}

func titledItems(titles ...string) []feed.Item {
	items := make([]feed.Item, len(titles))
	for i, title := range titles {
		items[i] = feed.Item{GUID: "guid-" + title, Title: title}
	}
	return items
}

func guids(items []feed.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.GUID
	}
	return out
}

func neverSent(string) (bool, error) { return false, nil }

func TestSelectNew_exclude(t *testing.T) {
	f := loadFeed(t, "  - url: http://x\n    label: X\n    exclude: [sponsored]\n")
	got := selectNew(f, titledItems("News", "Sponsored: buy", "More"), false, 0, neverSent)
	want := []string{"guid-News", "guid-More"}
	if g := guids(got); len(g) != 2 || g[0] != want[0] || g[1] != want[1] {
		t.Errorf("got %v, want %v", g, want)
	}
}

func TestSelectNew_includeOnly(t *testing.T) {
	f := loadFeed(t, "  - url: http://x\n    label: X\n    include: [golang]\n")
	got := selectNew(f, titledItems("Golang 1.25", "Rust news", "Why GOLANG"), false, 0, neverSent)
	if len(got) != 2 || got[0].Title != "Golang 1.25" || got[1].Title != "Why GOLANG" {
		t.Errorf("unexpected items: %v", guids(got))
	}
}

func TestSelectNew_filteredItemsSkipDedupCheck(t *testing.T) {
	f := loadFeed(t, "  - url: http://x\n    label: X\n    exclude: [skip]\n")
	var checked []string
	isSent := func(g string) (bool, error) { checked = append(checked, g); return false, nil }
	selectNew(f, titledItems("keep", "skip me"), false, 0, isSent)
	if len(checked) != 1 || checked[0] != "guid-keep" {
		t.Errorf("dedup checked %v, want only guid-keep", checked)
	}
}

func TestSelectNew_maxInitialCountsOnlyMatches(t *testing.T) {
	f := loadFeed(t, "  - url: http://x\n    label: X\n    exclude: [skip]\n")
	items := titledItems("skip 1", "skip 2", "a", "skip 3", "b", "c")
	got := selectNew(f, items, true, 2, neverSent)
	if len(got) != 2 || got[0].Title != "a" || got[1].Title != "b" {
		t.Errorf("got %v, want [a b]", guids(got))
	}
}

func TestSelectNew_maxInitialIgnoredForKnownFeed(t *testing.T) {
	f := loadFeed(t, "  - url: http://x\n    label: X\n")
	got := selectNew(f, titledItems("a", "b", "c"), false, 2, neverSent)
	if len(got) != 3 {
		t.Errorf("got %d items, want 3", len(got))
	}
}

func TestDueForDelete_perFeed(t *testing.T) {
	zero, ten := 0, 10
	cfg := &config.Config{MaxAgeDays: 1, ArchiveRetentionDays: 30, Feeds: []config.Feed{
		{URL: "keep", ArchiveRetentionDays: &zero},
		{URL: "short", ArchiveRetentionDays: &ten},
		{URL: "plain"},
	}}
	now := time.Now()
	at := now.Add(-20 * 24 * time.Hour)
	u := func(s string) *string { return &s }
	items := []state.SentItem{
		{GUID: "keep", FeedURL: u("keep"), ArchivedAt: &at},
		{GUID: "short", FeedURL: u("short"), ArchivedAt: &at},
		{GUID: "plain", FeedURL: u("plain"), ArchivedAt: &at},
		{GUID: "removed", FeedURL: u("gone"), ArchivedAt: &at},
		{GUID: "legacy", ArchivedAt: &at},
	}
	got := dueForDelete(cfg, items, now)
	if len(got) != 1 || got[0].GUID != "short" {
		t.Errorf("got %+v, want only short (20d old: 10d feed due, 30d global not, 0 never; legacy kept while a feed opts out)", got)
	}
	if minRetention(cfg) != 10 {
		t.Errorf("minRetention: %d", minRetention(cfg))
	}
	cfg.Feeds[0].ArchiveRetentionDays = nil // no opt-out: legacy falls back to global
	if retentionFor(cfg, items[4]) != 30 {
		t.Error("legacy row should use global when no feed opts out")
	}
}

func TestDueForArchive_perFeed(t *testing.T) {
	three := 3
	cfg := &config.Config{MaxAgeDays: 1, Feeds: []config.Feed{{URL: "slow", MaxAgeDays: &three}, {URL: "fast"}}}
	now := time.Now()
	sent := now.Add(-2 * 24 * time.Hour)
	u := func(s string) *string { return &s }
	items := []state.SentItem{{GUID: "slow", FeedURL: u("slow"), SentAt: sent}, {GUID: "fast", FeedURL: u("fast"), SentAt: sent}, {GUID: "legacy", SentAt: sent}}
	got := dueForArchive(cfg, items, now)
	if len(got) != 2 || got[0].GUID != "fast" || got[1].GUID != "legacy" {
		t.Errorf("got %+v", got)
	}
}
