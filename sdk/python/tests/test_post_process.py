"""test_post_process — placeholder for the regen post-processor.

PR 6 does NOT introduce a post-processor (the generator's emit is
clean). PR 7 (the `make sdk-gen` aggregator) is the natural place
to add one if the generator ever needs deterministic output.

The tripwire for non-determinism is `make sdk-gen-python-twice`,
which runs in the smoke CI job; `pytest -m 'not regen' tests/` is
the default invocation and excludes the deterministic-regen test
below (it forks the generator twice and is redundant with the
Makefile tripwire). To run it locally:

    cd sdk/python && .venv/bin/python -m pytest -m regen tests/test_post_process.py
"""

from __future__ import annotations

from collections.abc import Iterator

import pytest

#: Pytest marker for the deterministic-regen tripwire. Run with
#: `pytest -m regen` to opt in; the default `pytest -m 'not regen'`
#: excludes it (see pyproject.toml::markers).
REGEN_MARK = pytest.mark.regen


@pytest.fixture(scope="module")
def _regen_skip_reason() -> Iterator[None]:
    """Skip when the generator or ruamel.yaml is not on PATH."""
    from shutil import which

    if which("openapi-python-client") is None:
        pytest.skip("openapi-python-client not installed; regen tripwire skipped")
    yield


@REGEN_MARK
def test_regen_is_deterministic(_regen_skip_reason: None) -> None:
    """Regenerating twice produces no diff. The Makefile tripwire
    (`make sdk-gen-python-twice`) is the canonical assertion in CI;
    this pytest is the local dev path for operators who run
    `pytest -m regen`.
    """
    import subprocess
    import sys
    from pathlib import Path

    repo_root = Path(__file__).resolve().parent.parent.parent.parent
    sdk_root = repo_root / "sdk" / "python"
    script = sdk_root / "scripts" / "gen.py"
    if not script.exists():
        pytest.skip("gen.py not present")

    env_args = {"cwd": str(sdk_root)}
    subprocess.run([sys.executable, str(script)], check=True, **env_args)
    first = subprocess.run(
        ["git", "ls-files", "-s", "faas_sdk/"],
        check=True,
        capture_output=True,
        text=True,
        **env_args,
    )
    subprocess.run([sys.executable, str(script)], check=True, **env_args)
    second = subprocess.run(
        ["git", "ls-files", "-s", "faas_sdk/"],
        check=True,
        capture_output=True,
        text=True,
        **env_args,
    )
    assert first.stdout == second.stdout, "regen is non-deterministic: the second regen changed tracked files"
