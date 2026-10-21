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
	stalePRReads                                            int
	editAfterRefWrite                                       bool
	mainReads, mainChangeAt                                 int
	mergeConflict                                           bool
	behind                                                  bool
	baseMerges                                              int
	commitParent                                            string
	t                                                       *testing.T
	client                                                  *Client
	pr                                                      PR
	data                                                    Data
	readme                                                  string
	excluded                                                []Exclusion
	fresh                                                   bool
	failFetch                                               bool
	holdOnRead                                              bool
	writeRace                                               bool
	commits, created, merges, deletes, mutations            int
	writtenData                                             Data
	writtenReadme                                           string
	branchExists, closedReview, closedMerged, pendingChecks bool
	branchSHA, extraPath                                    string
	dispatches                                              map[string]int
}

func newSyncFixture(t *testing.T, existing bool) *syncFixture {
	t.Helper()
	f := &syncFixture{t: t, dispatches: map[string]int{}}
	now := time.Now().UTC().Truncate(time.Second)
	f.data, f.readme, _ = Build(fixtureConfig(), Data{}, nil, "# handwritten 日本語 English Русский\n![stats](assets/private-contributions.svg)\n"+Start+"\n\n"+End+"\n## Operating Principles\noriginal", nil, now)
	if existing {
		f.pr = PR{Number: 31, URL: "https://github.com/" + Profile + "/pull/31", Created: now.Add(-8 * 24 * time.Hour), State: "open"}
		f.pr.Head.Ref = ReviewBranch
		f.pr.Head.SHA = strings.Repeat("b", 40)
		f.pr.Head.Repo.FullName = Profile
		f.pr.Base.Ref = "main"
		f.branchExists = true
		f.branchSHA = f.pr.Head.SHA
	}
	f.client = serveClient(t, f.serve)
	f.client.TrustedMain = strings.Repeat("a", 40)
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
		f.mainReads++
		sha := strings.Repeat("a", 40)
		if f.mainChangeAt > 0 && f.mainReads >= f.mainChangeAt {
			sha = strings.Repeat("f", 40)
		}
		sendJSON(w, map[string]any{"object": map[string]string{"sha": sha}})
	case p == profileAPI+"/pulls" && r.Method == "GET":
		if r.URL.Query().Get("state") == "closed" {
			if f.closedReview {
				closed := f.pr
				closed.Number = 30
				closed.State = "closed"
				closed.Head.SHA = strings.Repeat("b", 40)
				closed.Head.Ref = ReviewBranch
				closed.Head.Repo.FullName = Profile
				closed.Base.Ref = "main"
				if f.closedMerged {
					date := time.Now().UTC()
					closed.MergedAt = &date
				}
				sendJSON(w, []PR{closed})
			} else {
				sendJSON(w, []PR{})
			}
			return
		}
		if f.pr.Number > 0 {
			sendJSON(w, []PR{f.pr})
		} else {
			sendJSON(w, []PR{})
		}
	case p == profileAPI+"/pulls" && r.Method == "POST":
		f.created++
		f.pr = PR{Number: 31, URL: "https://github.com/" + Profile + "/pull/31", Created: time.Now().UTC(), State: "open"}
		f.pr.Head.Ref = ReviewBranch
		f.pr.Head.SHA = f.branchSHA
		f.pr.Head.Repo.FullName = Profile
		f.pr.Base.Ref = "main"
		sendJSON(w, f.pr)
	case p == profileAPI+"/pulls/31/files":
		sendJSON(w, []map[string]string{{"filename": "README.md", "status": "modified"}, {"filename": DataPath, "status": "modified"}})
	case p == profileAPI+"/pulls/31":
		if f.stalePRReads > 0 {
			f.stalePRReads--
			stale := f.pr
			stale.Head.SHA = strings.Repeat("b", 40)
			sendJSON(w, stale)
			return
		}
		if f.holdOnRead {
			f.pr.Labels = []label{{Name: "hold"}}
		}
		sendJSON(w, f.pr)
	case strings.HasPrefix(p, profileAPI+"/compare/"):
		files := []map[string]string{}
		if f.branchExists && f.pr.Number == 0 {
			files = append(files, map[string]string{"filename": DataPath, "status": "modified"})
		}
		if f.extraPath != "" {
			files = append(files, map[string]string{"filename": f.extraPath, "status": "modified"})
		}
		behind := 0
		if f.behind {
			behind = 1
		}
		sendJSON(w, map[string]any{"behind_by": behind, "files": files})
	case p == profileAPI+"/merges":
		if f.mergeConflict {
			w.WriteHeader(409)
			return
		}
		var request map[string]string
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request["base"] != ReviewBranch || request["head"] != strings.Repeat("a", 40) {
			f.t.Error("orphan base merge was not pinned")
		}
		f.baseMerges++
		f.behind = false
		f.branchSHA = strings.Repeat("d", 40)
		w.WriteHeader(201)
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
		var request struct{ Parents []string }
		_ = json.NewDecoder(r.Body).Decode(&request)
		if len(request.Parents) == 1 {
			f.commitParent = request.Parents[0]
		} else {
			f.t.Error("expected one pinned commit parent")
		}
		f.commits++
		sendJSON(w, map[string]string{"sha": strings.Repeat("e", 40)})
	case p == profileAPI+"/git/refs":
		if f.branchExists {
			w.WriteHeader(422)
			return
		}
		var request struct{ SHA string }
		_ = json.NewDecoder(r.Body).Decode(&request)
		f.branchExists = true
		f.branchSHA = request.SHA
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
		f.branchSHA = update.SHA
		f.data = f.writtenData
		f.readme = f.writtenReadme
		if f.editAfterRefWrite {
			f.branchSHA = strings.Repeat("d", 40)
			f.pr.Head.SHA = f.branchSHA
			f.data.Entries[0].Title = "late editorial change"
		}
		w.WriteHeader(200)
	case strings.Contains(p, "/check-runs"):
		sha := strings.Split(strings.TrimPrefix(p, profileAPI+"/commits/"), "/")[0]
		runs := []map[string]any{}
		for _, name := range requiredChecks {
			status, conclusion := "completed", "success"
			if f.pendingChecks && len(f.dispatches) < 2 {
				status = "queued"
				conclusion = ""
			}
			runs = append(runs, map[string]any{"name": name, "status": status, "conclusion": conclusion, "head_sha": sha, "app": map[string]int{"id": 15368}})
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
		if !f.branchExists {
			w.WriteHeader(404)
			return
		}
		sendJSON(w, map[string]any{"object": map[string]string{"sha": f.branchSHA}})
	case strings.Contains(p, "/actions/workflows/") && strings.HasSuffix(p, "/dispatches"):
		name := strings.TrimSuffix(strings.TrimPrefix(p, profileAPI+"/actions/workflows/"), "/dispatches")
		f.dispatches[name]++
		w.WriteHeader(204)
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

func TestOrphanReviewBranchRecoversWithoutResettingEdits(t *testing.T) {
	f := newSyncFixture(t, false)
	f.fresh = true
	f.branchExists = true
	f.branchSHA = strings.Repeat("b", 40)
	e := Entry{ID: "github:101:pull:1", Kind: "pull", Repository: "AutoDevJapan/demo", RepositoryID: 101, Number: 1, URL: "https://github.com/AutoDevJapan/demo/pull/1", Date: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), Title: "original", Technologies: []string{"go"}, TechSource: "repository"}
	f.data, f.readme, _ = Build(fixtureConfig(), f.data, nil, f.readme, []Entry{e}, time.Now())
	f.data.Entries[0].Title = "中断したブランチの手編集"
	f.excluded = []Exclusion{{ID: "github:101:pull:2", Reason: "掲載しない"}}
	result, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 168*time.Hour)
	if err != nil || result.PR != 31 || f.created != 1 || f.writtenData.Entries[0].Title != "中断したブランチの手編集" {
		t.Fatalf("orphan branch was not recovered: %#v %v", result, err)
	}
}

func TestOrphanCompleteDataStillGetsOneReviewPR(t *testing.T) {
	f := newSyncFixture(t, false)
	f.branchExists = true
	f.branchSHA = strings.Repeat("b", 40)
	result, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 168*time.Hour)
	if err != nil || result.PR != 31 || f.created != 1 || f.commits != 0 {
		t.Fatalf("already-written orphan was abandoned: %#v %v", result, err)
	}
}

func TestOrphanOutOfScopeChangesArePreservedAndRefused(t *testing.T) {
	f := newSyncFixture(t, false)
	f.branchExists = true
	f.branchSHA = strings.Repeat("b", 40)
	f.fresh = true
	f.extraPath = "cmd/profile-activity/main.go"
	_, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
	if err == nil || f.commits != 0 || f.created != 0 || f.merges != 0 {
		t.Fatalf("out-of-scope orphan was changed: commits=%d err=%v", f.commits, err)
	}
}

func TestClosedUnmergedReviewRemainsStopped(t *testing.T) {
	f := newSyncFixture(t, false)
	f.branchExists = true
	f.branchSHA = strings.Repeat("b", 40)
	f.closedReview = true
	f.fresh = true
	result, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
	if err != nil || result.Outcome != "review-closed" || f.mutations != 0 {
		t.Fatalf("closed editorial decision was overwritten: %#v %v", result, err)
	}
}

func TestMergedBranchNoOpDoesNotCreateAnotherPRorCommit(t *testing.T) {
	f := newSyncFixture(t, false)
	f.branchExists = true
	f.branchSHA = strings.Repeat("b", 40)
	f.closedReview = true
	f.closedMerged = true
	f.behind = true
	result, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
	if err != nil || result.PR != 0 || f.mutations != 0 {
		t.Fatalf("retained merged branch was not no-op: %#v %v", result, err)
	}
}

func TestNextReviewCycleMergesMainWithoutResettingBranch(t *testing.T) {
	f := newSyncFixture(t, false)
	f.branchExists = true
	f.branchSHA = strings.Repeat("b", 40)
	f.closedReview = true
	f.closedMerged = true
	f.behind = true
	f.fresh = true
	result, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 168*time.Hour)
	if err != nil || result.PR != 31 || f.baseMerges != 1 || f.commitParent != strings.Repeat("d", 40) || f.deletes != 0 {
		t.Fatalf("next review cycle lost its branch: %#v %v", result, err)
	}
}

func TestMainAdvanceCannotPublishAnOlderGeneration(t *testing.T) {
	f := newSyncFixture(t, true)
	f.mainChangeAt = 3
	result, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
	if err != nil || result.Outcome != "base-changed-retry" || result.Published || f.merges != 0 {
		t.Fatalf("advanced main was ignored: %#v %v", result, err)
	}
}

func TestPRHeadPropagationWaitDoesNotRewriteOrLoseEdits(t *testing.T) {
	f := newSyncFixture(t, true)
	f.fresh = true
	f.pr.Labels = []label{{Name: "hold"}}
	f.stalePRReads = 2
	f.client.pause = func(context.Context, time.Duration) error { return nil }
	result, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
	if err != nil || result.PR != 31 || result.Head != strings.Repeat("e", 40) || f.commits != 1 || f.created != 0 || f.merges != 0 {
		t.Fatalf("PR view lag was not handled: %#v %v", result, err)
	}
}

func TestConcurrentEditDuringPRHeadPropagationIsPreserved(t *testing.T) {
	f := newSyncFixture(t, true)
	f.fresh = true
	f.pr.Labels = []label{{Name: "hold"}}
	f.stalePRReads = 2
	f.editAfterRefWrite = true
	f.client.pause = func(context.Context, time.Duration) error { return nil }
	_, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
	if err == nil || f.branchSHA != strings.Repeat("d", 40) || f.data.Entries[0].Title != "late editorial change" || f.commits != 1 || f.merges != 0 {
		t.Fatalf("concurrent editorial change lost: %v", err)
	}
}

func TestPRHeadPropagationIsBoundedAndReportsAcceptedWrite(t *testing.T) {
	f := newSyncFixture(t, true)
	f.fresh = true
	f.pr.Labels = []label{{Name: "hold"}}
	f.stalePRReads = 100
	f.client.pause = func(context.Context, time.Duration) error { return nil }
	result, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
	if err == nil || !result.Changed || result.Head != strings.Repeat("e", 40) || result.HeadWaits != 10 || f.commits != 1 || f.merges != 0 {
		t.Fatalf("unbounded or misreported PR propagation: %#v %v", result, err)
	}
	f.stalePRReads = 0
	again, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
	if err != nil || again.Changed || f.commits != 1 {
		t.Fatalf("recovery duplicated a write: %#v %v", again, err)
	}
}

func TestMainAdvanceBeforeWriteKeepsCandidateUnpublished(t *testing.T) {
	f := newSyncFixture(t, false)
	f.mainChangeAt = 2
	f.fresh = true
	result, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
	if err != nil || result.Outcome != "base-changed-retry" || f.commits != 0 || f.created != 0 {
		t.Fatalf("stale generation was written: %#v %v", result, err)
	}
}

func TestStaleTrustedCheckoutDoesNotCollectOrWrite(t *testing.T) {
	f := newSyncFixture(t, false)
	f.fresh = true
	f.client.TrustedMain = strings.Repeat("d", 40)
	result, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
	if err != nil || result.Outcome != "trusted-checkout-stale" || f.mutations != 0 {
		t.Fatalf("stale checkout was accepted: %#v %v", result, err)
	}
}

func TestOrphanMainMergeConflictPreservesBranchAndData(t *testing.T) {
	f := newSyncFixture(t, false)
	f.branchExists = true
	f.branchSHA = strings.Repeat("b", 40)
	f.closedReview = true
	f.closedMerged = true
	f.behind = true
	f.fresh = true
	f.mergeConflict = true
	before := string(Encode(f.data))
	readme := f.readme
	_, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
	if err == nil || f.commits != 0 || f.created != 0 || f.branchSHA != strings.Repeat("b", 40) || string(Encode(f.data)) != before || f.readme != readme {
		t.Fatalf("merge conflict changed editorial data: %v", err)
	}
}

func TestFormalChecksDispatchAndWaitForSameHead(t *testing.T) {
	f := newSyncFixture(t, true)
	f.pendingChecks = true
	f.client.pause = func(context.Context, time.Duration) error { return nil }
	result, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
	if err != nil || !result.Published || f.dispatches["tests.yml"] != 1 || f.dispatches["profile-readme.yml"] != 1 {
		t.Fatalf("dispatch path failed: %#v dispatches=%v err=%v", result, f.dispatches, err)
	}
}

func TestHeadChangeDuringDispatchedChecksCannotPublish(t *testing.T) {
	f := newSyncFixture(t, true)
	f.pendingChecks = true
	f.client.pause = func(context.Context, time.Duration) error { f.pr.Head.SHA = strings.Repeat("d", 40); return nil }
	_, err := Sync(context.Background(), fixtureConfig(), f.client, time.Now(), 0)
	if err == nil || f.merges != 0 {
		t.Fatalf("changed validation head published: %v", err)
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
	if err != nil || !result.Published || f.merges != 1 || f.deletes != 0 {
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
