package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseFeedSupportsRSSAndAtomDates(t *testing.T) {
	feed := `<rss><channel><item><title>RSS</title><link>https://example.com/rss?utm_source=x</link><pubDate>Mon, 02 Jan 2006 15:04:05 +0000</pubDate></item></channel></rss>`
	items, err := parseFeed(strings.NewReader(feed), "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "RSS" {
		t.Fatalf("items = %#v", items)
	}
	if items[0].Link != "https://example.com/rss" {
		t.Fatalf("link = %q", items[0].Link)
	}
	if _, err := parseTime("2006-01-02T15:04:05Z"); err != nil {
		t.Fatal(err)
	}
	atom := `<feed><entry><title>Atom</title><link href="https://example.com/atom"/><updated>2006-01-02T15:04:05Z</updated></entry></feed>`
	items, err = parseFeed(strings.NewReader(atom), "test")
	if err != nil || len(items) != 1 || items[0].Link != "https://example.com/atom" {
		t.Fatalf("atom items = %#v, err = %v", items, err)
	}
}

func TestRunWritesOnlyRecentItemsAndDeduplicates(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	known := seen{itemKey("https://example.com/old"): now}
	pruneSeen(known, now)
	if len(known) != 1 {
		t.Fatal("recent seen item was removed")
	}
	known[itemKey("https://example.com/stale")] = now.Add(-seenFor - time.Hour)
	pruneSeen(known, now)
	if len(known) != 1 {
		t.Fatal("stale seen item was retained")
	}
}
