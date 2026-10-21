package activity

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const Profile = "mashfromband/mashfromband"
const ReviewBranch = "automation/public-activity"
const profileAPI = "/repos/" + Profile

type PR struct {
	Number   int        `json:"number"`
	URL      string     `json:"html_url"`
	Created  time.Time  `json:"created_at"`
	Draft    bool       `json:"draft"`
	Labels   []label    `json:"labels"`
	Merged   bool       `json:"merged"`
	MergedAt *time.Time `json:"merged_at,omitempty"`
	State    string     `json:"state"`
	Head     struct {
		SHA, Ref string
		Repo     struct {
			FullName string `json:"full_name"`
		}
	} `json:"head"`
	Base struct{ Ref string } `json:"base"`
}

func (p PR) labelNames() []string {
	out := []string{}
	for _, l := range p.Labels {
		out = append(out, l.Name)
	}
	return out
}

type SyncResult struct {
	Outcome   string `json:"outcome"`
	PR        int    `json:"pr,omitempty"`
	URL       string `json:"url,omitempty"`
	Head      string `json:"head,omitempty"`
	Changed   bool   `json:"changed"`
	Published bool   `json:"published"`
	Entries   int    `json:"entries"`
}
type remoteInput struct {
	Readme   []byte
	Data     Data
	Excluded []Exclusion
}

func (api *Client) ReadFile(ctx context.Context, path, ref string) ([]byte, error) {
	var content struct {
		Type, Encoding, Content string
		Size                    int64
	}
	_, err := api.Call(ctx, "GET", profileAPI+"/contents/"+path+"?ref="+url.QueryEscape(ref), nil, &content)
	if err != nil {
		return nil, err
	}
	if content.Type != "file" || content.Encoding != "base64" || content.Size > 2*1024*1024 {
		return nil, errors.New("unexpected profile file")
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(content.Content, "\n", ""))
	if err != nil || int64(len(b)) != content.Size {
		return nil, errors.New("profile file is incomplete")
	}
	return b, nil
}
func (api *Client) input(ctx context.Context, sha string) (remoteInput, error) {
	var in remoteInput
	b, err := api.ReadFile(ctx, DataPath, sha)
	if err != nil {
		return in, err
	}
	if err = Decode(b, &in.Data); err != nil {
		return in, err
	}
	b, err = api.ReadFile(ctx, ExclusionsPath, sha)
	if err != nil {
		return in, err
	}
	if err = Decode(b, &in.Excluded); err != nil {
		return in, err
	}
	in.Readme, err = api.ReadFile(ctx, "README.md", sha)
	return in, err
}
func (api *Client) getPR(ctx context.Context, n int) (PR, error) {
	var p PR
	_, err := api.Call(ctx, "GET", fmt.Sprintf("%s/pulls/%d", profileAPI, n), nil, &p)
	return p, err
}

func (api *Client) sameMain(ctx context.Context, expected string) (bool, error) {
	var ref struct{ Object struct{ SHA string } }
	if _, err := api.Call(ctx, "GET", profileAPI+"/git/ref/heads/main", nil, &ref); err != nil {
		return false, err
	}
	if len(ref.Object.SHA) != 40 {
		return false, errors.New("invalid current main reference")
	}
	return ref.Object.SHA == expected, nil
}
func validPR(p PR) bool {
	return p.Number > 0 && p.Head.Repo.FullName == Profile && p.Head.Ref == ReviewBranch && p.Base.Ref == "main" && p.State == "open" && len(p.Head.SHA) == 40 && !p.Created.IsZero()
}

type changedFile struct{ Filename, Status string }

func generatedFile(f changedFile) bool {
	return (f.Filename == "README.md" || f.Filename == DataPath || f.Filename == ExclusionsPath) && (f.Status == "added" || f.Status == "modified")
}

// A ref can outlive a failed PR-creation request. Inspect it without resetting
// it; closed-unmerged PRs represent an editor's deliberate publication stop.
func (api *Client) orphan(ctx context.Context, main string) (string, bool, bool, error) {
	var ref struct{ Object struct{ SHA string } }
	_, err := api.Call(ctx, "GET", profileAPI+"/git/ref/heads/"+ReviewBranch, nil, &ref)
	if errors.Is(err, ErrNotFound) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, err
	}
	if len(ref.Object.SHA) != 40 {
		return "", false, false, errors.New("invalid existing review branch")
	}
	var closed []PR
	if _, err = api.Call(ctx, "GET", profileAPI+"/pulls?state=closed&base=main&head=mashfromband%3Aautomation%2Fpublic-activity&sort=created&direction=desc&per_page=1", nil, &closed); err != nil {
		return "", false, false, err
	}
	if len(closed) > 1 {
		return "", false, false, errors.New("unexpected closed review history")
	}
	if len(closed) == 1 {
		p := closed[0]
		if p.Head.Repo.FullName != Profile || p.Head.Ref != ReviewBranch || p.Base.Ref != "main" || p.State != "closed" {
			return "", false, false, errors.New("closed review identity differs")
		}
		if p.MergedAt == nil {
			return ref.Object.SHA, false, true, nil
		}
	}
	var diff struct{ Files []changedFile }
	if _, err = api.Call(ctx, "GET", profileAPI+"/compare/"+main+"..."+ref.Object.SHA, nil, &diff); err != nil {
		return "", false, false, err
	}
	if len(diff.Files) > 3 {
		return "", false, false, errors.New("orphan review branch contains unrelated files")
	}
	for _, f := range diff.Files {
		if !generatedFile(f) {
			return "", false, false, errors.New("orphan review branch contains unrelated files")
		}
	}
	pending := len(diff.Files) > 0
	if len(closed) == 1 {
		pending = ref.Object.SHA != closed[0].Head.SHA
	}
	return ref.Object.SHA, pending, false, nil
}

func (api *Client) mergeOrphanBase(ctx context.Context, main, sha string) (string, error) {
	var diff struct {
		Behind int `json:"behind_by"`
	}
	if _, err := api.Call(ctx, "GET", profileAPI+"/compare/"+main+"..."+sha, nil, &diff); err != nil {
		return "", err
	}
	if diff.Behind == 0 {
		return sha, nil
	}
	// A server-side merge retains concurrent edits or reports a conflict. There
	// is no reset, force update, or non-atomic read-then-delete of the branch.
	if _, err := api.Call(ctx, "POST", profileAPI+"/merges", map[string]string{"base": ReviewBranch, "head": main, "commit_message": "Merge main into public activity review"}, nil); err != nil {
		return "", err
	}
	next, _, held, err := api.orphan(ctx, main)
	if err != nil {
		return "", err
	}
	if next == "" || held {
		return "", errors.New("review branch changed while updating main")
	}
	return next, nil
}

func (api *Client) ensureScope(ctx context.Context, p PR) error {
	if !validPR(p) {
		return errors.New("review PR identity differs")
	}
	var files []changedFile
	if err := api.Pages(ctx, fmt.Sprintf("%s/pulls/%d/files?per_page=100", profileAPI, p.Number), &files); err != nil {
		return err
	}
	for _, f := range files {
		if !generatedFile(f) {
			return errors.New("review PR changes files outside public activity data")
		}
	}
	return nil
}

func (api *Client) updateBase(ctx context.Context, p PR) (PR, error) {
	var compare struct {
		Behind int `json:"behind_by"`
	}
	if _, err := api.Call(ctx, "GET", profileAPI+"/compare/main..."+p.Head.SHA, nil, &compare); err != nil {
		return p, err
	}
	if compare.Behind == 0 {
		return p, nil
	}
	if _, err := api.Call(ctx, "PUT", fmt.Sprintf("%s/pulls/%d/update-branch", profileAPI, p.Number), map[string]any{"expected_head_sha": p.Head.SHA}, nil); err != nil {
		return p, err
	}
	old := p.Head.SHA
	for i := 0; i < 20; i++ {
		var err error
		p, err = api.getPR(ctx, p.Number)
		if err != nil {
			return p, err
		}
		if p.Head.SHA != old {
			return p, api.ensureScope(ctx, p)
		}
		if err = api.pause(ctx, 2*time.Second); err != nil {
			return p, err
		}
	}
	return p, errors.New("review branch update has not completed")
}

func (api *Client) commit(ctx context.Context, parent string, files map[string]string) (string, error) {
	var base struct{ Tree struct{ SHA string } }
	if _, err := api.Call(ctx, "GET", profileAPI+"/git/commits/"+parent, nil, &base); err != nil {
		return "", err
	}
	if len(base.Tree.SHA) != 40 {
		return "", errors.New("invalid source tree")
	}
	tree := []map[string]string{}
	for _, name := range []string{"README.md", DataPath} {
		tree = append(tree, map[string]string{"path": name, "mode": "100644", "type": "blob", "content": files[name]})
	}
	var created struct{ SHA string }
	if _, err := api.Call(ctx, "POST", profileAPI+"/git/trees", map[string]any{"base_tree": base.Tree.SHA, "tree": tree}, &created); err != nil {
		return "", err
	}
	if _, err := api.Call(ctx, "POST", profileAPI+"/git/commits", map[string]any{"message": "chore(profile): update public development activity", "tree": created.SHA, "parents": []string{parent}}, &created); err != nil {
		return "", err
	}
	if len(created.SHA) != 40 {
		return "", errors.New("invalid activity commit")
	}
	return created.SHA, nil
}

func (api *Client) createReview(ctx context.Context, sha string) (PR, error) {
	var p PR
	body := "公開製品の利用者向けの変更と正式リリースを、READMEの専用領域へ反映します。\n\n文章や技術の修正は `data/public-activity.json`、非掲載は `data/activity-excluded.json` のIDと理由で指定してください。編集は次の収集でも保持します。`hold` ラベルで公開を保留できます。通常はこのPRの作成から7日後、同じHEADの正式検査が成功していれば自動で反映します。\n\n手書きの紹介・3言語・非公開活動の集計は保持します。"
	_, err := api.Call(ctx, "POST", profileAPI+"/pulls", map[string]any{"title": "chore: update public development activity", "head": ReviewBranch, "base": "main", "body": body}, &p)
	if err != nil {
		return p, err
	}
	if !validPR(p) || p.Head.SHA != sha {
		return p, errors.New("created review PR did not match expected head")
	}
	return p, nil
}

func (api *Client) publish(ctx context.Context, p PR, parent string, files map[string]string, existingBranch bool) (PR, error) {
	sha, err := api.commit(ctx, parent, files)
	if err != nil {
		return p, err
	}
	if !existingBranch {
		if _, err = api.Call(ctx, "POST", profileAPI+"/git/refs", map[string]string{"ref": "refs/heads/" + ReviewBranch, "sha": sha}, nil); err != nil {
			return p, err
		}
	} else {
		// Non-force fast-forward rejects edits made after the immutable input SHA.
		_, err = api.Call(ctx, "PATCH", profileAPI+"/git/refs/heads/"+ReviewBranch, map[string]any{"sha": sha, "force": false}, nil)
	}
	if err != nil {
		return p, err
	}
	if p.Number == 0 {
		return api.createReview(ctx, sha)
	}
	p, err = api.getPR(ctx, p.Number)
	if err != nil {
		return p, err
	}
	if !validPR(p) || p.Head.SHA != sha {
		return p, errors.New("published review PR did not match expected head")
	}
	return p, nil
}

type checkRun struct {
	Name, Status, Conclusion string
	Head                     string `json:"head_sha"`
	App                      struct{ ID int64 }
}

var requiredChecks = []string{"Markdown links", "Python unit tests", "Go activity tests"}

func (api *Client) checks(ctx context.Context, sha string) (bool, bool, error) {
	var result struct {
		Total int        `json:"total_count"`
		Runs  []checkRun `json:"check_runs"`
	}
	if _, err := api.Call(ctx, "GET", profileAPI+"/commits/"+sha+"/check-runs?per_page=100&filter=latest", nil, &result); err != nil {
		return false, false, err
	}
	if result.Total > 100 {
		return false, false, errors.New("check run list incomplete")
	}
	seen := map[string]checkRun{}
	for _, run := range result.Runs {
		if run.Head == sha && run.App.ID == 15368 {
			if _, ok := seen[run.Name]; !ok {
				seen[run.Name] = run
			}
		}
	}
	all, failed := true, false
	for _, name := range requiredChecks {
		r, ok := seen[name]
		if !ok || r.Status != "completed" {
			all = false
			continue
		}
		if r.Conclusion != "success" {
			all = false
			failed = true
		}
	}
	return all, failed, nil
}

func delay(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (api *Client) validateHead(ctx context.Context, p PR) error {
	all, failed, err := api.checks(ctx, p.Head.SHA)
	if err != nil {
		return err
	}
	if all {
		return nil
	}
	if failed {
		return errors.New("formal checks failed on review head")
	}
	for _, workflow := range []string{"tests.yml", "profile-readme.yml"} {
		if _, err = api.Call(ctx, "POST", profileAPI+"/actions/workflows/"+workflow+"/dispatches", map[string]string{"ref": ReviewBranch}, nil); err != nil {
			return err
		}
	}
	for i := 0; i < 60; i++ {
		if err = api.pause(ctx, 5*time.Second); err != nil {
			return err
		}
		current, e := api.getPR(ctx, p.Number)
		if e != nil {
			return e
		}
		if current.Head.SHA != p.Head.SHA {
			return errors.New("review head changed during validation")
		}
		all, failed, err = api.checks(ctx, p.Head.SHA)
		if err != nil {
			return err
		}
		if all {
			return nil
		}
		if failed {
			return errors.New("formal checks failed on review head")
		}
	}
	return errors.New("formal checks are not complete yet")
}

// Sync reads source files by immutable SHA and only writes the two generated
// files. PR prose, exclusions, other files, and manually edited activity fields
// survive; concurrent edits are rejected by a non-force reference update.
func Sync(ctx context.Context, cfg Config, api *Client, now time.Time, reviewDelay time.Duration) (SyncResult, error) {
	result := SyncResult{Outcome: "unchanged"}
	var meta publicRepo
	if _, err := api.Call(ctx, "GET", profileAPI, nil, &meta); err != nil {
		return result, err
	}
	if meta.ID != 1221617650 || meta.FullName != Profile || meta.Private == nil || *meta.Private || meta.Visibility != "public" {
		return result, errors.New("profile repository identity differs")
	}
	api.RepositoryIDs[Profile] = 1221617650
	var main struct{ Object struct{ SHA string } }
	if _, err := api.Call(ctx, "GET", profileAPI+"/git/ref/heads/main", nil, &main); err != nil {
		return result, err
	}
	if len(main.Object.SHA) != 40 {
		return result, errors.New("invalid main reference")
	}
	if len(api.TrustedMain) != 40 {
		return result, errors.New("sync requires the trusted collector checkout SHA")
	}
	if api.TrustedMain != main.Object.SHA {
		result.Outcome = "trusted-checkout-stale"
		return result, nil
	}
	var open []PR
	if err := api.Pages(ctx, profileAPI+"/pulls?state=open&base=main&head=mashfromband%3Aautomation%2Fpublic-activity&per_page=100", &open); err != nil {
		return result, err
	}
	if len(open) > 1 {
		return result, errors.New("more than one review PR")
	}
	p := PR{}
	parent := main.Object.SHA
	existingBranch, pending := false, false
	if len(open) == 1 {
		p = open[0]
		if err := api.ensureScope(ctx, p); err != nil {
			return result, err
		}
		parent = p.Head.SHA
		existingBranch = true
	} else {
		sha, wantsReview, held, err := api.orphan(ctx, main.Object.SHA)
		if err != nil {
			return result, err
		}
		if held {
			result.Outcome = "review-closed"
			return result, nil
		}
		if sha != "" {
			parent = sha
			existingBranch = true
			pending = wantsReview
		}
	}
	in, err := api.input(ctx, parent)
	if err != nil {
		return result, err
	}
	fresh, err := cfg.Collect(ctx, api)
	if err != nil {
		return result, err
	}
	data, readme, err := Build(cfg, in.Data, in.Excluded, string(in.Readme), fresh, now)
	if err != nil {
		return result, err
	}
	changed := string(Encode(data)) != string(Encode(in.Data)) || readme != string(in.Readme)
	due := p.Number > 0 && Ready(p.Created, p.labelNames(), p.Draft, now, reviewDelay)
	if existingBranch && (changed || due || pending) {
		current, err := api.sameMain(ctx, main.Object.SHA)
		if err != nil {
			return result, err
		}
		if !current {
			result.Outcome = "base-changed-retry"
			return result, nil
		}
		if p.Number > 0 {
			p, err = api.updateBase(ctx, p)
			parent = p.Head.SHA
		} else {
			parent, err = api.mergeOrphanBase(ctx, main.Object.SHA, parent)
		}
		if err != nil {
			return result, err
		}
		in, err = api.input(ctx, parent)
		if err != nil {
			return result, err
		}
		data, readme, err = Build(cfg, in.Data, in.Excluded, string(in.Readme), fresh, now)
		if err != nil {
			return result, err
		}
		changed = string(Encode(data)) != string(Encode(in.Data)) || readme != string(in.Readme)
	}
	result.Entries = len(data.Entries)
	if changed || (p.Number == 0 && pending) {
		current, err := api.sameMain(ctx, main.Object.SHA)
		if err != nil {
			return result, err
		}
		if !current {
			result.Outcome = "base-changed-retry"
			return result, nil
		}
	}
	if changed {
		p, err = api.publish(ctx, p, parent, map[string]string{"README.md": readme, DataPath: string(Encode(data))}, existingBranch)
		if err != nil {
			return result, err
		}
		result.Changed = true
		result.Outcome = "review-updated"
	} else if p.Number == 0 && pending {
		p, err = api.createReview(ctx, parent)
		if err != nil {
			return result, err
		}
		result.Outcome = "review-recovered"
	}
	if p.Number == 0 {
		return result, nil
	}
	result.PR, result.URL, result.Head = p.Number, p.URL, p.Head.SHA
	if err = api.validateHead(ctx, p); err != nil {
		return result, err
	}
	// Re-read labels and identity after checks, immediately before a fixed-SHA merge.
	current, err := api.getPR(ctx, p.Number)
	if err != nil {
		return result, err
	}
	if !validPR(current) || current.Head.SHA != p.Head.SHA {
		return result, errors.New("review PR changed before publication")
	}
	if !Ready(current.Created, current.labelNames(), current.Draft, time.Now().UTC(), reviewDelay) {
		result.Outcome = "review-held-or-grace-period"
		return result, nil
	}
	baseUnchanged, err := api.sameMain(ctx, main.Object.SHA)
	if err != nil {
		return result, err
	}
	if !baseUnchanged {
		result.Outcome = "base-changed-retry"
		return result, nil
	}
	var merged struct {
		Merged bool
		SHA    string
	}
	if _, err = api.Call(ctx, "PUT", fmt.Sprintf("%s/pulls/%d/merge", profileAPI, p.Number), map[string]string{"sha": p.Head.SHA, "merge_method": "squash"}, &merged); err != nil {
		return result, err
	}
	if !merged.Merged || len(merged.SHA) != 40 {
		return result, errors.New("GitHub did not confirm review merge")
	}
	result.Outcome = "published"
	result.Published = true
	// Keep the one automation branch for the next cycle. Deleting it after a
	// separate GET could discard a late editor's commit; later cycles merge main.
	return result, nil
}
