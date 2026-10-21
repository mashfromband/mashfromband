package activity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var ErrNotFound = errors.New("GitHub resource not found")

type Client struct {
	headWaits     int
	writtenHead   string
	TrustedMain   string
	Base, Token   string
	HTTP          *http.Client
	RepositoryIDs map[string]int64
	pause         func(context.Context, time.Duration) error
}

func NewClient(token string) *Client {
	return &Client{Base: "https://api.github.com", Token: token, RepositoryIDs: map[string]int64{}, pause: delay, HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *Client) samePagePath(actual, expected string) bool {
	if actual == expected {
		return true
	}
	for name, id := range c.RepositoryIDs {
		prefix := "/repos/" + name + "/"
		if strings.HasPrefix(expected, prefix) && actual == fmt.Sprintf("/repositories/%d/", id)+strings.TrimPrefix(expected, prefix) {
			return true
		}
	}
	return false
}

func (c *Client) Call(ctx context.Context, method, path string, body any, out any) (http.Header, error) {
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(b)
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, errors.New("invalid GitHub endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Base, "/")+path, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "mashfromband-public-activity")
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("GitHub request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub %s failed (HTTP %d)", method, resp.StatusCode)
	}
	if out != nil {
		b, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024+1))
		if err != nil || len(b) > 32*1024*1024 {
			return nil, errors.New("GitHub response incomplete or too large")
		}
		if bytes.Equal(bytes.TrimSpace(b), []byte("null")) || json.Unmarshal(b, out) != nil {
			return nil, errors.New("GitHub response is not valid complete JSON")
		}
	}
	return resp.Header, nil
}

func (c *Client) Pages(ctx context.Context, path string, out any) error {
	var all []json.RawMessage
	next := path
	seen := map[string]bool{}
	for i := 0; next != ""; i++ {
		if i >= 30 || seen[next] {
			return errors.New("GitHub pagination incomplete")
		}
		seen[next] = true
		var page []json.RawMessage
		headers, err := c.Call(ctx, http.MethodGet, next, nil, &page)
		if err != nil {
			return err
		}
		if page == nil {
			return errors.New("GitHub list cannot be null")
		}
		all = append(all, page...)
		next = ""
		for _, part := range strings.Split(headers.Get("Link"), ",") {
			if !strings.Contains(part, `rel="next"`) {
				continue
			}
			a, b := strings.Index(part, "<"), strings.Index(part, ">")
			if a < 0 || b <= a {
				return errors.New("invalid GitHub pagination link")
			}
			u, err := url.Parse(part[a+1 : b])
			base, _ := url.Parse(c.Base)
			original, _ := url.Parse(path)
			if err != nil || u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil || !c.samePagePath(u.Path, original.Path) || u.Fragment != "" {
				return errors.New("GitHub pagination changed origin")
			}
			next = u.RequestURI()
		}
	}
	data, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

type publicRepo struct {
	ID         int64
	FullName   string `json:"full_name"`
	Private    *bool
	Visibility string
}
type label struct{ Name string }
type pull struct {
	Number               int64
	Title, Body, HTMLURL string
	MergedAt             *time.Time `json:"merged_at"`
	Labels               []label
	User                 struct{ Login string }
}

// GitHub uses snake_case for URLs; decode it explicitly rather than accepting
// an empty URL that could later masquerade as successful empty activity.
func (p *pull) UnmarshalJSON(b []byte) error {
	type alias pull
	v := struct {
		*alias
		URL string `json:"html_url"`
	}{alias: (*alias)(p)}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	p.HTMLURL = v.URL
	return nil
}

type release struct {
	ID                int64
	Name, Body        string
	URL               string     `json:"html_url"`
	Tag               string     `json:"tag_name"`
	Published         *time.Time `json:"published_at"`
	Draft, Prerelease bool
}

var comments = regexp.MustCompile(`(?s)<!--.*?-->`)
var tags = regexp.MustCompile(`<[^>]*>`)
var mdLink = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
var notProse = regexp.MustCompile(`^(?:[-*+]\s|\d+[.)]\s|>|\||\[[ xX]\])`)
var boring = regexp.MustCompile(`(?i)^(?:chore|ci|build|test|style|docs|refactor)(?:\([^)]*\))?!?:`)
var conventional = regexp.MustCompile(`(?i)^(?:feat|fix|perf)(?:\([^)]*\))?!?:\s*`)
var taskSuffix = regexp.MustCompile(`\s*[（(]ToDo[^）)]*[）)]\s*$`)

func Plain(s string) string {
	s = comments.ReplaceAllString(s, "")
	s = mdLink.ReplaceAllString(s, "$1")
	s = tags.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.ReplaceAll(s, "`", "")
	return strings.Join(strings.Fields(s), " ")
}
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func Summary(body, fallback string) string {
	if i := strings.Index(body, "<!-- CURSOR_SUMMARY -->"); i >= 0 {
		body = body[:i]
	}
	body = comments.ReplaceAllString(strings.ReplaceAll(body, "\r\n", "\n"), "")
	paragraph := []string{}
	fence := ""
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			mark := line[:3]
			if fence == "" {
				fence = mark
			} else if fence == mark {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if line == "" {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "#") || notProse.MatchString(line) {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		paragraph = append(paragraph, line)
	}
	s := Plain(strings.Join(paragraph, " "))
	if s == "" || strings.Contains(s, "ここに要約") {
		s = fallback
	}
	return shorten(s, 220)
}

func matches(patterns []string, s string) bool {
	for _, p := range patterns {
		if regexp.MustCompile(p).MatchString(s) {
			return true
		}
	}
	return false
}
func (c Config) eligible(r Repository, title, login string, labels []label) bool {
	title = taskSuffix.ReplaceAllString(Plain(title), "")
	if boring.MatchString(title) || matches(c.ExcludeTitles, title) || strings.EqualFold(login, "dependabot[bot]") || strings.EqualFold(login, "renovate[bot]") {
		return false
	}
	for _, l := range labels {
		switch strings.ToLower(l.Name) {
		case "activity:skip", "profile:skip", "dependencies", "internal":
			return false
		}
	}
	return matches(r.IncludeTitles, title)
}

func (c Config) entry(r Repository, kind string, number int64, title, body, link string, date time.Time, labels []label) (Entry, error) {
	title = shorten(Plain(taskSuffix.ReplaceAllString(title, "")), 160)
	title = conventional.ReplaceAllString(title, "")
	summary := Summary(body, title)
	if matches(c.ExcludeTitles, summary) {
		summary = title
	}
	tech := []string{}
	source := "labels"
	known := map[string]bool{}
	for _, t := range c.Technologies {
		known[t.ID] = true
	}
	for _, l := range labels {
		if strings.HasPrefix(l.Name, "tech:") {
			id := strings.TrimPrefix(l.Name, "tech:")
			if !known[id] {
				return Entry{}, fmt.Errorf("undefined activity technology %q", id)
			}
			tech = append(tech, id)
		}
	}
	if len(tech) == 0 {
		tech = append(tech, r.Technologies...)
		source = "repository"
	}
	sortStringsUnique := func(in []string) []string {
		out := []string{}
		seen := map[string]bool{}
		for _, v := range in {
			if !seen[v] {
				out = append(out, v)
				seen[v] = true
			}
		}
		return out
	}
	tech = sortStringsUnique(tech)
	return Entry{ID: fmt.Sprintf("github:%d:%s:%d", r.ID, kind, number), Kind: kind, Repository: r.Name, RepositoryID: r.ID, Number: number, URL: link, Date: date.UTC(), Title: title, Summary: summary, Technologies: tech, TechSource: source}, nil
}

func (c Config) Collect(ctx context.Context, client *Client) ([]Entry, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	// Verify every allowlisted repository before requesting any PR/release body.
	for _, r := range c.Repositories {
		var meta publicRepo
		if _, err := client.Call(ctx, "GET", "/repos/"+r.Name, nil, &meta); err != nil {
			return nil, err
		}
		if meta.ID != r.ID || meta.FullName != r.Name || meta.Private == nil || *meta.Private || meta.Visibility != "public" {
			return nil, errors.New("allowlisted repository is not the expected public resource")
		}
		client.RepositoryIDs[r.Name] = r.ID
	}
	entries := []Entry{}
	for _, r := range c.Repositories {
		var prs []pull
		if err := client.Pages(ctx, "/repos/"+r.Name+"/pulls?state=closed&sort=updated&direction=desc&per_page=100", &prs); err != nil {
			return nil, err
		}
		for _, p := range prs {
			if p.MergedAt == nil || !c.eligible(r, p.Title, p.User.Login, p.Labels) {
				continue
			}
			e, err := c.entry(r, "pull", p.Number, p.Title, p.Body, p.HTMLURL, *p.MergedAt, p.Labels)
			if err != nil {
				return nil, err
			}
			entries = append(entries, e)
		}
		var releases []release
		if err := client.Pages(ctx, "/repos/"+r.Name+"/releases?per_page=100", &releases); err != nil {
			return nil, err
		}
		for _, rel := range releases {
			if rel.Draft || rel.Prerelease || rel.Published == nil {
				continue
			}
			title := rel.Name
			if title == "" {
				title = rel.Tag
			}
			if matches(c.ExcludeTitles, Plain(title)) {
				continue
			}
			e, err := c.entry(r, "release", rel.ID, title, rel.Body, rel.URL, *rel.Published, nil)
			if err != nil {
				return nil, err
			}
			entries = append(entries, e)
		}
	}
	if err := c.ValidateEntries(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func ParseHours(s string) (time.Duration, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 168 {
		return 0, errors.New("review hours must be between 0 and 168")
	}
	return time.Duration(n) * time.Hour, nil
}
