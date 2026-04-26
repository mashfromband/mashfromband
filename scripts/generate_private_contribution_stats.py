#!/usr/bin/env python3
"""Generate aggregate-only private contribution stats for the profile README.

The generated SVG intentionally avoids repository names, repository URLs, commit
messages, issue titles, and organization names. It is safe to publish because it
only contains aggregate counts derived from repositories the token can read.
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


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Generate a public-safe SVG from private GitHub repository aggregates."
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


def fetch_private_repositories(token: str) -> list[dict[str, Any]]:
    params = urlencode(
        {
            "visibility": "private",
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
        repositories.extend(repo for repo in payload if repo.get("private") is True)
        url = parse_link_header(headers.get("link")).get("next", "")

    return repositories


def parse_github_datetime(value: str | None) -> datetime | None:
    if not value:
        return None
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def summarize(repositories: list[dict[str, Any]]) -> dict[str, str]:
    now = datetime.now(UTC)
    updated_dates = [
        parsed
        for parsed in (parse_github_datetime(repo.get("updated_at")) for repo in repositories)
        if parsed is not None
    ]
    language_counts = Counter(repo.get("language") or "Other" for repo in repositories)
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

    return {
        "private_repos": str(len(repositories)),
        "active_90": str(active_90),
        "active_365": str(active_365),
        "organization_count": str(organization_count),
        "top_languages": top_languages or "No private languages detected",
        "latest_update": max(updated_dates).strftime("%Y-%m-%d") if updated_dates else "N/A",
        "generated_at": now.strftime("%Y-%m-%d"),
    }


def text(value: str) -> str:
    return html.escape(value, quote=True)


def render_svg(summary: dict[str, str], *, username: str, configured: bool) -> str:
    if not configured:
        summary = {
            "private_repos": "Setup",
            "active_90": "Needed",
            "active_365": "Needed",
            "organization_count": "Needed",
            "top_languages": "Add PROFILE_STATS_TOKEN to generate private contribution aggregates",
            "latest_update": "N/A",
            "generated_at": datetime.now(UTC).strftime("%Y-%m-%d"),
        }

    cards = [
        ("Private repos", summary["private_repos"]),
        ("Active in 90 days", summary["active_90"]),
        ("Active in 365 days", summary["active_365"]),
        ("Org workspaces", summary["organization_count"]),
    ]
    card_svg = []
    for index, (label, value) in enumerate(cards):
        x = 28 + index * 191
        card_svg.append(
            f"""
  <g>
    <rect x="{x}" y="86" width="170" height="82" rx="16" fill="#111827" stroke="#334155"/>
    <text x="{x + 18}" y="120" fill="#94a3b8" font-size="13">{text(label)}</text>
    <text x="{x + 18}" y="148" fill="#f8fafc" font-size="28" font-weight="700">{text(value)}</text>
  </g>"""
        )

    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="820" height="260" viewBox="0 0 820 260" role="img" aria-labelledby="title desc">
  <title id="title">Private contribution aggregate stats for {text(username)}</title>
  <desc id="desc">Aggregate-only private repository activity. Repository names and URLs are not included.</desc>
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
  <rect width="820" height="260" rx="24" fill="url(#bg)"/>
  <rect x="1" y="1" width="818" height="258" rx="23" fill="none" stroke="#334155"/>
  <rect x="28" y="30" width="186" height="8" rx="4" fill="url(#accent)"/>
  <text x="28" y="66" fill="#f8fafc" font-family="Segoe UI, Inter, Arial, sans-serif" font-size="26" font-weight="700">Private Work Signals</text>
  <text x="28" y="212" fill="#cbd5e1" font-family="Segoe UI, Inter, Arial, sans-serif" font-size="14">Top private stack: {text(summary["top_languages"])}</text>
  <text x="28" y="236" fill="#94a3b8" font-family="Segoe UI, Inter, Arial, sans-serif" font-size="12">Aggregate only. No private repository names, URLs, issue titles, or commit messages are published. Latest private activity: {text(summary["latest_update"])}. Generated: {text(summary["generated_at"])}.</text>
  {''.join(card_svg)}
</svg>
"""


def main() -> None:
    args = parse_args()
    token = os.getenv("PROFILE_STATS_TOKEN") or os.getenv("GH_TOKEN") or os.getenv("GITHUB_TOKEN")
    configured = bool(token)

    repositories = fetch_private_repositories(token) if token else []
    summary = summarize(repositories)
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(
        render_svg(summary, username=args.username, configured=configured),
        encoding="utf-8",
        newline="\n",
    )


if __name__ == "__main__":
    main()
