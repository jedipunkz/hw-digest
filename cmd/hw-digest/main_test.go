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
	article := item{Title: "A & B", Link: "https://example.com/a?x=1&y=2", Source: "Example", Summary: "要約", ImageURL: "https://example.com/image.jpg", Published: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)}
	if err := writeFeed(dir, feedSet{Title: "Test", Description: "Description"}, []item{article}, article.Published); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil || !strings.Contains(string(page), "A &amp; B") || !strings.Contains(string(page), "Example") || !strings.Contains(string(page), "summary") || !strings.Contains(string(page), "image.jpg") {
		t.Fatalf("page = %s, err = %v", page, err)
	}
	rss, err := os.ReadFile(filepath.Join(dir, "index.xml"))
	if err != nil || !strings.Contains(string(rss), "<description><![CDATA[") || !strings.Contains(string(rss), "media:content") {
		t.Fatalf("rss = %s, err = %v", rss, err)
	}
}

func TestExtractReadableTextScopesToMainAndDedups(t *testing.T) {
	page := `<html><body>
	<nav><ul><li>ナビゲーションのリンクでも十分に長い無関係なテキストが入りますよここに</li></ul></nav>
	<article>
	<h1>本物の記事タイトルがここに入りますこれは十分に長い見出しのテキストです</h1>
	<p>これは記事の本文であり重要な数値や事実を含む十分に長い段落のテキストです。</p>
	<p>これは記事の本文であり重要な数値や事実を含む十分に長い段落のテキストです。</p>
	<p>二つめの段落も十分に長くて記事の内容を補足する重要な情報を持っています。</p>
	</article>
	<aside><ul><li>関連記事へのリンクで十分に長いテキストが並んでいますがこれは無関係です</li></ul></aside>
	</body></html>`
	out := extractReadableText(page, 5000)
	if got := strings.Count(out, "重要な数値や事実を含む十分に長い段落"); got != 1 {
		t.Fatalf("duplicate paragraph not deduplicated (count=%d): %q", got, out)
	}
	if strings.Contains(out, "ナビゲーションのリンク") || strings.Contains(out, "関連記事へのリンク") {
		t.Fatalf("navigation/related boilerplate leaked: %q", out)
	}
	if !strings.Contains(out, "本物の記事タイトル") || !strings.Contains(out, "二つめの段落") {
		t.Fatalf("main content missing: %q", out)
	}
}

func TestExtractReadableTextFallsBackWithoutMainContainer(t *testing.T) {
	page := `<html><body><div><p>article 要素が無いページでも本文の段落はしっかり抽出されるべきです。</p></div></body></html>`
	if out := extractReadableText(page, 5000); !strings.Contains(out, "本文の段落はしっかり抽出される") {
		t.Fatalf("fallback extraction failed: %q", out)
	}
}

func TestMergeArticlesPreservesArchiveAndReplacesSameLink(t *testing.T) {
	old := item{Title: "old", Link: "https://example.com/old", Published: time.Unix(1, 0)}
	updated := item{Title: "updated", Link: old.Link, Published: time.Unix(2, 0)}
	newer := item{Title: "new", Link: "https://example.com/new", Published: time.Unix(3, 0)}
	merged := mergeArticles([]item{old}, []item{updated, newer})
	if len(merged) != 2 || merged[0].Title != "new" || merged[1].Title != "updated" {
		t.Fatalf("merged = %#v", merged)
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
