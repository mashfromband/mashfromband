package activity

import (
	"fmt"
	"html"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

const Start = "<!-- PUBLIC-ACTIVITY:START -->"
const End = "<!-- PUBLIC-ACTIVITY:END -->"

type Technology struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty"`
}
type Repository struct {
	Name          string   `json:"name"`
	ID            int64    `json:"id"`
	Technologies  []string `json:"technologies"`
	IncludeTitles []string `json:"include_titles"`
}
type Config struct {
	Repositories  []Repository `json:"repositories"`
	Technologies  []Technology `json:"technologies"`
	ExcludeTitles []string     `json:"exclude_titles"`
	Limit         int          `json:"readme_limit"`
}
type Entry struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	Repository   string    `json:"repository"`
	RepositoryID int64     `json:"repository_id"`
	Number       int64     `json:"number"`
	URL          string    `json:"url"`
	Date         time.Time `json:"date"`
	Title        string    `json:"title"`
	Summary      string    `json:"summary"`
	Technologies []string  `json:"technologies"`
	TechSource   string    `json:"tech_source"`
}
type Data struct {
	Version   int       `json:"version"`
	Updated   time.Time `json:"updated"`
	Entries   []Entry   `json:"entries"`
	ReadmeSHA string    `json:"readme_sha256,omitempty"`
}
type Exclusion struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

func Merge(old Data, incoming []Entry, excluded []Exclusion, now time.Time) (Data, error) {
	if old.Version != 0 && old.Version != 1 {
		return Data{}, fmt.Errorf("unsupported data version")
	}
	skip := map[string]bool{}
	for _, x := range excluded {
		if x.ID == "" || strings.TrimSpace(x.Reason) == "" || skip[x.ID] {
			return Data{}, fmt.Errorf("exclusion requires a unique id and reason")
		}
		skip[x.ID] = true
	}
	seen := map[string]bool{}
	entries := make([]Entry, 0, len(old.Entries)+len(incoming))
	for _, e := range old.Entries {
		if e.ID == "" || seen[e.ID] {
			return Data{}, fmt.Errorf("duplicate or missing activity id")
		}
		seen[e.ID] = true
		if !skip[e.ID] {
			entries = append(entries, e)
		}
	}
	generated := map[string]bool{}
	for _, e := range incoming {
		if e.ID == "" || generated[e.ID] {
			return Data{}, fmt.Errorf("duplicate or missing generated id")
		}
		generated[e.ID] = true
		if !seen[e.ID] && !skip[e.ID] {
			entries = append(entries, e)
			seen[e.ID] = true
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Date.Equal(entries[j].Date) {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].Date.After(entries[j].Date)
	})
	updated := old.Updated
	if old.Version != 1 || !reflect.DeepEqual(old.Entries, entries) {
		updated = now.UTC()
	}
	return Data{Version: 1, Updated: updated, Entries: entries, ReadmeSHA: old.ReadmeSHA}, nil
}

func ReplaceBlock(readme, content string) (string, error) {
	if strings.Count(readme, Start) != 1 || strings.Count(readme, End) != 1 {
		return "", fmt.Errorf("README needs exactly one activity marker pair")
	}
	a, b := strings.Index(readme, Start), strings.Index(readme, End)
	if b < a {
		return "", fmt.Errorf("README markers are reversed")
	}
	return readme[:a+len(Start)] + "\n" + strings.TrimSpace(content) + "\n" + readme[b:], nil
}

func Ready(created time.Time, labels []string, draft bool, now time.Time, delay time.Duration) bool {
	if draft || created.IsZero() || delay < 0 || created.After(now) {
		return false
	}
	for _, l := range labels {
		if strings.EqualFold(strings.TrimSpace(l), "hold") {
			return false
		}
	}
	return !now.Before(created.Add(delay))
}

var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var techID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)

func (c Config) Validate() error {
	if len(c.Repositories) == 0 || len(c.Repositories) > 10 || c.Limit < 1 || c.Limit > 20 {
		return fmt.Errorf("invalid repository list or README limit")
	}
	techs := map[string]Technology{}
	for _, t := range c.Technologies {
		if !techID.MatchString(t.ID) || strings.TrimSpace(t.Name) == "" || techs[t.ID].ID != "" {
			return fmt.Errorf("invalid or duplicate technology")
		}
		techs[t.ID] = t
	}
	state := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		t, ok := techs[id]
		if !ok {
			return fmt.Errorf("undefined technology %q", id)
		}
		if state[id] == 1 {
			return fmt.Errorf("technology parent cycle at %q", id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		if t.Parent != "" {
			if err := visit(t.Parent); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for id := range techs {
		if err := visit(id); err != nil {
			return err
		}
	}
	seenNames, seenIDs := map[string]bool{}, map[int64]bool{}
	for _, r := range c.Repositories {
		owner := strings.ToLower(strings.Split(r.Name, "/")[0])
		key := strings.ToLower(r.Name)
		if !repoName.MatchString(r.Name) || r.ID <= 0 || seenNames[key] || seenIDs[r.ID] || (owner != "autodevjapan" && owner != "bm1314" && owner != "mashfromband") || key == "mashfromband/mashfromband" || key == "autodevjapan/cc-pc-tools-rs" {
			return fmt.Errorf("invalid or out-of-scope public repository")
		}
		seenNames[key] = true
		seenIDs[r.ID] = true
		if len(r.Technologies) == 0 || len(r.IncludeTitles) == 0 {
			return fmt.Errorf("repository needs technology and publication rules")
		}
		for _, id := range r.Technologies {
			if _, ok := techs[id]; !ok {
				return fmt.Errorf("undefined repository technology %q", id)
			}
		}
		for _, p := range r.IncludeTitles {
			if _, err := regexp.Compile(p); err != nil {
				return fmt.Errorf("invalid inclusion pattern: %w", err)
			}
		}
	}
	for _, p := range c.ExcludeTitles {
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("invalid exclusion pattern: %w", err)
		}
	}
	return nil
}

func (c Config) ValidateEntries(entries []Entry) error {
	if err := c.Validate(); err != nil {
		return err
	}
	repos := map[int64]Repository{}
	techs := map[string]bool{}
	ids := map[string]bool{}
	for _, r := range c.Repositories {
		repos[r.ID] = r
	}
	for _, t := range c.Technologies {
		techs[t.ID] = true
	}
	for _, e := range entries {
		r, ok := repos[e.RepositoryID]
		if !ok || r.Name != e.Repository || e.Number <= 0 || e.ID != fmt.Sprintf("github:%d:%s:%d", r.ID, e.Kind, e.Number) || ids[e.ID] || e.Date.IsZero() || strings.TrimSpace(e.Title) == "" || len(e.Technologies) == 0 {
			return fmt.Errorf("invalid public activity identity")
		}
		ids[e.ID] = true
		u, err := url.Parse(e.URL)
		if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("invalid public activity URL")
		}
		if (e.Kind == "pull" && u.Path != "/"+r.Name+fmt.Sprintf("/pull/%d", e.Number)) || (e.Kind == "release" && !strings.HasPrefix(u.Path, "/"+r.Name+"/releases/tag/")) || (e.Kind != "pull" && e.Kind != "release") {
			return fmt.Errorf("activity URL and source differ")
		}
		for _, id := range e.Technologies {
			if !techs[id] {
				return fmt.Errorf("undefined activity technology %q", id)
			}
		}
		if e.TechSource != "labels" && e.TechSource != "repository" && e.TechSource != "edited" {
			return fmt.Errorf("invalid technology evidence source")
		}
	}
	return nil
}

func markdown(s string) string {
	s = html.EscapeString(Plain(s))
	return strings.NewReplacer("\\", "\\\\", "|", "\\|", "[", "\\[", "]", "\\]", "*", "\\*", "_", "\\_", "`", "\\`").Replace(s)
}

func (c Config) Render(d Data) (string, error) {
	if err := c.ValidateEntries(d.Entries); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("## Recent Public Work\n\n")
	fmt.Fprintf(&b, "公開製品の開発記録。更新: %s (UTC)。\n\n", d.Updated.UTC().Format("2006-01-02"))
	techs := map[string]Technology{}
	for _, t := range c.Technologies {
		techs[t.ID] = t
	}
	if len(d.Entries) == 0 {
		b.WriteString("対象となる公開活動はまだありません。\n")
	} else {
		b.WriteString("| Date | Public development | Technology |\n| --- | --- | --- |\n")
		for i, e := range d.Entries {
			if i >= c.Limit {
				break
			}
			names := []string{}
			for _, id := range e.Technologies {
				names = append(names, markdown(techs[id].Name))
			}
			desc := fmt.Sprintf("[%s](%s)", markdown(e.Title), e.URL)
			if e.Summary != "" && e.Summary != e.Title {
				desc += " — " + markdown(e.Summary)
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", e.Date.UTC().Format("2006-01-02"), desc, strings.Join(names, ", "))
		}
	}
	b.WriteString("\n## Technology Evidence\n\n")
	graph := map[string][]Entry{}
	for _, e := range d.Entries {
		seen := map[string]bool{}
		for _, id := range e.Technologies {
			for id != "" {
				if !seen[id] {
					graph[id] = append(graph[id], e)
					seen[id] = true
				}
				id = techs[id].Parent
			}
		}
	}
	if len(graph) == 0 {
		b.WriteString("公開活動に結び付く技術を、記録が増えた時点で表示します。\n")
	} else {
		b.WriteString("| Technology | Public evidence |\n| --- | --- |\n")
		keys := []string{}
		for id := range graph {
			keys = append(keys, id)
		}
		sort.Strings(keys)
		for _, id := range keys {
			links := []string{}
			for i, e := range graph[id] {
				if i == 3 {
					break
				}
				links = append(links, fmt.Sprintf("[%s](%s)", markdown(e.Title), e.URL))
			}
			fmt.Fprintf(&b, "| %s | %s |\n", markdown(techs[id].Name), strings.Join(links, " / "))
		}
	}
	b.WriteString("\n技術は変更の `tech:<id>` ラベル、未指定なら公開リポジトリの技術を参照しています。熟練度の評価ではありません。\n")
	return b.String(), nil
}
