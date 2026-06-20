package main

import (
	"os"
	"path/filepath"
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
	atom := `<feed><entry><title>Atom</title><link href="https://example.com/atom"/><published>2006-01-02T15:04:05Z</published></entry></feed>`
	items, err = parseFeed(strings.NewReader(atom), "test")
	if err != nil || len(items) != 1 || items[0].Link != "https://example.com/atom" {
		t.Fatalf("atom items = %#v, err = %v", items, err)
	}
}

func TestWriteFeedRendersArticleList(t *testing.T) {
	dir := t.TempDir()
	article := item{Title: "A & B", Link: "https://example.com/a?x=1&y=2", Source: "Example", Published: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)}
	if err := writeFeed(dir, feedSet{Title: "Test", Description: "Description"}, []item{article}, article.Published, 3*time.Hour); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil || !strings.Contains(string(page), "A &amp; B") || !strings.Contains(string(page), "Example") {
		t.Fatalf("page = %s, err = %v", page, err)
	}
}

func TestParseLookback(t *testing.T) {
	for _, value := range []string{"", "3h", "24h", "7days"} {
		if _, err := parseLookback(value); err != nil {
			t.Fatalf("parseLookback(%q): %v", value, err)
		}
	}
	if _, err := parseLookback("6h"); err == nil {
		t.Fatal("parseLookback accepted an unsupported duration")
	}
}

func TestMatchesCategories(t *testing.T) {
	if !matchesCategories(item{Category: "HARDWARE"}, []string{"hardware"}) {
		t.Fatal("matching category was rejected")
	}
	if matchesCategories(item{Category: "GAME"}, []string{"HARDWARE"}) {
		t.Fatal("non-matching category was accepted")
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
