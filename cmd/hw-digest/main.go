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
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	seenFor         = 30 * 24 * time.Hour
	userAgent       = "hw-digest/1.0 (+https://github.com/jedipunkz/hw-digest)"
	maxItemsPerFeed = 15
)

type source struct {
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	Categories []string `json:"categories"`
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
	Category  string
	Summary   string
	ImageURL  string
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
	lookback, err := parseLookback(os.Getenv("LOOKBACK"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "hw-digest:", err)
		os.Exit(2)
	}
	if err := run(context.Background(), time.Now().UTC(), lookback, "sources.json", "data/seen.json", "docs"); err != nil {
		fmt.Fprintln(os.Stderr, "hw-digest:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, now time.Time, lookback time.Duration, configPath, seenPath, outputDir string) error {
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
	summarizer := newSummarizer(client, os.Getenv("GITHUB_TOKEN"))
	for _, set := range cfg.Feeds {
		collected, err := collect(ctx, client, set.Sources, now, lookback)
		if err != nil {
			return fmt.Errorf("collect %s: %w", set.Title, err)
		}
		sort.Slice(collected.Items, func(i, j int) bool { return collected.Items[i].Published.After(collected.Items[j].Published) })
		fresh := make([]item, 0, min(len(collected.Items), maxItemsPerFeed))
		for _, article := range collected.Items {
			if len(fresh) >= maxItemsPerFeed {
				break
			}
			key := itemKey(article.Link)
			if _, exists := known[key]; exists {
				continue
			}
			known[key] = now
			fresh = append(fresh, article)
		}
		enrichItems(ctx, client, summarizer, fresh)
		if err := writeFeed(filepath.Join(outputDir, set.Path), set, fresh, now, lookback); err != nil {
			return err
		}
		writeSummary(set, collected.Sources, len(fresh))
	}
	return writeSeen(seenPath, known)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func parseLookback(value string) (time.Duration, error) {
	if value == "" {
		return 3 * time.Hour, nil
	}
	if value == "7days" {
		return 7 * 24 * time.Hour, nil
	}
	lookback, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid LOOKBACK %q: %w", value, err)
	}
	for _, allowed := range []time.Duration{3 * time.Hour, 24 * time.Hour, 7 * 24 * time.Hour} {
		if lookback == allowed {
			return lookback, nil
		}
	}
	return 0, fmt.Errorf("LOOKBACK must be one of 3h, 24h, or 7days; got %q", value)
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

func collect(ctx context.Context, client *http.Client, sources []source, now time.Time, lookback time.Duration) (collection, error) {
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
			if !matchesCategories(article, src.Categories) || article.Published.IsZero() || article.Published.After(now.Add(10*time.Minute)) || article.Published.Before(now.Add(-lookback)) {
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

func matchesCategories(article item, categories []string) bool {
	if len(categories) == 0 {
		return true
	}
	for _, category := range categories {
		if strings.EqualFold(strings.TrimSpace(article.Category), strings.TrimSpace(category)) {
			return true
		}
	}
	return false
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

type articleMetadata struct{ Description, ImageURL, Text string }

type textTranslator struct{ client *http.Client }
type articleSummarizer struct {
	client *http.Client
	token  string
}

func newSummarizer(client *http.Client, token string) *articleSummarizer {
	return &articleSummarizer{client: client, token: token}
}

func enrichItems(ctx context.Context, client *http.Client, summarizer *articleSummarizer, items []item) {
	translator := &textTranslator{client: client}
	for i := range items {
		metadata, err := fetchArticleMetadata(ctx, client, items[i].Link)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: read %s: %v\n", items[i].Link, err)
			continue
		}
		items[i].ImageURL = metadata.ImageURL
		input := strings.TrimSpace(strings.Join([]string{items[i].Title, metadata.Description, metadata.Text}, "\n\n"))
		if input == "" {
			continue
		}
		translated, err := translator.translate(ctx, trimRunes(input, 5000))
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: translate %s: %v\n", items[i].Link, err)
			translated = trimRunes(input, 800)
		}
		summary, err := summarizer.summarize(ctx, items[i].Title, translated)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: summarize %s: %v\n", items[i].Link, err)
			summary = trimRunes(translated, 900)
		}
		items[i].Summary = summary
	}
}

func fetchArticleMetadata(ctx context.Context, client *http.Client, rawURL string) (articleMetadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return articleMetadata{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.1")
	resp, err := client.Do(req)
	if err != nil {
		return articleMetadata{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return articleMetadata{}, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return articleMetadata{}, err
	}
	text := string(body)
	return articleMetadata{Description: firstMeta(text, "description"), ImageURL: firstMeta(text, "image"), Text: extractReadableText(text, 5000)}, nil
}

func (t *textTranslator) translate(ctx context.Context, text string) (string, error) {
	params := url.Values{"client": {"gtx"}, "sl": {"auto"}, "tl": {"ja"}, "dt": {"t"}, "q": {text}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://translate.googleapis.com/translate_a/single?"+params.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("google translate: %s", resp.Status)
	}
	var parsed []any
	if err := json.Unmarshal(data, &parsed); err != nil || len(parsed) == 0 {
		return "", errors.New("invalid Google Translate response")
	}
	sentences, ok := parsed[0].([]any)
	if !ok {
		return "", errors.New("Google Translate returned no sentences")
	}
	var out strings.Builder
	for _, raw := range sentences {
		if sentence, ok := raw.([]any); ok && len(sentence) > 0 {
			if part, ok := sentence[0].(string); ok {
				out.WriteString(part)
			}
		}
	}
	if out.Len() == 0 {
		return "", errors.New("Google Translate returned an empty translation")
	}
	return html.UnescapeString(out.String()), nil
}

func (s *articleSummarizer) summarize(ctx context.Context, title, translated string) (string, error) {
	if s.token == "" {
		return "", errors.New("GITHUB_TOKEN is not set")
	}
	prompt := fmt.Sprintf("次の記事を日本語で250〜400文字に要約してください。製品・技術・数値など重要な事実を優先し、推測は書かないでください。\n\nタイトル: %s\n\n本文:\n%s", title, trimRunes(translated, 5000))
	payload := map[string]any{"model": "openai/gpt-4.1-mini", "messages": []map[string]string{{"role": "user", "content": prompt}}, "max_tokens": 600, "temperature": 0.2}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://models.github.ai/inference/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("GitHub Models: %s", resp.Status)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &result); err != nil || len(result.Choices) == 0 {
		return "", errors.New("invalid GitHub Models response")
	}
	return strings.TrimSpace(result.Choices[0].Message.Content), nil
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
			Category  string   `xml:"category"`
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
		articles = append(articles, item{Title: strings.TrimSpace(raw.Title), Link: normalizeURL(link), Published: published, Source: sourceName, Category: strings.TrimSpace(raw.Category)})
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

func writeFeed(dir string, set feedSet, articles []item, now time.Time, lookback time.Duration) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var feed strings.Builder
	feed.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<rss version=\"2.0\" xmlns:media=\"http://search.yahoo.com/mrss/\"><channel>\n")
	fmt.Fprintf(&feed, "<title>%s</title><description>%s</description><link>./</link><lastBuildDate>%s</lastBuildDate>\n", escape(set.Title), escape(set.Description), now.Format(time.RFC1123Z))
	for _, article := range articles {
		description := article.Summary
		if description == "" {
			description = article.Title
		}
		htmlDescription := "<p>" + html.EscapeString(description) + "</p>"
		if article.ImageURL != "" {
			htmlDescription += "<p><img src=\"" + html.EscapeString(article.ImageURL) + "\" alt=\"" + html.EscapeString(article.Title) + "\"></p>"
		}
		fmt.Fprintf(&feed, "<item><title>%s</title><link>%s</link><guid isPermaLink=\"true\">%s</guid><pubDate>%s</pubDate><description><![CDATA[%s]]></description><source url=\"%s\">%s</source>", escape(article.Title), escape(article.Link), escape(article.Link), article.Published.Format(time.RFC1123Z), htmlDescription, escape(article.Link), escape(article.Source))
		if article.ImageURL != "" {
			fmt.Fprintf(&feed, "<media:content url=\"%s\" medium=\"image\"/><media:thumbnail url=\"%s\"/>", escape(article.ImageURL), escape(article.ImageURL))
		}
		feed.WriteString("</item>\n")
	}
	feed.WriteString("</channel></rss>\n")
	if err := os.WriteFile(filepath.Join(dir, "index.xml"), []byte(feed.String()), 0o644); err != nil {
		return err
	}
	var page strings.Builder
	fmt.Fprintf(&page, "<!doctype html><html lang=\"ja\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>%s</title><link rel=\"alternate\" type=\"application/rss+xml\" title=\"%s\" href=\"index.xml\"></head><body><h1>%s</h1><p>%s</p><p>対象期間: 直近%s</p><p><a href=\"index.xml\">RSSを購読する</a></p>", escape(set.Title), escape(set.Title), escape(set.Title), escape(set.Description), formatLookback(lookback))
	if len(articles) == 0 {
		fmt.Fprintf(&page, "<p>直近%sの新着記事はありません。</p>", formatLookback(lookback))
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

func firstMeta(body, name string) string {
	patterns := []string{
		`<meta[^>]+name=["']` + regexp.QuoteMeta(name) + `["'][^>]+content=["']([^"']+)["'][^>]*>`,
		`<meta[^>]+content=["']([^"']+)["'][^>]+name=["']` + regexp.QuoteMeta(name) + `["'][^>]*>`,
		`<meta[^>]+property=["']og:` + regexp.QuoteMeta(name) + `["'][^>]+content=["']([^"']+)["'][^>]*>`,
		`<meta[^>]+content=["']([^"']+)["'][^>]+property=["']og:` + regexp.QuoteMeta(name) + `["'][^>]*>`,
	}
	for _, pattern := range patterns {
		if match := regexp.MustCompile("(?is)" + pattern).FindStringSubmatch(body); len(match) > 1 {
			return html.UnescapeString(strings.TrimSpace(match[1]))
		}
	}
	return ""
}

func extractReadableText(body string, maxChars int) string {
	for _, tag := range []string{"script", "style", "noscript"} {
		body = regexp.MustCompile(`(?is)<`+tag+`[^>]*>.*?</`+tag+`\s*>`).ReplaceAllString(body, " ")
	}
	var chunks []string
	for _, match := range regexp.MustCompile(`(?is)<(p|h1|h2|h3|li|blockquote)[^>]*>(.*?)</\s*(p|h1|h2|h3|li|blockquote)\s*>`).FindAllStringSubmatch(body, -1) {
		text := htmlToText(match[2])
		if len([]rune(text)) >= 30 {
			chunks = append(chunks, text)
		}
	}
	if len(chunks) == 0 {
		chunks = append(chunks, htmlToText(body))
	}
	return trimRunes(strings.Join(chunks, "\n\n"), maxChars)
}

func htmlToText(input string) string {
	input = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "</p>", "\n\n", "</li>", "\n").Replace(input)
	input = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(input, " ")
	return strings.TrimSpace(strings.Join(strings.Fields(html.UnescapeString(input)), " "))
}

func trimRunes(input string, max int) string {
	runes := []rune(strings.TrimSpace(input))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}

func formatLookback(lookback time.Duration) string {
	if lookback%(24*time.Hour) == 0 {
		return fmt.Sprintf("%d日", int(lookback/(24*time.Hour)))
	}
	return fmt.Sprintf("%d時間", int(lookback/time.Hour))
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
