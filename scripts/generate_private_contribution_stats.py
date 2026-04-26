#!/usr/bin/env python3
"""Generate aggregate-only activity stats for the profile README.

The generated SVG intentionally avoids repository names, repository URLs, commit
messages, issue titles, and organization names. It is safe to publish because it
only contains aggregate counts derived from repository activity metadata the token
can read.
"""

from __future__ import annotations

import argparse
import html
import json
import os
import socket
from collections import Counter
from datetime import UTC, datetime, timedelta
from http.client import IncompleteRead
from pathlib import Path
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import Request, urlopen


API_ROOT = "https://api.github.com"
REQUEST_TIMEOUT_SECONDS = 20
REQUEST_RETRIES = 1
SEARCH_DATE_SAMPLE_PAGES = 5


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Generate a public-safe SVG from aggregate GitHub activity data."
    )
    parser.add_argument("--output", default="assets/private-contributions.svg")
    parser.add_argument("--username", default=os.getenv("GITHUB_ACTOR", "mashfromband"))
    parser.add_argument("--days", type=int, default=365)
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
    for attempt in range(1, REQUEST_RETRIES + 1):
        try:
            with urlopen(request, timeout=REQUEST_TIMEOUT_SECONDS) as response:
                payload = json.loads(response.read().decode("utf-8"))
                headers = {key.lower(): value for key, value in response.headers.items()}
                return payload, headers
        except HTTPError as error:
            detail = error.read().decode("utf-8", errors="replace")
            raise RuntimeError(f"GitHub API request failed: {error.code} {detail}") from error
        except (IncompleteRead, TimeoutError, socket.timeout, URLError) as error:
            if attempt == REQUEST_RETRIES:
                raise RuntimeError(f"GitHub API request timed out after retries: {url}") from error

    raise RuntimeError(f"GitHub API request failed unexpectedly: {url}")


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


def fetch_commit_activity(
    repositories: list[dict[str, Any]], token: str, username: str, since: datetime
) -> tuple[int, Counter[str]]:
    _ = repositories
    since_date = since.date().isoformat()
    query = f"author:{username} committer-date:>={since_date}"
    params = urlencode(
        {
            "q": query,
            "per_page": "100",
            "sort": "committer-date",
            "order": "desc",
        }
    )
    url = f"{API_ROOT}/search/commits?{params}"
    activity_dates: Counter[str] = Counter()

    total = 0
    sampled_pages = 0
    while url and sampled_pages < SEARCH_DATE_SAMPLE_PAGES:
        sampled_pages += 1
        try:
            payload, headers = request_json(url, token)
        except RuntimeError as error:
            if "timed out" in str(error):
                break
            raise
        if not isinstance(payload, dict) or not isinstance(payload.get("items"), list):
            raise RuntimeError("Unexpected GitHub API response for search/commits")

        total = max(total, int(payload.get("total_count", 0)))
        for item in payload["items"]:
            commit_date = (
                item.get("commit", {}).get("author", {}).get("date")
                or item.get("commit", {}).get("committer", {}).get("date")
            )
            parsed = parse_github_datetime(commit_date)
            if parsed:
                activity_dates[parsed.date().isoformat()] += 1

        url = parse_link_header(headers.get("link")).get("next", "")

    return total, activity_dates


def fetch_search_activity(
    token: str, query: str, date_field: str
) -> tuple[int, Counter[str]]:
    params = urlencode({"q": query, "per_page": "100", "sort": "created", "order": "desc"})
    url = f"{API_ROOT}/search/issues?{params}"
    total = 0
    activity_dates: Counter[str] = Counter()

    sampled_pages = 0
    while url and sampled_pages < SEARCH_DATE_SAMPLE_PAGES:
        sampled_pages += 1
        try:
            payload, headers = request_json(url, token)
        except RuntimeError as error:
            if "timed out" in str(error):
                break
            raise
        if not isinstance(payload, dict) or not isinstance(payload.get("items"), list):
            raise RuntimeError("Unexpected GitHub API response for search/issues")

        total = max(total, int(payload.get("total_count", 0)))
        for item in payload["items"]:
            parsed = parse_github_datetime(item.get(date_field))
            if parsed:
                activity_dates[parsed.date().isoformat()] += 1

        url = parse_link_header(headers.get("link")).get("next", "")

    return total, activity_dates


def parse_github_datetime(value: str | None) -> datetime | None:
    if not value:
        return None
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def calculate_streaks(activity_dates: Counter[str], since: datetime, today: datetime) -> dict[str, int]:
    parsed_days = []
    current_day = since.date()
    end_day = today.date()
    while current_day <= end_day:
        parsed_days.append((current_day, activity_dates[current_day.isoformat()]))
        current_day += timedelta(days=1)

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


def collect_activity(
    repositories: list[dict[str, Any]], token: str, username: str, days: int
) -> dict[str, Any]:
    now = datetime.now(UTC)
    since = now - timedelta(days=days)
    since_date = since.date().isoformat()

    commit_count, activity_dates = fetch_commit_activity(repositories, token, username, since)

    authored_pr_count, pr_dates = fetch_search_activity(
        token, f"author:{username} type:pr created:>={since_date}", "created_at"
    )
    issue_count, issue_dates = fetch_search_activity(
        token, f"author:{username} type:issue created:>={since_date}", "created_at"
    )
    reviewed_pr_count, review_dates = fetch_search_activity(
        token, f"reviewed-by:{username} type:pr updated:>={since_date}", "updated_at"
    )

    activity_dates.update(pr_dates)
    activity_dates.update(issue_dates)
    activity_dates.update(review_dates)

    return {
        "commit_count": commit_count,
        "authored_pr_count": authored_pr_count,
        "issue_count": issue_count,
        "reviewed_pr_count": reviewed_pr_count,
        "activity_dates": activity_dates,
        "streaks": calculate_streaks(activity_dates, since, now),
    }


def summarize(
    repositories: list[dict[str, Any]], activity: dict[str, Any] | None
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

    activity = activity or {}
    streaks = activity.get("streaks") or {"current": 0, "longest": 0, "active_days": 0}
    commit_count = int(activity.get("commit_count", 0))
    authored_pr_count = int(activity.get("authored_pr_count", 0))
    issue_count = int(activity.get("issue_count", 0))
    reviewed_pr_count = int(activity.get("reviewed_pr_count", 0))
    total_activity = commit_count + authored_pr_count + issue_count + reviewed_pr_count

    return {
        "total_contributions": str(total_activity),
        "current_streak": str(streaks["current"]),
        "longest_streak": str(streaks["longest"]),
        "active_days": str(streaks["active_days"]),
        "commit_contributions": str(commit_count),
        "pull_requests": str(authored_pr_count),
        "issues": str(issue_count),
        "reviews": str(reviewed_pr_count),
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
            "issues": "Needed",
            "reviews": "Needed",
            "accessible_repos": "Setup",
            "private_repos": "Setup",
            "public_repos": "Setup",
            "active_90": "Needed",
            "active_365": "Needed",
            "organization_count": "Needed",
            "top_languages": "Add PROFILE_STATS_TOKEN to generate aggregate activity signals",
            "latest_update": "N/A",
            "generated_at": datetime.now(UTC).strftime("%Y-%m-%d"),
        }

    cards = [
        ("Total activity", summary["total_contributions"]),
        ("Current streak", f'{summary["current_streak"]}d'),
        ("Longest streak", f'{summary["longest_streak"]}d'),
        ("Active days", summary["active_days"]),
        ("Commits", summary["commit_contributions"]),
        ("Pull requests", summary["pull_requests"]),
        ("Issues", summary["issues"]),
        ("Reviewed PRs", summary["reviews"]),
        ("Repos touched", summary["active_365"]),
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

    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="820" height="460" viewBox="0 0 820 460" role="img" aria-labelledby="title desc">
  <title id="title">Aggregate activity stats for {text(username)}</title>
  <desc id="desc">Aggregate activity across accessible repositories. Repository names and URLs are not included.</desc>
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
  <rect width="820" height="460" rx="24" fill="url(#bg)"/>
  <rect x="1" y="1" width="818" height="458" rx="23" fill="none" stroke="#334155"/>
  <rect x="28" y="30" width="186" height="8" rx="4" fill="url(#accent)"/>
  <text x="28" y="66" fill="#f8fafc" font-family="Segoe UI, Inter, Arial, sans-serif" font-size="26" font-weight="700">All Activity Signals</text>
  <text x="28" y="386" fill="#cbd5e1" font-family="Segoe UI, Inter, Arial, sans-serif" font-size="14">Accessible repos: {text(summary["accessible_repos"])} total / {text(summary["private_repos"])} private / {text(summary["public_repos"])} public / {text(summary["organization_count"])} org workspaces</text>
  <text x="28" y="410" fill="#cbd5e1" font-family="Segoe UI, Inter, Arial, sans-serif" font-size="14">Top stack across accessible repos: {text(summary["top_languages"])}</text>
  <text x="28" y="436" fill="#94a3b8" font-family="Segoe UI, Inter, Arial, sans-serif" font-size="12">Aggregate only. No repository names, URLs, issue titles, or commit messages are published. Latest repo activity: {text(summary["latest_update"])}. Generated: {text(summary["generated_at"])}.</text>
  {''.join(card_svg)}
</svg>
"""


def main() -> None:
    args = parse_args()
    token = os.getenv("PROFILE_STATS_TOKEN") or os.getenv("GH_TOKEN") or os.getenv("GITHUB_TOKEN")
    configured = bool(token)

    repositories = fetch_accessible_repositories(token) if token else []
    activity = collect_activity(repositories, token, args.username, args.days) if token else None
    summary = summarize(repositories, activity)
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(
        render_svg(summary, username=args.username, configured=configured),
        encoding="utf-8",
        newline="\n",
    )


if __name__ == "__main__":
    main()
