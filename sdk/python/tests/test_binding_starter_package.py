"""Canonical drift and isolated application packaging for Python starters."""

import importlib.util
import shutil
import subprocess
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]


def test_python_starter_runtime_matches_canonical_sources():
    subprocess.run([sys.executable, str(ROOT / "sdk/python/scripts/sync_binding_starters.py"), "--check"], check=True)


@pytest.mark.parametrize(
    ("template", "drivers", "pattern"),
    [
        ("rest-api-postgres-python", ("psycopg", "psycopg_pool"), "test_database.py"),
        ("object-storage-python", ("aiobotocore", "botocore"), "test_storage.py"),
    ],
)
def test_python_starter_runs_without_repository_sdk_or_relative_dependencies(tmp_path, template, drivers, pattern):
    if any(importlib.util.find_spec(module) is None for module in ("fastapi", "uvicorn", *drivers)):
        pytest.skip("install the Python starter requirements to run isolated application tests")
    source = ROOT / "cmd/gregale/templates" / template
    destination = tmp_path / "application"
    shutil.copytree(source, destination, ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
    requirements = (destination / "requirements.txt").read_text()
    assert "-e " not in requirements and "../" not in requirements
    subprocess.run(
        [sys.executable, "-I", "-m", "unittest", "discover", "-s", "tests", "-p", pattern],
        cwd=destination,
        check=True,
        timeout=30,
    )
