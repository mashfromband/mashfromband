package activity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fixtureConfig() Config {
	return Config{Repositories: []Repository{{Name: "AutoDevJapan/demo", ID: 101, Technologies: []string{"go"}, IncludeTitles: []string{`(?i)^(feat|fix|perf)(\([^)]*\))?:`}}}, Technologies: []Technology{{ID: "go", Name: "Go"}, {ID: "rust", Name: "Rust"}}, ExcludeTitles: []string{`(?i)internal|customer|acceptance|顧客|運用|CI`}, Limit: 6}
}
func serveClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	c := NewClient("")
	c.Base = s.URL
	return c
}
func sendJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestCollectUsefulBotPRAndReleaseWithPagination(t *testing.T) {
	cfg := fixtureConfig()
	var api *Client
	pages := 0
	api = serveClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/AutoDevJapan/demo":
			sendJSON(w, map[string]any{"id": 101, "full_name": "AutoDevJapan/demo", "private": false, "visibility": "public"})
		case "/repos/AutoDevJapan/demo/pulls", "/repositories/101/pulls":
			pages++
			if r.URL.Query().Get("page") == "2" {
				sendJSON(w, []any{map[string]any{"number": 8, "title": "fix: repair export", "html_url": "https://github.com/AutoDevJapan/demo/pull/8", "merged_at": "2026-10-19T12:00:00Z", "body": "## Changes\nAn export now preserves files.", "user": map[string]string{"login": "ai-bot[bot]"}, "labels": []any{map[string]string{"name": "tech:rust"}}}})
				return
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s/repositories/101/pulls?page=2&per_page=100>; rel="next"`, api.Base))
			sendJSON(w, []any{map[string]any{"number": 7, "title": "feat: shared items", "html_url": "https://github.com/AutoDevJapan/demo/pull/7", "merged_at": "2026-10-20T12:00:00Z", "body": "<!-- template -->\n# Change\nShared items work offline.\n\n## Tests\nInternal instructions are not copied.", "user": map[string]string{"login": "github-actions[bot]"}}, map[string]any{"number": 9, "title": "ci: build cache", "merged_at": "2026-10-20T12:00:00Z"}, map[string]any{"number": 10, "title": "feat: internal customer work", "merged_at": "2026-10-20T12:00:00Z"}})
		case "/repos/AutoDevJapan/demo/releases":
			sendJSON(w, []any{map[string]any{"id": 7, "name": "v1.0", "tag_name": "v1.0", "html_url": "https://github.com/AutoDevJapan/demo/releases/tag/v1.0", "published_at": "2026-10-20T13:00:00Z", "draft": false, "prerelease": false}, map[string]any{"id": 8, "name": "draft", "draft": true}})
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(500)
		}
	})
	entries, err := cfg.Collect(context.Background(), api)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || pages != 2 {
		t.Fatalf("incomplete collection: %d, pages %d", len(entries), pages)
	}
	if entries[0].Summary != "Shared items work offline." || entries[0].Technologies[0] != "go" || entries[1].Technologies[0] != "rust" || entries[0].ID == entries[2].ID {
		t.Fatalf("wrong source/summary/identity: %#v", entries)
	}
}

func TestRejectPrivateBeforeAnyDetailFetch(t *testing.T) {
	for _, metadata := range []string{`{"id":101,"full_name":"AutoDevJapan/demo","private":true,"visibility":"private"}`, `{"id":999,"full_name":"AutoDevJapan/demo","private":false,"visibility":"public"}`, `{"id":101,"full_name":"AutoDevJapan/demo","visibility":"public"}`, `null`} {
		t.Run(metadata, func(t *testing.T) {
			details := 0
			api := serveClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/AutoDevJapan/demo" {
					details++
					w.WriteHeader(500)
					return
				}
				fmt.Fprint(w, metadata)
			})
			_, err := fixtureConfig().Collect(context.Background(), api)
			if err == nil || details != 0 {
				t.Fatalf("metadata failure accepted; error=%v details=%d", err, details)
			}
		})
	}
}

func TestPartialPaginationFailsInsteadOfReturningEmptyOrPartialData(t *testing.T) {
	var api *Client
	api = serveClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/AutoDevJapan/demo" {
			sendJSON(w, map[string]any{"id": 101, "full_name": "AutoDevJapan/demo", "private": false, "visibility": "public"})
			return
		}
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=2>; rel="next"`, api.Base, r.URL.Path))
		sendJSON(w, []any{})
	})
	got, err := fixtureConfig().Collect(context.Background(), api)
	if err == nil || got != nil {
		t.Fatalf("partial data accepted: %#v %v", got, err)
	}
}

func TestPublicationFilter(t *testing.T) {
	c := fixtureConfig()
	r := c.Repositories[0]
	for _, tt := range []struct {
		title, user string
		labels      []label
		want        bool
	}{
		{"feat: offline files", "github-actions[bot]", nil, true},
		{"feat: offline files", "dependabot[bot]", nil, false},
		{"chore: activity update", "ai-bot[bot]", nil, false},
		{"ci: checks", "human", nil, false},
		{"fix(acceptance): logs", "ai", nil, false},
		{"feat: customer support note", "ai", nil, false},
		{"feat: offline files", "ai", []label{{Name: "activity:skip"}}, false},
		{"docs: README refresh", "ai", nil, false},
	} {
		if got := c.eligible(r, tt.title, tt.user, tt.labels); got != tt.want {
			t.Errorf("%q: %v", tt.title, got)
		}
	}
}

func TestSummaryIsInertPlainText(t *testing.T) {
	body := "<!-- hidden -->\n```sh\nrm -rf /\n```\n## Changes\n- checklist\n\nFiles [stay](https://example.com) **available** <script>doNothing()</script>.\n\n## Internal\ncustomer secret"
	got := Summary(body, "fallback")
	if strings.ContainsAny(got, "<>`") || strings.Contains(got, "rm -rf") || strings.Contains(got, "customer") || strings.Contains(got, "https://") {
		t.Fatalf("unsafe summary %q", got)
	}
	if !strings.Contains(got, "Files stay") {
		t.Fatalf("first prose missing %q", got)
	}
}

func TestMarkdownEscapesTableAndLinkSyntax(t *testing.T) {
	if got, want := markdown("a|b [link] *x*"), "a\\|b \\[link\\] \\*x\\*"; got != want {
		t.Fatalf("Markdown syntax was not escaped: %q want %q", got, want)
	}
}

func TestPaginationCannotChangeVerifiedRepository(t *testing.T) {
	api := NewClient("")
	api.RepositoryIDs["AutoDevJapan/demo"] = 101
	if !api.samePagePath("/repositories/101/pulls", "/repos/AutoDevJapan/demo/pulls") || api.samePagePath("/repositories/999/pulls", "/repos/AutoDevJapan/demo/pulls") {
		t.Fatal("repository-bound pagination failed")
	}
}

func TestTechnologyValidationAndDeduplication(t *testing.T) {
	c := fixtureConfig()
	c.Technologies[0].Parent = "rust"
	c.Technologies[1].Parent = "go"
	if c.Validate() == nil {
		t.Fatal("cycle accepted")
	}
	c.Technologies[1].Parent = "missing"
	if c.Validate() == nil {
		t.Fatal("unknown parent accepted")
	}
	c.Technologies[1].Parent = ""
	e := Entry{ID: "github:101:pull:1", Kind: "pull", Repository: "AutoDevJapan/demo", RepositoryID: 101, Number: 1, URL: "https://github.com/AutoDevJapan/demo/pull/1", Date: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), Title: "files", Technologies: []string{"go", "rust"}, TechSource: "edited"}
	text, err := c.Render(Data{Version: 1, Updated: e.Date, Entries: []Entry{e}})
	if err != nil {
		t.Fatal(err)
	}
	line := "| Rust | [files](https://github.com/AutoDevJapan/demo/pull/1) |"
	if strings.Count(text, line) != 1 {
		t.Fatalf("duplicate parent evidence: %s", text)
	}
	e.Technologies = []string{"unknown"}
	if c.ValidateEntries([]Entry{e}) == nil {
		t.Fatal("unknown activity technology accepted")
	}
}

func TestBuildKeepsManualJSONAndStopsOnManualGeneratedMarkdown(t *testing.T) {
	c := fixtureConfig()
	now := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	e := Entry{ID: "github:101:pull:1", Kind: "pull", Repository: "AutoDevJapan/demo", RepositoryID: 101, Number: 1, URL: "https://github.com/AutoDevJapan/demo/pull/1", Date: now, Title: "original", Technologies: []string{"go"}, TechSource: "repository"}
	readme := "# hand written\n" + Start + "\n\n" + End + "\n日本語 English Русский\n![stats](assets/private-contributions.svg)"
	d, readme, err := Build(c, Data{}, nil, readme, []Entry{e}, now)
	if err != nil {
		t.Fatal(err)
	}
	d.Entries[0].Title = "手で直した題名"
	d2, r2, err := Build(c, d, nil, readme, []Entry{e}, now.Add(time.Hour))
	if err != nil || d2.Entries[0].Title != "手で直した題名" || !strings.Contains(r2, "手で直した題名") {
		t.Fatalf("manual JSON lost %v", err)
	}
	if _, _, err := Build(c, d2, nil, strings.Replace(r2, "手で直した題名", "READMEを直接編集", 1), []Entry{e}, now); err == nil {
		t.Fatal("direct README edit overwritten")
	}
	if err := Check(c, d2, nil, r2); err != nil {
		t.Fatal(err)
	}
}
