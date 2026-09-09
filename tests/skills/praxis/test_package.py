"""Offline package checks and characterization of preserved helpers.

These intentionally capture legacy limitations; they are not production
migration certification. No cloud, Redis, database or Raptor process is run.
Run: python3 -B -m unittest discover -s tests/skills/praxis -p 'test_*.py' -v
"""

import ast
import contextlib
import importlib.util
import io
import json
import os
import re
import subprocess
import sys
import unittest
from pathlib import Path
from unittest.mock import patch

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[3] / "internal/skillinstall/embedded/praxis"


def helper(relative):
    spec = importlib.util.spec_from_file_location(
        "preserved_" + relative.replace("/", "_"), ROOT / "scripts" / relative
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class PackageTests(unittest.TestCase):
    def test_one_entrypoint_and_all_references_directly_routable(self):
        self.assertEqual(list(ROOT.rglob("SKILL.md")), [ROOT / "SKILL.md"])
        body = (ROOT / "SKILL.md").read_text()
        self.assertLessEqual(len(body.splitlines()), 120)
        self.assertEqual(len(list((ROOT / "references").glob("*.md"))), 29)
        for path in (ROOT / "references").glob("*.md"):
            self.assertIn("references/" + path.name, body)

    def test_markdown_local_links_resolve_inside_package(self):
        for path in ROOT.rglob("*.md"):
            for target in re.findall(r"\]\(([^)]+)\)", path.read_text()):
                if "://" in target or target.startswith("#"):
                    continue
                destination = (path.parent / target.split("#", 1)[0]).resolve()
                self.assertTrue(destination.is_relative_to(ROOT), (path, target))
                self.assertTrue(destination.exists(), (path, target))

    def test_assets_are_parseable_and_hidden_provenance_is_preserved(self):
        for path in ROOT.rglob("*.json"):
            json.loads(path.read_text())
        for path in ROOT.rglob("*.py"):
            ast.parse(path.read_text(), filename=str(path))
        self.assertEqual((ROOT / "assets/catalog/.ig-version").read_text(), "v0.2.3\n")
        self.assertFalse(list(ROOT.rglob("*.pyc")))
        self.assertFalse(list(ROOT.rglob("node_modules")))

    def test_draft_generators_execute_with_example_specs_without_external_tools(self):
        for family in ("database", "cache"):
            directory = ROOT / "scripts" / family
            result = subprocess.run(
                [
                    sys.executable,
                    "-B",
                    str(directory / "plan.py"),
                    "--spec",
                    str(directory / "spec.example.json"),
                ],
                env={"PATH": "", "PYTHONDONTWRITEBYTECODE": "1"},
                capture_output=True,
                text=True,
                timeout=10,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("plan:", result.stdout)

    def test_cache_rejects_mutation_without_spawning_process(self):
        monitor = helper("cache/monitor.py")
        with patch.object(monitor.subprocess, "run") as run:
            with self.assertRaisesRegex(SystemExit, "refusing non-readonly"):
                monitor.rcli({}, "FLUSHALL")
            run.assert_not_called()

    def test_cache_auth_is_environment_not_argv(self):
        monitor = helper("cache/monitor.py")
        with (
            patch.dict(
                os.environ, {"TEST_CACHE_AUTH": "synthetic-test-value"}, clear=True
            ),
            patch.object(monitor.subprocess, "run") as run,
        ):
            run.return_value = subprocess.CompletedProcess([], 0, "PONG", "")
            self.assertEqual(
                monitor.rcli(
                    {"host": "fake", "port": 6379, "auth_env": "TEST_CACHE_AUTH"},
                    "PING",
                ),
                "PONG",
            )
            self.assertNotIn("synthetic-test-value", run.call_args.args[0])
            self.assertEqual(
                run.call_args.kwargs["env"]["REDISCLI_AUTH"], "synthetic-test-value"
            )

    def test_legacy_cache_empty_sample_and_ttl_gap_are_real(self):
        monitor = helper("cache/monitor.py")
        spec = {"source": {}, "target": {}}
        with (
            contextlib.redirect_stdout(io.StringIO()),
            patch.object(monitor, "sample_keys", return_value=[]),
        ):
            self.assertTrue(
                monitor.verify(spec, 10), "Characterization: empty set falsely passes"
            )
        with (
            contextlib.redirect_stdout(io.StringIO()),
            patch.object(monitor, "sample_keys", return_value=["session"]),
            patch.object(
                monitor,
                "fingerprint",
                side_effect=[("string", "8", "1000"), ("string", "8", "-1")],
            ),
        ):
            self.assertTrue(
                monitor.verify(spec, 10), "Characterization: TTL is not compared"
            )

    def test_secret_classification_and_short_digest_limit(self):
        replicate = helper("secrets/replicate.py")
        for kind, value, category in [
            ("string", '{"key":"value"}', "ok"),
            ("string", '{"key":{}}', "nonflat"),
            ("string", "[]", "manual"),
            ("binary", "bytes", "manual"),
        ]:
            self.assertEqual(replicate.classify_value(kind, value)[0], category)
        self.assertEqual(len(replicate.digest("synthetic-test-value")), 12)

    def test_secret_version_write_uses_stdin_and_existing_container(self):
        replicate = helper("secrets/replicate.py")
        with patch.object(replicate, "run", return_value=(0, b"", "")) as run:
            replicate.gcp_add_version(
                "fake-project", "fake-secret", "synthetic-test-value"
            )
            argv = run.call_args.args[0]
            self.assertIn("--data-file=-", argv)
            self.assertNotIn("synthetic-test-value", argv)
            self.assertNotIn("create", argv)
            self.assertEqual(
                run.call_args.kwargs["input_bytes"], b"synthetic-test-value"
            )

    def test_catalog_raw_artifact_reader_is_not_effective_env_resolver(self):
        facets = helper("catalog/facets.py")
        self.assertEqual(
            facets.artifact_of(
                {"spec": {"release": {"image": "${blueprint.self.artifacts.api}"}}}
            ),
            "api",
        )
        self.assertEqual(
            facets.artifact_of({"spec": {"release": {"build": {"name": " api "}}}}),
            "api",
        )
        self.assertIsNone(
            facets.artifact_of({"spec": {"custom_artifact_field": "api"}})
        )
        with patch.object(
            facets.subprocess,
            "run",
            return_value=subprocess.CompletedProcess([], 1, "", "denied"),
        ):
            with self.assertRaises(facets.RaptorError):
                facets.raptor_json("projects")


if __name__ == "__main__":
    unittest.main()
