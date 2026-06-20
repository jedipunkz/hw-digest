// hw-digest collects recent hardware news feeds and writes GitHub Pages assets.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	window    = 3 * time.Hour
	seenFor   = 30 * 24 * time.Hour
	userAgent = "hw-digest/1.0 (+https://github.com/jedipunkz/hw-digest)"
)

type source struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type feedSet struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Path        string   `json:"path"`
	Sources     []source `json:"sources"`
}

type config struct {
	Feeds []feedSet `json:"feeds"`
}

type item struct {
	Title     string
	Link      string
	Published time.Time
	Source    string
}

type sourceResult struct {
	Source  string
	Fetched int
	Recent  int
	Err     error
}

type collection struct {
	Items   []item
	Sources []sourceResult
}

type feedLink string

func (l *feedLink) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for _, attr := range start.Attr {
		if attr.Name.Local == "href" && strings.TrimSpace(attr.Value) != "" {
			*l = feedLink(attr.Value)
		}
	}
	var text string
	if err := d.DecodeElement(&text, &start); err != nil {
		return err
	}
	if *l == "" {
		*l = feedLink(strings.TrimSpace(text))
	}
	return nil
}

type seen map[string]time.Time

func main() {
	if err := run(context.Background(), time.Now().UTC(), "sources.json", "data/seen.json", "docs"); err != nil {
		fmt.Fprintln(os.Stderr, "hw-digest:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, now time.Time, configPath, seenPath, outputDir string) error {
	cfg, err := readConfig(configPath)
	if err != nil {
		return err
	}
	known, err := readSeen(seenPath)
	if err != nil {
		return err
	}
	pruneSeen(known, now)
	client := &http.Client{Timeout: 30 * time.Second}
	for _, set := range cfg.Feeds {
		collected, err := collect(ctx, client, set.Sources, now)
		if err != nil {
			return fmt.Errorf("collect %s: %w", set.Title, err)
		}
		fresh := make([]item, 0, len(collected.Items))
		for _, article := range collected.Items {
			key := itemKey(article.Link)
			if _, exists := known[key]; exists {
				continue
			}
			known[key] = now
			fresh = append(fresh, article)
		}
		sort.Slice(fresh, func(i, j int) bool { return fresh[i].Published.After(fresh[j].Published) })
		if err := writeFeed(filepath.Join(outputDir, set.Path), set, fresh, now); err != nil {
			return err
		}
		writeSummary(set, collected.Sources, len(fresh))
	}
	return writeSeen(seenPath, known)
}

func readConfig(path string) (config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return config{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cfg.Feeds) == 0 {
		return config{}, errors.New("sources.json has no feeds")
	}
	return cfg, nil
}

func collect(ctx context.Context, client *http.Client, sources []source, now time.Time) (collection, error) {
	var result collection
	succeeded := 0
	for _, src := range sources {
		articles, err := fetch(ctx, client, src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", src.Name, err)
			result.Sources = append(result.Sources, sourceResult{Source: src.Name, Err: err})
			continue
		}
		succeeded++
		recent := 0
		for _, article := range articles {
			if article.Published.IsZero() || article.Published.After(now.Add(10*time.Minute)) || article.Published.Before(now.Add(-window)) {
				continue
			}
			recent++
			result.Items = append(result.Items, article)
		}
		result.Sources = append(result.Sources, sourceResult{Source: src.Name, Fetched: len(articles), Recent: recent})
	}
	if succeeded == 0 {
		return collection{}, errors.New("all source requests failed; existing feed was left unchanged")
	}
	return result, nil
}

func fetch(ctx context.Context, client *http.Client, src source) ([]item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", src.URL, resp.Status)
	}
	return parseFeed(io.LimitReader(resp.Body, 8<<20), src.Name)
}

func parseFeed(r io.Reader, sourceName string) ([]item, error) {
	decoder := xml.NewDecoder(r)
	var articles []item
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok || (start.Name.Local != "item" && start.Name.Local != "entry") {
			continue
		}
		var raw struct {
			Title     string   `xml:"title"`
			Link      feedLink `xml:"link"`
			GUID      string   `xml:"guid"`
			Date      string   `xml:"pubDate"`
			Published string   `xml:"published"`
			Updated   string   `xml:"updated"`
			Issued    string   `xml:"issued"`
			DateDC    string   `xml:"date"`
		}
		if err := decoder.DecodeElement(&raw, &start); err != nil {
			return nil, err
		}
		link := strings.TrimSpace(string(raw.Link))
		if link == "" {
			link = strings.TrimSpace(raw.GUID)
		}
		if raw.Title == "" || link == "" {
			continue
		}
		date := firstNonEmpty(raw.Date, raw.Published, raw.Updated, raw.Issued, raw.DateDC)
		published, err := parseTime(date)
		if err != nil {
			continue
		}
		articles = append(articles, item{Title: strings.TrimSpace(raw.Title), Link: normalizeURL(link), Published: published, Source: sourceName})
	}
	return articles, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func parseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported publication date %q", value)
}

func normalizeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	u.Fragment = ""
	for key := range u.Query() {
		if strings.HasPrefix(strings.ToLower(key), "utm_") {
			q := u.Query()
			q.Del(key)
			u.RawQuery = q.Encode()
		}
	}
	return u.String()
}

func itemKey(link string) string {
	sum := sha256.Sum256([]byte(normalizeURL(link)))
	return hex.EncodeToString(sum[:])
}

func readSeen(path string) (seen, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return seen{}, nil
	}
	if err != nil {
		return nil, err
	}
	var values map[string]string
	if err := json.Unmarshal(b, &values); err != nil {
		return nil, err
	}
	result := seen{}
	for key, value := range values {
		if t, err := time.Parse(time.RFC3339, value); err == nil {
			result[key] = t
		}
	}
	return result, nil
}

func pruneSeen(values seen, now time.Time) {
	for key, added := range values {
		if added.Before(now.Add(-seenFor)) {
			delete(values, key)
		}
	}
}

func writeSeen(path string, values seen) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	serialized := make(map[string]string, len(values))
	for key, added := range values {
		serialized[key] = added.UTC().Format(time.RFC3339)
	}
	b, err := json.MarshalIndent(serialized, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func writeFeed(dir string, set feedSet, articles []item, now time.Time) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var feed strings.Builder
	feed.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<rss version=\"2.0\"><channel>\n")
	fmt.Fprintf(&feed, "<title>%s</title><description>%s</description><link>./</link><lastBuildDate>%s</lastBuildDate>\n", escape(set.Title), escape(set.Description), now.Format(time.RFC1123Z))
	for _, article := range articles {
		fmt.Fprintf(&feed, "<item><title>%s</title><link>%s</link><guid isPermaLink=\"true\">%s</guid><pubDate>%s</pubDate><source url=\"%s\">%s</source></item>\n", escape(article.Title), escape(article.Link), escape(article.Link), article.Published.Format(time.RFC1123Z), escape(article.Link), escape(article.Source))
	}
	feed.WriteString("</channel></rss>\n")
	if err := os.WriteFile(filepath.Join(dir, "index.xml"), []byte(feed.String()), 0o644); err != nil {
		return err
	}
	var page strings.Builder
	fmt.Fprintf(&page, "<!doctype html><html lang=\"ja\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>%s</title><link rel=\"alternate\" type=\"application/rss+xml\" title=\"%s\" href=\"index.xml\"></head><body><h1>%s</h1><p>%s</p><p><a href=\"index.xml\">RSSを購読する</a></p>", escape(set.Title), escape(set.Title), escape(set.Title), escape(set.Description))
	if len(articles) == 0 {
		page.WriteString("<p>直近3時間の新着記事はありません。</p>")
	} else {
		page.WriteString("<ul>")
		for _, article := range articles {
			fmt.Fprintf(&page, "<li><a href=\"%s\">%s</a> — %s（%s）</li>", escape(article.Link), escape(article.Title), escape(article.Source), article.Published.In(time.Local).Format("2006-01-02 15:04 MST"))
		}
		page.WriteString("</ul>")
	}
	page.WriteString("</body></html>\n")
	return os.WriteFile(filepath.Join(dir, "index.html"), []byte(page.String()), 0o644)
}

func escape(s string) string { return html.EscapeString(s) }

func writeSummary(set feedSet, results []sourceResult, published int) {
	var report strings.Builder
	fmt.Fprintf(&report, "## %s\n\n公開記事: **%d件**\n\n|収集元|取得|直近3時間|状態|\n|---|---:|---:|---|\n", set.Title, published)
	for _, result := range results {
		state := "ok"
		if result.Err != nil {
			state = result.Err.Error()
		}
		fmt.Fprintf(&report, "|%s|%d|%d|%s|\n", result.Source, result.Fetched, result.Recent, strings.ReplaceAll(state, "|", "\\|"))
	}
	fmt.Fprintln(os.Stderr, report.String())
	if summaryPath := os.Getenv("GITHUB_STEP_SUMMARY"); summaryPath != "" {
		if file, err := os.OpenFile(summaryPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			defer file.Close()
			_, _ = file.WriteString(report.String() + "\n")
		}
	}
}
