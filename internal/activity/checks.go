package activity

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type workflowRun struct {
	ID                              int64
	Event, Path, Status, Conclusion string
	Head                            string `json:"head_sha"`
	Branch                          string `json:"head_branch"`
	Repository                      publicRepo
	HeadRepository                  publicRepo `json:"head_repository"`
}

type commitStatus struct {
	Context, State string
	TargetURL      string `json:"target_url"`
	Creator        struct{ Login string }
}

var ErrWorkflowPending = errors.New("validated source workflow is still completing")

func (api *Client) verifyCheckSources(ctx context.Context, sha string, passed []checkRun) error {
	if len(passed) != len(requiredChecks) {
		return errors.New("incomplete validation evidence")
	}
	sources := map[int64]workflowRun{}
	pending := false
	for i, check := range passed {
		if check.Name != requiredChecks[i] || check.Head != sha || check.App.ID != 15368 || check.ID <= 0 || check.Suite.ID <= 0 || check.Status != "completed" || check.Conclusion != "success" {
			return errors.New("unexpected validation check identity")
		}
		run, ok := sources[check.Suite.ID]
		if !ok {
			var list struct {
				Total int           `json:"total_count"`
				Runs  []workflowRun `json:"workflow_runs"`
			}
			if _, err := api.Call(ctx, "GET", fmt.Sprintf("%s/actions/runs?check_suite_id=%d&per_page=2", profileAPI, check.Suite.ID), nil, &list); err != nil {
				return err
			}
			if list.Total != 1 || len(list.Runs) != 1 {
				return errors.New("validation check has no unique workflow run")
			}
			run = list.Runs[0]
			sources[check.Suite.ID] = run
		}
		workflow := "tests.yml"
		if check.Name == "Markdown links" {
			workflow = "profile-readme.yml"
		}
		if run.ID <= 0 || run.Path != ".github/workflows/"+workflow || run.Head != sha || run.Branch != ReviewBranch || (run.Event != "workflow_dispatch" && run.Event != "pull_request") || run.Repository.ID != ProfileRepositoryID || run.Repository.FullName != Profile || run.HeadRepository.ID != ProfileRepositoryID || run.HeadRepository.FullName != Profile {
			return errors.New("validation workflow provenance differs")
		}
		if check.DetailsURL != fmt.Sprintf("https://github.com/%s/actions/runs/%d/job/%d", Profile, run.ID, check.ID) {
			return errors.New("validation log URL differs")
		}
		if run.Status != "completed" {
			switch run.Status {
			case "queued", "in_progress", "requested", "waiting", "pending":
				if run.Conclusion != "" {
					return errors.New("incomplete source workflow has a conclusion")
				}
				pending = true
			default:
				return errors.New("unexpected source workflow state")
			}
		} else if run.Conclusion != "success" {
			return errors.New("source workflow did not succeed")
		}
	}
	if pending {
		return ErrWorkflowPending
	}
	return nil
}

// Report actual successful workflow evidence, never a guessed or skipped result.
// No status is written until every source has passed identity and completion.
func (api *Client) reportChecks(ctx context.Context, sha string, passed []checkRun) error {
	for attempt := 0; attempt < 5; attempt++ {
		err := api.verifyCheckSources(ctx, sha, passed)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrWorkflowPending) || attempt == 4 {
			return err
		}
		if err = api.pause(ctx, 2*time.Second); err != nil {
			return err
		}
	}
	var current []commitStatus
	if err := api.Pages(ctx, profileAPI+"/commits/"+sha+"/statuses?per_page=100", &current); err != nil {
		return err
	}
	for _, check := range passed {
		alreadyReported := false
		for _, status := range current {
			if status.Context == check.Name {
				alreadyReported = status.State == "success" && status.TargetURL == check.DetailsURL && status.Creator.Login == "github-actions[bot]"
				break
			}
		}
		if alreadyReported {
			continue
		}
		var reported commitStatus
		body := map[string]string{
			"context": check.Name, "state": "success", "target_url": check.DetailsURL,
			"description": "Passed exact-head workflow validation; source job " + fmt.Sprint(check.ID),
		}
		if _, err := api.Call(ctx, "POST", profileAPI+"/statuses/"+sha, body, &reported); err != nil {
			return err
		}
		if reported.Context != check.Name || reported.State != "success" || reported.TargetURL != check.DetailsURL || reported.Creator.Login != "github-actions[bot]" {
			return errors.New("GitHub did not confirm the validation status")
		}
	}
	return nil
}
