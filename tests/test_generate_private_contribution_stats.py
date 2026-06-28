"""Deterministic unit tests for the contribution stats generator.

Run with the standard library only (no extra dependencies):

    python -m unittest discover -s tests
"""

from __future__ import annotations

import importlib.util
import unittest
from datetime import datetime
from pathlib import Path
from unittest import mock

_MODULE_PATH = (
    Path(__file__).resolve().parent.parent / "scripts" / "generate_private_contribution_stats.py"
)
_spec = importlib.util.spec_from_file_location("contribution_stats", _MODULE_PATH)
assert _spec is not None and _spec.loader is not None
gen = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(gen)


class ParseLinkHeaderTests(unittest.TestCase):
    def test_extracts_next_link(self) -> None:
        header = (
            '<https://api.github.com/user/repos?page=2>; rel="next", '
            '<https://api.github.com/user/repos?page=5>; rel="last"'
        )
        links = gen.parse_link_header(header)
        self.assertEqual(links["next"], "https://api.github.com/user/repos?page=2")
        self.assertEqual(links["last"], "https://api.github.com/user/repos?page=5")

    def test_none_returns_empty(self) -> None:
        self.assertEqual(gen.parse_link_header(None), {})


class SummarizeTests(unittest.TestCase):
    def _repos(self) -> list[dict]:
        return [
            {
                "language": "Python",
                "private": True,
                "updated_at": "2026-06-01T00:00:00Z",
                "owner": {"login": "org1", "type": "Organization"},
            },
            # PowerShell must be excluded from stack detection.
            {
                "language": "PowerShell",
                "private": False,
                "updated_at": "2026-06-02T00:00:00Z",
                "owner": {"login": "user1", "type": "User"},
            },
            # None language is skipped; owner present-but-null must not crash.
            {"language": None, "private": True, "updated_at": None, "owner": None},
        ]

    def test_excludes_powershell_and_unknown_language(self) -> None:
        summary = gen.summarize(self._repos(), {"total_contributions": 100})
        self.assertEqual(summary["top_stack"], "Python")
        self.assertNotIn("PowerShell", summary["top_languages"])

    def test_aggregate_repo_counts(self) -> None:
        summary = gen.summarize(self._repos(), {"total_contributions": 100})
        self.assertEqual(summary["accessible_repos"], "3")
        self.assertEqual(summary["private_repos"], "2")
        self.assertEqual(summary["public_repos"], "1")
        self.assertEqual(summary["organization_count"], "1")
        self.assertEqual(summary["total_contributions"], "100")

    def test_total_falls_back_to_component_sum(self) -> None:
        activity = {
            "commit_count": 1,
            "authored_pr_count": 2,
            "issue_count": 3,
            "reviewed_pr_count": 4,
        }
        summary = gen.summarize([], activity)
        self.assertEqual(summary["total_contributions"], "10")
        self.assertEqual(summary["top_stack"], "N/A")


class RenderSvgTests(unittest.TestCase):
    def test_unconfigured_shows_setup_placeholder(self) -> None:
        svg = gen.render_svg({}, username="mashfromband", configured=False)
        self.assertIn("Add PROFILE_STATS_TOKEN", svg)
        self.assertIn("Setup", svg)

    def test_escapes_dynamic_values(self) -> None:
        summary = gen.summarize([], {"total_contributions": 5})
        svg = gen.render_svg(summary, username="a<b>&", configured=True)
        self.assertIn("a&lt;b&gt;&amp;", svg)
        self.assertNotIn("<b>", svg)

    def test_no_private_card_label(self) -> None:
        summary = gen.summarize([], {"total_contributions": 5})
        svg = gen.render_svg(summary, username="me", configured=True)
        self.assertNotIn(">Private contributions<", svg)
        self.assertIn(">Repos updated<", svg)


class CollectActivityTests(unittest.TestCase):
    def test_handles_explicit_null_fields(self) -> None:
        payload = {
            "user": {
                "contributionsCollection": {
                    "contributionCalendar": {"totalContributions": None},
                    "totalCommitContributions": None,
                    "totalPullRequestContributions": None,
                    "totalIssueContributions": None,
                    "totalPullRequestReviewContributions": None,
                }
            }
        }
        with mock.patch.object(gen, "request_graphql", return_value=payload):
            result = gen.collect_activity("token", "user", 365)
        self.assertEqual(result["total_contributions"], 0)
        self.assertEqual(result["commit_count"], 0)

    def test_null_collection_does_not_crash(self) -> None:
        payload = {"user": {"contributionsCollection": None}}
        with mock.patch.object(gen, "request_graphql", return_value=payload):
            result = gen.collect_activity("token", "user", 365)
        self.assertEqual(result["total_contributions"], 0)

    def test_missing_user_raises(self) -> None:
        with mock.patch.object(gen, "request_graphql", return_value={"user": None}):
            with self.assertRaises(RuntimeError):
                gen.collect_activity("token", "user", 365)

    def test_days_clamped_to_one_year(self) -> None:
        payload = {
            "user": {"contributionsCollection": {"contributionCalendar": {"totalContributions": 1}}}
        }
        with mock.patch.object(gen, "request_graphql", return_value=payload) as patched:
            gen.collect_activity("token", "user", 800)
        variables = patched.call_args.args[2]
        span = datetime.fromisoformat(variables["to"]) - datetime.fromisoformat(variables["from"])
        self.assertLessEqual(span.days, 366)
        self.assertGreaterEqual(span.days, 364)


if __name__ == "__main__":
    unittest.main()
