# Private Contribution Stats

This profile repository can publish aggregate-only activity across accessible repositories in `assets/private-contributions.svg`.

## Privacy Model

The generated SVG may publish:

- Total activity over the last year
- Commit, authored pull request, authored issue, and reviewed pull request totals
- Total accessible repository count
- Private and public repository counts
- Active repository counts for recent time windows
- Count of organization-owned private workspaces
- Top primary languages by count
- Latest repository activity date

The generated SVG must not publish:

- Repository names
- Repository URLs
- Organization names derived only from private access
- Issue titles
- Pull request titles
- Commit messages

## Token Setup

Create a repository secret named `PROFILE_STATS_TOKEN`.

Recommended token type:

- Fine-grained personal access token
- Read-only access
- Repository access limited to the repositories that should be counted
- Permissions:
  - Metadata: read
  - Contents: read
  - Issues: read
  - Pull requests: read

Classic tokens also work with `repo` scope, but fine-grained tokens are preferred.

## Counting Model

The script does not rely on GitHub's profile contribution calendar. Instead, it scans accessible repositories and GitHub search results to count:

- Commits authored by `mashfromband`
- Pull requests authored by `mashfromband`
- Issues authored by `mashfromband`
- Pull requests reviewed by `mashfromband`

This is intended to represent practical work across public and private repositories more accurately than public profile cards.

## Updating Stats

The `All Activity Signals` workflow runs once per day and can also be run manually. A daily run keeps the profile fresh while staying comfortably within normal GitHub Actions usage for a public profile repository.

When the generated SVG changes, the workflow opens a pull request instead of pushing directly to `main`.
