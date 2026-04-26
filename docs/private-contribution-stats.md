# Private Contribution Stats

This profile repository can publish aggregate-only contribution activity in `assets/private-contributions.svg`.

## Privacy Model

The generated SVG may publish:

- Total contributions over the last year
- Current contribution streak
- Longest contribution streak
- Active contribution days
- Commit, pull request, and review contribution totals
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

Classic tokens also work with `repo` scope, but fine-grained tokens are preferred.

## Updating Stats

The `Private Contribution Stats` workflow runs on a daily schedule and can also be run manually.

When the generated SVG changes, the workflow opens a pull request instead of pushing directly to `main`.
