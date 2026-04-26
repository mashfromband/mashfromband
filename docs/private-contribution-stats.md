# Private Contribution Stats

This profile repository can publish aggregate-only private repository activity in `assets/private-contributions.svg`.

## Privacy Model

The generated SVG may publish:

- Total accessible private repository count
- Active private repository counts for recent time windows
- Count of organization-owned private workspaces
- Top primary languages by count
- Latest private repository activity date

The generated SVG must not publish:

- Private repository names
- Private repository URLs
- Organization names derived only from private access
- Issue titles
- Pull request titles
- Commit messages

## Token Setup

Create a repository secret named `PROFILE_STATS_TOKEN`.

Recommended token type:

- Fine-grained personal access token
- Read-only access
- Repository access limited to the private repositories that should be counted
- Permissions:
  - Metadata: read
  - Contents: read

Classic tokens also work with `repo` scope, but fine-grained tokens are preferred.

## Updating Stats

The `Private Contribution Stats` workflow runs on a daily schedule and can also be run manually.

When the generated SVG changes, the workflow opens a pull request instead of pushing directly to `main`.
