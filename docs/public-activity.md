# Public development activity

Issue: [#30](https://github.com/mashfromband/mashfromband/issues/30).

The profile keeps its existing introduction, three languages and private
contribution aggregate. The Go collector adds ordinary Markdown between
`PUBLIC-ACTIVITY` markers. It does not use an LLM, new service, database or AI API.

## Sources and publication rules

`data/activity-config.json` is the explicit public-product allowlist and canonical
technology dictionary. Every run verifies each repository's numeric ID, exact
name and public visibility before fetching any PR/release detail. No repository
discovery or private contribution detail is used. The PC operations repository is
deliberately absent because its public history includes internal operational work.

Only useful feature/fix/performance titles or configured product-specific topics
are admitted. Maintenance, CI, internal/customer context, dependency updates,
`activity:skip`, `profile:skip`, `internal` and `dependencies` are excluded. AI-created
useful PRs remain eligible. Releases must be published, non-draft and non-prerelease.
Headlines and the first prose paragraph are normalized as inert plain text;
internal prose falls back to an eligible public headline. The raw PR body is never
saved. The summary does not claim deployment or acceptance beyond the linked PR.

IDs use repository ID, source kind and source number. All pages must succeed;
metadata, request, pagination or validation failure leaves published files intact.
GitHub pagination through `/repositories/<id>` is bound to the independently
verified allowlisted ID. Links to any different origin or repository are refused.

## Editing a proposal

- Edit an existing item's `title`, `summary` or `technologies` in
  `data/public-activity.json`. The same stable ID is retained on later collection.
  `tech_source` may be set to `edited` for an editorial technology selection.
- Put an ID and a nonempty reason in `data/activity-excluded.json` to remove it
  permanently, including from already collected data.
- Add `hold` to the confirmation PR to stop automatic publication. Drafts also
  remain unpublished.
- Keep handwritten text outside the marker pair. A direct edit inside the
  generated block is detected by its saved hash and is preserved by failing the
  update. Make the corresponding JSON edit instead.

Technology labels `tech:<canonical-id>` take precedence over repository defaults.
Unknown IDs/parents, cycles and duplicate source IDs fail validation. Parent
evidence is deduplicated by activity ID; technologies without evidence are omitted.
Repository defaults describe project-level evidence, not measured proficiency.

## Daily workflow

The existing **All Activity Signals** workflow runs once daily and manually. Its
private aggregate job alone receives `PROFILE_STATS_TOKEN`. The separate public
job only receives the ephemeral `GITHUB_TOKEN`, with profile contents/PR write,
Checks read, commit-status write and Actions dispatch permissions. It never reads
`PROFILE_STATS_TOKEN` or `GH_TOKEN`.

One `automation/public-activity` PR accumulates changes. The branch is not rebuilt
or force pushed. Files are read by immutable SHA; a non-force ref update rejects a
concurrent editor. Other changed paths/deletions are refused. When needed, GitHub's
expected-head branch-update API integrates main and retains manual edits; merge
conflicts fail without resetting the proposal. If PR creation was interrupted
after ref creation, the existing branch is inspected and its permitted files and
editorial changes are reused. Unrelated paths or renames are refused. A PR that
was closed without merging stays stopped.

The collector does not delete the automation branch after publication, protecting
late edits from a separate read-then-delete operation. The repository's existing
automatic branch-deletion setting may still remove it; both states are supported.
The next changed cycle merges main into it on GitHub and opens a new confirmation
PR; an unchanged cycle makes no commit/PR. This also avoids a read-then-delete
race against an editor. The implementation branch is separate.

Normal publication is due **168 hours after the PR's `created_at`**, irrespective
of later updates. After the deadline and without `hold`, formal **Markdown links**,
**Python unit tests** and **Go activity tests** must succeed on that exact HEAD
from the GitHub Actions app. The collector explicitly dispatches the two existing
check workflows; it does not assume token-generated PR/push events will run them.
GitHub does not count `workflow_dispatch` job checks toward protected-branch PR
requirements. Before publication the collector verifies each successful check's
Actions app, exact HEAD, workflow path, source repository, event, branch and log
URL. Only after all three are verified does it report their real results through
the commit-status API, using the same check names and links to the actual jobs.
Existing required checks and their required GitHub Actions app are retained.
Matching reports are reused; failed/unverified checks never become success
statuses. Checks are refreshed before GitHub's seven-day result expiry, so an
unchanged proposal can publish after its normal seven-day grace period.
The collector then re-reads the PR and labels and merges with the validated SHA
pinned. A late hold or edit during status reporting still stops publication.

References: [required checks and eligible workflow events](https://docs.github.com/en/pull-requests/how-tos/merge-and-close-pull-requests/troubleshooting-required-status-checks),
[commit-status API](https://docs.github.com/en/rest/commits/statuses).

`TRUSTED_COLLECTOR_SHA` is the actual checkout commit reported by the checkout
action. It must match main at the start. Main is checked again before branch/data
writes and immediately before publication. If it advances, the run defers with
`base-changed-retry` (or `trusted-checkout-stale`) and does not publish. The next
normal run starts with a fresh trusted checkout, re-merges main, regenerates the
proposal and validates the new head. This avoids hot-reloading policy into an old
collector. The repository's strict required checks remain in force as well.

The manual workflow has `review_hours=0` solely for explicit acceptance or an
intentional immediate publication. Scheduled runs always use 168. This still
computes the deadline from the real `created_at` and honors hold/draft/checks; it
does not fake an elapsed seven days. `public_only=true` leaves the aggregate job
untouched during these checks.

No new commit or PR is made if generated bytes are unchanged. A timestamp alone
does not change the data. The README records the last changed generation date;
workflow logs record subsequent successful unchanged refreshes.

## Acceptance scenarios and commands

Given editable data and new API data for the same ID, refreshing retains the
edited fields. Given a reasoned exclusion, the item stays absent. Given a failed
second API page or a private/wrong-ID source, collection fails without publishing
partial data. Given malformed markers or an edited generated block, no overwrite
occurs. Given a mature PR and passing fixed-head checks, it publishes; hold,
draft, future creation and a concurrent edit prevent publication. A repeated
identical refresh creates no commit/PR.

```text
go test ./...
go vet ./...
go run ./cmd/profile-activity -mode check
go run ./cmd/profile-activity -mode collect
```

`render` regenerates from the editable JSON without network access. `sync` is the
workflow entrypoint and requires `GITHUB_TOKEN`. Local `collect` may use anonymous
public read. Local output replacement validates all inputs first and restores the
previous data if the README replacement fails; published output is one Git commit.

The initial four behavioral tests were run against stubs and failed for missing
merge/edit retention, marker updates, deadline handling and exclusion validation.
They passed after implementation. Further tests cover real HTTP-shaped pagination,
public-source verification, filters, plain summaries, technology evidence,
fixed-SHA check provenance, no-op remote updates and concurrent editing failures.
The real dispatch-check/HTTP405 failure is reproduced before its repair. Tests
also reject mismatched or failed workflow evidence and failed status reporting,
reuse matching status reports and refresh expired results before publication.
The actual production config is tested for English customer-context exclusion,
including summary fallback. Orphan recovery, retained merged branches, deliberate
closure, and the pending-check dispatch/wait/SHA-change paths are tested too.
The implementation was also exercised against the actual allowlisted public API.
Real workflow/PR/hold/edit/no-op results are recorded in the implementation PR.

## Reference and license

Inspired by [Tora29's article](https://zenn.dev/tora29/articles/static-first-portfolio),
published 2026-09-28 and updated 2026-10-04; read on 2026-10-21. The referenced
repository's license was unconfirmed (`license=null`), so no source code was
copied. This is an independent Go implementation adapted to a GitHub README;
there is no Astro site or browser JavaScript.
