package activity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

type syncFixture struct {
	t                                            *testing.T
	client                                       *Client
	pr                                           PR
	data                                         Data
	readme                                       string
	excluded                                     []Exclusion
	fresh                                        bool
	failFetch                                    bool
	holdOnRead                                   bool
	writeRace                                    bool
	commits, created, merges, deletes, mutations int
	writtenData                                  Data
	writtenReadme                                string
}

func newSyncFixture(t *testing.T, existing bool) *syncFixture {
	t.Helper()
	f := &syncFixture{t: t}
	now := time.Now().UTC().Truncate(time.Second)
	f.data, f.readme, _ = Build(fixtureConfig(), Data{}, nil, "# handwritten 日本語 English Русский\n![stats](assets/private-contributions.svg)\n"+Start+"\n\n"+End+"\n## Operating Principles\noriginal", nil, now)
	if existing {
		f.pr = PR{Number: 31, URL: "https://github.com/" + Profile + "/pull/31", Created: now.Add(-8 * 24 * time.Hour), State: "open"}
		f.pr.Head.Ref = ReviewBranch
		f.pr.Head.SHA = strings.Repeat("b", 40)
		f.pr.Head.Repo.FullName = Profile
		f.pr.Base.Ref = "main"
	}
	f.client = serveClient(t, f.serve)
	return f
}
func (f *syncFixture) serve(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if r.Method != "GET" {
		f.mutations++
	}
	switch {
	case p == profileAPI:
		sendJSON(w, map[string]any{"id": 1221617650, "full_name": Profile, "private": false, "visibility": "public"})
	case p == profileAPI+"/git/ref/heads/main":
		sendJSON(w, map[string]any{"object": map[string]string{"sha": strings.Repeat("a", 40)}})
	case p == profileAPI+"/pulls" && r.Method == "GET":
		if f.pr.Number > 0 {
			sendJSON(w, []PR{f.pr})
		} else {
			sendJSON(w, []PR{})
		}
	case p == profileAPI+"/pulls" && r.Method == "POST":
		f.created++
		f.pr = PR{Number: 31, URL: "https://github.com/" + Profile + "/pull/31", Created: time.Now().UTC(), State: "open"}
		f.pr.Head.Ref = ReviewBranch
		f.pr.Head.SHA = strings.Repeat("e", 40)
		f.pr.Head.Repo.FullName = Profile
		f.pr.Base.Ref = "main"
		sendJSON(w, f.pr)
	case p == profileAPI+"/pulls/31/files":
		sendJSON(w, []map[string]string{{"filename": "README.md", "status": "modified"}, {"filename": DataPath, "status": "modified"}})
	case p == profileAPI+"/pulls/31":
		if f.holdOnRead {
			f.pr.Labels = []label{{Name: "hold"}}
		}
		sendJSON(w, f.pr)
	case strings.HasPrefix(p, profileAPI+"/compare/"):
		sendJSON(w, map[string]int{"behind_by": 0})
	case strings.HasPrefix(p, profileAPI+"/contents/"):
		name := strings.TrimPrefix(p, profileAPI+"/contents/")
		var b []byte
		switch name {
		case DataPath:
			b = Encode(f.data)
		case "README.md":
			b = []byte(f.readme)
		case ExclusionsPath:
			b = Encode(f.excluded)
			if f.excluded == nil {
				b = []byte("[]")
			}
		default:
			f.t.Errorf("unexpected file %q", name)
			w.WriteHeader(404)
			return
		}
		sendJSON(w, map[string]any{"type": "file", "encoding": "base64", "size": len(b), "content": base64.StdEncoding.EncodeToString(b)})
	case p == "/repos/AutoDevJapan/demo":
		sendJSON(w, map[string]any{"id": 101, "full_name": "AutoDevJapan/demo", "private": false, "visibility": "public"})
	case p == "/repos/AutoDevJapan/demo/pulls":
		if f.failFetch {
			w.WriteHeader(500)
			return
		}
		if !f.fresh {
			sendJSON(w, []any{})
			return
		}
		sendJSON(w, []map[string]any{{"number": 1, "title": "feat: original API title", "html_url": "https://github.com/AutoDevJapan/demo/pull/1", "merged_at": "2026-10-20T12:00:00Z", "body": "Exported files are retained."}, {"number": 2, "title": "fix: another public improvement", "html_url": "https://github.com/AutoDevJapan/demo/pull/2", "merged_at": "2026-10-20T11:00:00Z"}})
	case p == "/repos/AutoDevJapan/demo/releases":
		sendJSON(w, []any{})
	case strings.HasPrefix(p, profileAPI+"/git/commits/") && r.Method == "GET":
		sendJSON(w, map[string]any{"tree": map[string]string{"sha": strings.Repeat("c", 40)}})
	case p == profileAPI+"/git/trees":
		var request struct {
			Tree []struct{ Path, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			f.t.Error(err)
		}
		if len(request.Tree) != 2 {
			f.t.Errorf("unexpected generated paths: %#v", request)
		}
		for _, file := range request.Tree {
			switch file.Path {
			case DataPath:
				if err := Decode([]byte(file.Content), &f.writtenData); err != nil {
					f.t.Error(err)
				}
			case "README.md":
				f.writtenReadme = file.Content
			default:
				f.t.Errorf("out-of-scope write %s", file.Path)
			}
		}
		sendJSON(w, map[string]string{"sha": strings.Repeat("d", 40)})
	case p == profileAPI+"/git/commits" && r.Method == "POST":
		f.commits++
		sendJSON(w, map[string]string{"sha": strings.Repeat("e", 40)})
	case p == profileAPI+"/git/refs":
		w.WriteHeader(201)
	case p == profileAPI+"/git/refs/heads/"+ReviewBranch && r.Method == "PATCH":
		var update struct {
			Force bool
			SHA   string
		}
		_ = json.NewDecoder(r.Body).Decode(&update)
		if update.Force {
			f.t.Error("force update is forbidden")
		}
		if f.writeRace {
			w.WriteHeader(409)
			return
		}
		f.pr.Head.SHA = update.SHA
		f.data = f.writtenData
		f.readme = f.writtenReadme
		w.WriteHeader(200)
	case strings.Contains(p, "/check-runs"):
		sha := strings.Split(strings.TrimPrefix(p, profileAPI+"/commits/"), "/")[0]
		runs := []map[string]any{}
		for _, name := range requiredChecks {
			runs = append(runs, map[string]any{"name": name, "status": "completed", "conclusion": "success", "head_sha": sha, "app": map[string]int{"id": 15368}})
		}
		sendJSON(w, map[string]any{"total_count": len(runs), "check_runs": runs})
	case p == profileAPI+"/pulls/31/merge":
		var request struct{ SHA string }
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.SHA != f.pr.Head.SHA {
			f.t.Error("merge did not pin validated SHA")
		}
		f.merges++
		sendJSON(w, map[string]any{"merged": true, "sha": strings.Repeat("f", 40)})
	case p == profileAPI+"/git/ref/heads/"+ReviewBranch:
		sendJSON(w, map[string]any{"object": map[string]string{"sha": f.pr.Head.SHA}})
	case p == profileAPI+"/git/refs/heads/"+ReviewBranch && r.Method == "DELETE":
		f.deletes++
		w.WriteHeader(204)
	default:
		f.t.Errorf("unexpected GitHub request %s %s", r.Method, r.URL)
		w.WriteHeader(500)
	}
}

func TestSyncNoOpDoesNotCreateCommitBranchOrPR(t *testing.T) {
	f := newSyncFixture(t, false)
	result, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now().UTC(), 168*time.Hour)
	if err != nil || result.Changed || result.PR != 0 || f.mutations != 0 {
		t.Fatalf("no-op wrote GitHub: %#v mutations=%d err=%v", result, f.mutations, err)
	}
}

func TestSyncCreatesOneReviewAndPreservesEditsAndExclusion(t *testing.T) {
	f := newSyncFixture(t, true)
	f.fresh = true
	f.pr.Labels = []label{{Name: "hold"}}
	e := Entry{ID: "github:101:pull:1", Kind: "pull", Repository: "AutoDevJapan/demo", RepositoryID: 101, Number: 1, URL: "https://github.com/AutoDevJapan/demo/pull/1", Date: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), Title: "original", Summary: "original", Technologies: []string{"go"}, TechSource: "repository"}
	f.data, f.readme, _ = Build(fixtureConfig(), f.data, nil, f.readme, []Entry{e}, time.Now())
	f.data.Entries[0].Title = "編集した公開説明"
	f.data.Entries[0].Technologies = []string{"rust"}
	f.excluded = []Exclusion{{ID: "github:101:pull:2", Reason: "採用しない公開記録"}}
	result, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Published || f.created != 0 || f.commits != 1 || len(f.writtenData.Entries) != 1 || f.writtenData.Entries[0].Title != "編集した公開説明" || f.writtenData.Entries[0].Technologies[0] != "rust" || !strings.HasPrefix(f.writtenReadme, "# handwritten 日本語 English Русский\n![stats](assets/private-contributions.svg)") {
		t.Fatalf("edits, exclusion or hold lost %#v %#v", result, f.writtenData)
	}
	_, err = Sync(context.Background(), fixtureConfig(), f.client, time.Now().Add(time.Hour), 0)
	if err != nil || f.commits != 1 || f.created != 0 || f.merges != 0 {
		t.Fatalf("second refresh was not no-op: commits=%d err=%v", f.commits, err)
	}
}

func TestSyncCreatedAtDeadlinePublishesValidatedSHA(t *testing.T) {
	f := newSyncFixture(t, true)
	result, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 168*time.Hour)
	if err != nil || !result.Published || f.merges != 1 || f.deletes != 1 {
		t.Fatalf("deadline publication failed %#v %v", result, err)
	}
}

func TestSyncRechecksHoldAfterFormalChecks(t *testing.T) {
	f := newSyncFixture(t, true)
	f.holdOnRead = true
	result, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
	if err != nil || result.Published || f.merges != 0 {
		t.Fatalf("late hold ignored %#v %v", result, err)
	}
}

func TestSyncFailureKeepsRemoteFilesAndNeverPublishes(t *testing.T) {
	for _, which := range []string{"partial-fetch", "concurrent-edit"} {
		t.Run(which, func(t *testing.T) {
			f := newSyncFixture(t, true)
			f.fresh = true
			f.failFetch = which == "partial-fetch"
			f.writeRace = which == "concurrent-edit"
			before := string(Encode(f.data))
			readme := f.readme
			_, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
			if err == nil || f.merges != 0 || string(Encode(f.data)) != before || f.readme != readme {
				t.Fatalf("failure changed published/edited data: %s err=%v", which, err)
			}
		})
	}
}

func TestChecksCannotUseAnotherSHAOrAnotherApp(t *testing.T) {
	for _, which := range []string{"sha", "app"} {
		t.Run(which, func(t *testing.T) {
			api := serveClient(t, func(w http.ResponseWriter, r *http.Request) {
				runs := []map[string]any{}
				for _, name := range requiredChecks {
					sha := strings.Repeat("a", 40)
					app := int64(15368)
					if which == "sha" {
						sha = strings.Repeat("b", 40)
					} else {
						app = 999
					}
					runs = append(runs, map[string]any{"name": name, "head_sha": sha, "app": map[string]int64{"id": app}, "status": "completed", "conclusion": "success"})
				}
				sendJSON(w, map[string]any{"total_count": 3, "check_runs": runs})
			})
			ok, _, err := api.checks(context.Background(), strings.Repeat("a", 40))
			if err != nil || ok {
				t.Fatalf("wrong check provenance accepted %v %v", ok, err)
			}
		})
	}
}

func ExampleReady() {
	now := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	fmt.Println(Ready(now.Add(-168*time.Hour), nil, false, now, 168*time.Hour))
	fmt.Println(Ready(now.Add(-200*time.Hour), []string{"hold"}, false, now, 0))
	// Output:
	// true
	// false
}
