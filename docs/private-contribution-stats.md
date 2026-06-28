# Private Contribution Stats

This profile repository publishes an aggregate-only mirror of the contribution
statistics GitHub shows to the signed-in owner ("self view") in
`assets/private-contributions.svg`. The numbers include private contributions,
which public profile cards cannot read.

Contributions made in organizations the token cannot access are intentionally
not counted, so the published total can be lower than the owner's signed-in
profile view. The SVG states this, and the steps to include an organization are
in [Organization access](#organization-access-required-for-org-contributions).

## Privacy Model

The generated SVG may publish:

- Total contributions over the last year (private included; mirrors the owner self view for accessible organizations)
- Commit, authored pull request, authored issue, and pull request review totals
- Total accessible repository count
- Private and public repository counts
- Active repository counts for recent time windows
- Count of organization-owned workspaces
- Top primary development languages by count
- Latest repository activity date

The generated SVG must not publish:

- Repository names
- Repository URLs
- Organization names (only an aggregate workspace count is shown)
- Issue titles
- Pull request titles
- Commit messages

## Token Setup

Create a repository secret named `PROFILE_STATS_TOKEN`.

The token must belong to the profile owner so the contribution query returns the
same private-inclusive totals shown on the owner's own profile.

Recommended token type:

- Fine-grained personal access token, owner-scoped, read-only, or
- Classic personal access token with `repo` and `read:user` scopes

Required access for full fidelity:

- `read:user` (or fine-grained "Profile" read) — required for the
  `contributionsCollection` GraphQL query
- `repo` / repository contents read — required for private contributions and
  private repository counts to be included

Without private repository access the query still succeeds, but private
contributions are reported as part of the public totals only.

### Organization access (required for org contributions)

`contributionsCollection` only counts contributions in organizations the token
can actually access. Contributions made in an organization are **silently
excluded** from the totals when the token cannot read that organization, which
causes an undercount versus the owner's signed-in profile view. For every
organization whose activity should be counted, ensure:

- **SAML SSO authorization**: if the organization enforces SAML single sign-on,
  the token must be authorized for it. Settings → Developer settings → Personal
  access tokens → select the token → **Configure SSO** → Authorize for each
  organization. (For fine-grained tokens, grant the token to the organization
  as the resource owner.)
- **Organization PAT policy**: the organization must allow access via personal
  access tokens. Organization Settings → Third-party Access / Personal access
  tokens → allow (or approve) the token. Classic-PAT access can be restricted at
  the org level.
- **Membership**: the token owner must be a member (or collaborator) of the org.

To verify which organizations are actually counted, run the diagnostic in the
Counting Model section below with the token.

## Counting Model

The script reads the GraphQL `contributionsCollection` for the configured user,
which is the same data source GitHub uses for the contribution graph. When the
token belongs to the owner, this mirrors the owner self view and includes
private contributions:

- `contributionCalendar.totalContributions` — the authoritative total shown on
  the profile (private included)
- `totalCommitContributions` — commits
- `totalPullRequestContributions` — authored pull requests
- `totalIssueContributions` — authored issues
- `totalPullRequestReviewContributions` — pull request reviews

When the token can read private repositories (`repo` scope), private
contributions are folded directly into these totals rather than reported as a
separate restricted count.

Repository metadata (accessible repository count, private/public split,
organization workspace count, and stack detection) comes from the REST
`/user/repos` endpoint with `affiliation=owner,collaborator,organization_member`,
so activity across every repository you are involved in is counted.

### Diagnosing missing organizations

If the total is lower than the signed-in profile view, list the repositories the
token actually counts and confirm the expected organizations appear. Run with
the same token configured as `PROFILE_STATS_TOKEN` (replace `TOKEN`):

```bash
curl -s -H "Authorization: Bearer TOKEN" https://api.github.com/graphql -d '{
  "query": "query { viewer { contributionsCollection { contributionCalendar { totalContributions } commitContributionsByRepository(maxRepositories: 100) { repository { nameWithOwner isPrivate } contributions { totalCount } } } } }"
}'
```

Organizations that are missing from the output are not accessible to the token —
fix their access per the "Organization access" steps above, then re-run.

### Stack detection

Stack detection considers development languages only. Repositories with no
detected language are skipped, and `PowerShell` is excluded because it is shell
automation rather than part of the reported development stack.

## Testing

The generator's pure functions and null/clamp handling are covered by standard
library unit tests (no extra dependencies):

```
python -m unittest discover -s tests
```

The `Tests` workflow runs this suite on every pull request and on every push to
`main`, and is configured as a required status check before merge.

## Updating Stats

The `All Activity Signals` workflow runs once per day and can also be run
manually. A daily run keeps the profile fresh while staying comfortably within
normal GitHub Actions usage for a public profile repository.

When the generated SVG changes, the workflow opens a pull request instead of
pushing directly to `main`. The pull request is created with
`PROFILE_STATS_TOKEN` (a PAT) rather than the default `GITHUB_TOKEN`, so the
required status checks run on it; pull requests opened by `GITHUB_TOKEN` do not
trigger workflows.

If `PROFILE_STATS_TOKEN` is not configured, the workflow reports a warning and
skips the refresh steps. This keeps the scheduled workflow green without
replacing the last published aggregate SVG with setup placeholder data.
