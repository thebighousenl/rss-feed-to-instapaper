package main

import (
	"log"
	"os"
	"sort"
	"time"

	"github.com/danielgroothuis/rss-feed-to-instapaper/internal/config"
	"github.com/danielgroothuis/rss-feed-to-instapaper/internal/feed"
	"github.com/danielgroothuis/rss-feed-to-instapaper/internal/instapaper"
	"github.com/danielgroothuis/rss-feed-to-instapaper/internal/state"
)

type pendingItem struct {
	item      feed.Item
	feedLabel string
	feedURL   string
}

func sortPending(items []pendingItem) {
	sort.Slice(items, func(i, j int) bool {
		pi, pj := items[i].item.PublishedAt, items[j].item.PublishedAt
		if pi == nil && pj == nil {
			return false
		}
		if pi == nil {
			return true
		}
		if pj == nil {
			return false
		}
		return pi.Before(*pj)
	})
}

// selectNew returns the items to add for a feed. Title filters are applied
// first, so filtered-out items are neither dedup-checked nor counted toward
// the initial-sync cap.
func selectNew(f config.Feed, items []feed.Item, isNew bool, maxInitial int, isSent func(string) (bool, error)) []feed.Item {
	var out []feed.Item
	for _, item := range items {
		if !f.Matches(item.Title) {
			continue
		}
		sent, err := isSent(item.GUID)
		if err != nil {
			log.Printf("ERROR check state for %s: %v", item.GUID, err)
			continue
		}
		if sent {
			continue
		}
		out = append(out, item)
	}
	if maxInitial > 0 && isNew && len(out) > maxInitial {
		out = out[:maxInitial]
	}
	return out
}

func findFeed(cfg *config.Config, url *string) (config.Feed, bool) {
	if url != nil {
		for _, f := range cfg.Feeds {
			if f.URL == *url {
				return f, true
			}
		}
	}
	return config.Feed{}, false
}

// maxAgeFor: the feed's threshold, or the global one for unknown/removed feeds.
func maxAgeFor(cfg *config.Config, item state.SentItem) int {
	if f, ok := findFeed(cfg, item.FeedURL); ok {
		return cfg.MaxAgeFor(f)
	}
	return cfg.MaxAgeDays
}

// retentionFor returns 0 for "never delete". Rows with no feed_url (legacy,
// not yet backfilled) could belong to any feed, so they are kept while any
// feed explicitly opts out of deletion.
func retentionFor(cfg *config.Config, item state.SentItem) int {
	if f, ok := findFeed(cfg, item.FeedURL); ok {
		return cfg.RetentionFor(f)
	}
	if item.FeedURL == nil {
		for _, f := range cfg.Feeds {
			if f.ArchiveRetentionDays != nil && *f.ArchiveRetentionDays == 0 {
				return 0
			}
		}
	}
	return cfg.ArchiveRetentionDays
}

// minMaxAge is the smallest threshold in play, used as the SQL pre-filter.
func minMaxAge(cfg *config.Config) int {
	m := cfg.MaxAgeDays
	for _, f := range cfg.Feeds {
		if d := cfg.MaxAgeFor(f); d < m {
			m = d
		}
	}
	return m
}

// minRetention is the smallest positive threshold in play (0 = nothing is ever deleted).
func minRetention(cfg *config.Config) int {
	m := cfg.ArchiveRetentionDays
	for _, f := range cfg.Feeds {
		if d := cfg.RetentionFor(f); d > 0 && (m == 0 || d < m) {
			m = d
		}
	}
	return m
}

func dueForArchive(cfg *config.Config, items []state.SentItem, now time.Time) []state.SentItem {
	var out []state.SentItem
	for _, it := range items {
		if now.Sub(it.SentAt) >= time.Duration(maxAgeFor(cfg, it))*24*time.Hour {
			out = append(out, it)
		}
	}
	return out
}

func dueForDelete(cfg *config.Config, items []state.SentItem, now time.Time) []state.SentItem {
	var out []state.SentItem
	for _, it := range items {
		if d := retentionFor(cfg, it); d > 0 && it.ArchivedAt != nil && now.Sub(*it.ArchivedAt) >= time.Duration(d)*24*time.Hour {
			out = append(out, it)
		}
	}
	return out
}

func main() {
	configPath := envOr("CONFIG_PATH", "/config/config.yaml")
	statePath := envOr("STATE_PATH", "/data/state.db")
	username := requireEnv("INSTAPAPER_USERNAME")
	password := requireEnv("INSTAPAPER_PASSWORD")
	consumerKey := requireEnv("INSTAPAPER_CONSUMER_KEY")
	consumerSecret := requireEnv("INSTAPAPER_CONSUMER_SECRET")

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	db, err := state.Open(statePath)
	if err != nil {
		log.Fatalf("open state db: %v", err)
	}
	defer db.Close()

	client := instapaper.NewClient(consumerKey, consumerSecret, username, password)
	if err := client.Authenticate(); err != nil {
		log.Fatalf("authenticate with instapaper: %v", err)
	}

	var pending []pendingItem
	for _, f := range cfg.Feeds {
		if !f.IsEnabled() {
			log.Printf("[%s] skipped (disabled)", f.Label)
			continue
		}
		before := len(pending)
		items, err := feed.FetchItems(f.URL)
		if err != nil {
			log.Printf("ERROR fetch feed %s: %v", f.URL, err)
			continue
		}
		isNew, err := db.RegisterFeedIfNew(f.URL)
		if err != nil {
			log.Printf("ERROR register feed %s: %v", f.URL, err)
		}
		for _, item := range items {
			if err := db.BackfillFeedURL(item.GUID, f.URL); err != nil {
				log.Printf("ERROR backfill feed_url %s: %v", item.GUID, err)
			}
		}
		feedNew := selectNew(f, items, isNew, cfg.MaxInitialItems, db.IsSent)
		for _, item := range feedNew {
			pending = append(pending, pendingItem{item: item, feedLabel: f.Label, feedURL: f.URL})
		}
		log.Printf("[%s] %d new articles", f.Label, len(pending)-before)
	}

	if !cfg.DisableDateSort {
		sortPending(pending)
	}

	var added int
	for _, p := range pending {
		log.Printf("adding [%s] %q", p.feedLabel, p.item.Title)
		bookmarkID, err := client.Add(p.item.URL, p.item.Title)
		if err != nil {
			log.Printf("ERROR add to instapaper %s: %v", p.item.URL, err)
			continue
		}
		if err := db.MarkSentWithID(p.item.GUID, bookmarkID, p.feedURL); err != nil {
			log.Printf("ERROR mark sent %s: %v", p.item.GUID, err)
		}
		added++
	}

	var archived int
	aged, err := db.OldItems(minMaxAge(cfg))
	if err != nil {
		log.Printf("ERROR query old items: %v", err)
	}
	for _, item := range dueForArchive(cfg, aged, time.Now()) {
		if item.BookmarkID != nil {
			log.Printf("archiving %q (bookmark %d)", item.GUID, *item.BookmarkID)
			if err := client.Archive(*item.BookmarkID); err != nil {
				log.Printf("ERROR archive bookmark %d (%s): %v", *item.BookmarkID, item.GUID, err)
			}
			if err := db.MarkArchived(item.GUID); err != nil {
				log.Printf("ERROR mark archived %s: %v", item.GUID, err)
			}
		} else {
			if err := db.DeleteItem(item.GUID); err != nil {
				log.Printf("ERROR delete item %s: %v", item.GUID, err)
			}
		}
		archived++
	}

	var deleted int
	if cfg.ClearArchiveOnSync {
		archivedIDs, err := client.ListArchived()
		if err != nil {
			log.Printf("ERROR list archived bookmarks: %v", err)
		} else {
			for _, id := range archivedIDs {
				log.Printf("deleting archived bookmark %d", id)
				if err := client.Delete(id); err != nil {
					log.Printf("ERROR delete bookmark %d: %v", id, err)
				}
				deleted++
			}
		}
		// purge all DB-tracked archived items regardless of Instapaper result
		if toDelete, err := db.AllArchivedItems(); err != nil {
			log.Printf("ERROR query archived items: %v", err)
		} else {
			for _, item := range toDelete {
				if err := db.DeleteItem(item.GUID); err != nil {
					log.Printf("ERROR delete item %s: %v", item.GUID, err)
				}
			}
		}
	} else if minDays := minRetention(cfg); minDays > 0 {
		old, err := db.ArchivedItems(minDays)
		if err != nil {
			log.Printf("ERROR query archived items: %v", err)
		}
		for _, item := range dueForDelete(cfg, old, time.Now()) {
			if item.BookmarkID != nil {
				log.Printf("deleting %q (bookmark %d)", item.GUID, *item.BookmarkID)
				if err := client.Delete(*item.BookmarkID); err != nil {
					log.Printf("ERROR delete bookmark %d (%s): %v", *item.BookmarkID, item.GUID, err)
				}
			}
			if err := db.DeleteItem(item.GUID); err != nil {
				log.Printf("ERROR delete item %s: %v", item.GUID, err)
			}
			deleted++
		}
	}

	log.Printf("sync complete: %d added, %d archived, %d deleted", added, archived, deleted)
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
