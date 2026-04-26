#!/usr/bin/env python3
"""Generate aggregate-only contribution stats for the profile README.

The generated SVG intentionally avoids repository names, repository URLs, commit
messages, issue titles, and organization names. It is safe to publish because it
only contains aggregate counts derived from contribution and repository metadata
the token can read.
"""

from __future__ import annotations

import argparse
import html
import json
import os
from collections import Counter
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import Request, urlopen


API_ROOT = "https://api.github.com"
GRAPHQL_ROOT = "https://api.github.com/graphql"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Generate a public-safe SVG from aggregate GitHub contribution data."
    )
    parser.add_argument("--output", default="assets/private-contributions.svg")
    parser.add_argument("--username", default=os.getenv("GITHUB_ACTOR", "mashfromband"))
    return parser.parse_args()


def request_json(url: str, token: str) -> tuple[Any, dict[str, str]]:
    request = Request(
        url,
        headers={
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {token}",
            "X-GitHub-Api-Version": "2022-11-28",
            "User-Agent": "mashfromband-profile-private-stats",
        },
    )
    try:
        with urlopen(request, timeout=30) as response:
            payload = json.loads(response.read().decode("utf-8"))
            headers = {key.lower(): value for key, value in response.headers.items()}
            return payload, headers
    except HTTPError as error:
        detail = error.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"GitHub API request failed: {error.code} {detail}") from error


def request_graphql(query: str, variables: dict[str, Any], token: str) -> dict[str, Any]:
    request = Request(
        GRAPHQL_ROOT,
        data=json.dumps({"query": query, "variables": variables}).encode("utf-8"),
        headers={
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
            "User-Agent": "mashfromband-profile-private-stats",
        },
        method="POST",
    )
    try:
        with urlopen(request, timeout=30) as response:
            payload = json.loads(response.read().decode("utf-8"))
    except HTTPError as error:
        detail = error.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"GitHub GraphQL request failed: {error.code} {detail}") from error

    if payload.get("errors"):
        raise RuntimeError(f"GitHub GraphQL returned errors: {payload['errors']}")
    return payload["data"]


def parse_link_header(value: str | None) -> dict[str, str]:
    links: dict[str, str] = {}
    if not value:
        return links

    for part in value.split(","):
        section = part.strip().split(";")
        if len(section) < 2:
            continue
        url = section[0].strip()
        rel = section[1].strip()
        if url.startswith("<") and url.endswith(">") and rel.startswith('rel="'):
            links[rel[5:-1]] = url[1:-1]
    return links


def fetch_accessible_repositories(token: str) -> list[dict[str, Any]]:
    params = urlencode(
        {
            "visibility": "all",
            "affiliation": "owner,collaborator,organization_member",
            "per_page": "100",
            "sort": "updated",
            "direction": "desc",
        }
    )
    url = f"{API_ROOT}/user/repos?{params}"
    repositories: list[dict[str, Any]] = []

    while url:
        payload, headers = request_json(url, token)
        if not isinstance(payload, list):
            raise RuntimeError("Unexpected GitHub API response for /user/repos")
        repositories.extend(payload)
        url = parse_link_header(headers.get("link")).get("next", "")

    return repositories


def fetch_contribution_calendar(token: str, username: str) -> dict[str, Any]:
    to_date = datetime.now(UTC)
    from_date = to_date - timedelta(days=365)
    data = request_graphql(
        """
        query($login: String!, $from: DateTime!, $to: DateTime!) {
          user(login: $login) {
            contributionsCollection(from: $from, to: $to) {
              contributionCalendar {
                totalContributions
                weeks {
                  contributionDays {
                    date
                    contributionCount
                  }
                }
              }
              restrictedContributionsCount
              totalCommitContributions
              totalIssueContributions
              totalPullRequestContributions
              totalPullRequestReviewContributions
            }
          }
        }
        """,
        {
            "login": username,
            "from": from_date.isoformat(),
            "to": to_date.isoformat(),
        },
        token,
    )
    user = data.get("user")
    if not user:
        raise RuntimeError(f"GitHub user not found: {username}")
    return user["contributionsCollection"]


def parse_github_datetime(value: str | None) -> datetime | None:
    if not value:
        return None
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def calculate_streaks(days: list[dict[str, Any]]) -> dict[str, int]:
    parsed_days = [
        (datetime.strptime(day["date"], "%Y-%m-%d").date(), int(day["contributionCount"]))
        for day in days
    ]
    parsed_days.sort(key=lambda item: item[0])

    longest = 0
    running = 0
    for _, count in parsed_days:
        if count > 0:
            running += 1
            longest = max(longest, running)
        else:
            running = 0

    current = 0
    relevant_days = parsed_days
    if parsed_days and parsed_days[-1][1] == 0:
        # GitHub cards commonly keep an active streak alive until yesterday when
        # today's contribution count is still zero.
        relevant_days = parsed_days[:-1]
    for _, count in reversed(relevant_days):
        if count <= 0:
            break
        current += 1

    active_days = sum(1 for _, count in parsed_days if count > 0)
    return {"current": current, "longest": longest, "active_days": active_days}


def summarize(
    repositories: list[dict[str, Any]], contributions: dict[str, Any] | None
) -> dict[str, str]:
    now = datetime.now(UTC)
    updated_dates = [
        parsed
        for parsed in (parse_github_datetime(repo.get("updated_at")) for repo in repositories)
        if parsed is not None
    ]
    language_counts = Counter(repo.get("language") or "Other" for repo in repositories)
    private_count = sum(1 for repo in repositories if repo.get("private") is True)
    public_count = sum(1 for repo in repositories if repo.get("private") is False)
    organization_count = len(
        {
            repo.get("owner", {}).get("login")
            for repo in repositories
            if repo.get("owner", {}).get("type") == "Organization"
        }
    )

    active_90 = sum(1 for updated_at in updated_dates if updated_at >= now - timedelta(days=90))
    active_365 = sum(1 for updated_at in updated_dates if updated_at >= now - timedelta(days=365))
    top_languages = ", ".join(
        f"{language} x{count}" for language, count in language_counts.most_common(4)
    )

    calendar = (contributions or {}).get("contributionCalendar", {})
    days = [
        day
        for week in calendar.get("weeks", [])
        for day in week.get("contributionDays", [])
    ]
    streaks = calculate_streaks(days) if days else {"current": 0, "longest": 0, "active_days": 0}

    return {
        "total_contributions": str(calendar.get("totalContributions", 0)),
        "current_streak": str(streaks["current"]),
        "longest_streak": str(streaks["longest"]),
        "active_days": str(streaks["active_days"]),
        "commit_contributions": str((contributions or {}).get("totalCommitContributions", 0)),
        "pull_requests": str((contributions or {}).get("totalPullRequestContributions", 0)),
        "reviews": str((contributions or {}).get("totalPullRequestReviewContributions", 0)),
        "restricted_contributions": str((contributions or {}).get("restrictedContributionsCount", 0)),
        "accessible_repos": str(len(repositories)),
        "private_repos": str(private_count),
        "public_repos": str(public_count),
        "active_90": str(active_90),
        "active_365": str(active_365),
        "organization_count": str(organization_count),
        "top_languages": top_languages or "No languages detected",
        "latest_update": max(updated_dates).strftime("%Y-%m-%d") if updated_dates else "N/A",
        "generated_at": now.strftime("%Y-%m-%d"),
    }


def text(value: str) -> str:
    return html.escape(value, quote=True)


def render_svg(summary: dict[str, str], *, username: str, configured: bool) -> str:
    if not configured:
        summary = {
            "total_contributions": "Setup",
            "current_streak": "Needed",
            "longest_streak": "Needed",
            "active_days": "Needed",
            "commit_contributions": "Needed",
            "pull_requests": "Needed",
            "reviews": "Needed",
            "restricted_contributions": "Needed",
            "accessible_repos": "Setup",
            "private_repos": "Setup",
            "public_repos": "Setup",
            "active_90": "Needed",
            "active_365": "Needed",
            "organization_count": "Needed",
            "top_languages": "Add PROFILE_STATS_TOKEN to generate aggregate contribution signals",
            "latest_update": "N/A",
            "generated_at": datetime.now(UTC).strftime("%Y-%m-%d"),
        }

    cards = [
        ("Total", summary["total_contributions"]),
        ("Current streak", f'{summary["current_streak"]}d'),
        ("Longest streak", f'{summary["longest_streak"]}d'),
        ("Active days", summary["active_days"]),
        ("Commits", summary["commit_contributions"]),
        ("Pull requests", summary["pull_requests"]),
        ("Reviews", summary["reviews"]),
        ("Repos touched", summary["accessible_repos"]),
    ]
    card_svg = []
    for index, (label, value) in enumerate(cards):
        row = index // 4
        column = index % 4
        x = 28 + column * 191
        y = 86 + row * 92
        card_svg.append(
            f"""
  <g>
    <rect x="{x}" y="{y}" width="170" height="82" rx="16" fill="#111827" stroke="#334155"/>
    <text x="{x + 18}" y="{y + 34}" fill="#94a3b8" font-size="13">{text(label)}</text>
    <text x="{x + 18}" y="{y + 62}" fill="#f8fafc" font-size="28" font-weight="700">{text(value)}</text>
  </g>"""
        )

    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="820" height="368" viewBox="0 0 820 368" role="img" aria-labelledby="title desc">
  <title id="title">Aggregate contribution stats for {text(username)}</title>
  <desc id="desc">Aggregate contribution and repository activity. Repository names and URLs are not included.</desc>
  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0%" stop-color="#020617"/>
      <stop offset="52%" stop-color="#111827"/>
      <stop offset="100%" stop-color="#312e81"/>
    </linearGradient>
    <linearGradient id="accent" x1="0" y1="0" x2="1" y2="0">
      <stop offset="0%" stop-color="#0ea5e9"/>
      <stop offset="50%" stop-color="#8b5cf6"/>
      <stop offset="100%" stop-color="#f97316"/>
    </linearGradient>
  </defs>
  <rect width="820" height="368" rx="24" fill="url(#bg)"/>
  <rect x="1" y="1" width="818" height="366" rx="23" fill="none" stroke="#334155"/>
  <rect x="28" y="30" width="186" height="8" rx="4" fill="url(#accent)"/>
  <text x="28" y="66" fill="#f8fafc" font-family="Segoe UI, Inter, Arial, sans-serif" font-size="26" font-weight="700">Contribution Signals</text>
  <text x="28" y="292" fill="#cbd5e1" font-family="Segoe UI, Inter, Arial, sans-serif" font-size="14">Accessible repos: {text(summary["accessible_repos"])} total / {text(summary["private_repos"])} private / {text(summary["public_repos"])} public / {text(summary["organization_count"])} org workspaces</text>
  <text x="28" y="316" fill="#cbd5e1" font-family="Segoe UI, Inter, Arial, sans-serif" font-size="14">Top stack across accessible repos: {text(summary["top_languages"])}</text>
  <text x="28" y="342" fill="#94a3b8" font-family="Segoe UI, Inter, Arial, sans-serif" font-size="12">Aggregate only. No repository names, URLs, issue titles, or commit messages are published. Latest repo activity: {text(summary["latest_update"])}. Generated: {text(summary["generated_at"])}.</text>
  {''.join(card_svg)}
</svg>
"""


def main() -> None:
    args = parse_args()
    token = os.getenv("PROFILE_STATS_TOKEN") or os.getenv("GH_TOKEN") or os.getenv("GITHUB_TOKEN")
    configured = bool(token)

    repositories = fetch_accessible_repositories(token) if token else []
    contributions = fetch_contribution_calendar(token, args.username) if token else None
    summary = summarize(repositories, contributions)
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(
        render_svg(summary, username=args.username, configured=configured),
        encoding="utf-8",
        newline="\n",
    )


if __name__ == "__main__":
    main()
