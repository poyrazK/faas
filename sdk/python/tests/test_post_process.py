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

import hashlib
import importlib.util
import json
from collections.abc import Iterator
from pathlib import Path

import pytest

#: Pytest marker for the deterministic-regen tripwire. Run with
#: `pytest -m regen` to opt in; the default `pytest -m 'not regen'`
#: excludes it (see pyproject.toml::markers).
REGEN_MARK = pytest.mark.regen


def generated_snapshot(root: Path) -> dict[str, str]:
    return {str(path.relative_to(root)): hashlib.sha256(path.read_bytes()).hexdigest() for path in root.rglob("*.py")}


def test_snapshot_detects_modified_and_new_generated_files(tmp_path):
    source = tmp_path / "client.py"
    source.write_text("first")
    first = generated_snapshot(tmp_path)
    source.write_text("second")
    assert generated_snapshot(tmp_path) != first
    second = generated_snapshot(tmp_path)
    (tmp_path / "new_model.py").write_text("model")
    assert generated_snapshot(tmp_path) != second


def test_tcp_patch_normalization_preserves_authoritative_spec(tmp_path):
    script = Path(__file__).resolve().parents[1] / "scripts/gen.py"
    spec = importlib.util.spec_from_file_location("sdk_generator", script)
    generator = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(generator)
    source = tmp_path / "spec.json"
    shape = {
        "components": {
            "schemas": {
                "UpdateTCPListenerRequest": {
                    "type": "object",
                    "additionalProperties": False,
                    "properties": {"enabled": {"type": "boolean"}, "tls": {"type": "object"}},
                    "oneOf": [{"required": ["enabled"]}, {"required": ["tls"]}],
                }
            }
        }
    }
    source.write_text(json.dumps(shape))
    before = source.read_bytes()
    normalized = generator.pre_normalize_spec(source)
    try:
        request = json.loads(normalized.read_text())["components"]["schemas"]["UpdateTCPListenerRequest"]
        assert "oneOf" not in request
        assert request["properties"] == shape["components"]["schemas"]["UpdateTCPListenerRequest"]["properties"]
        assert request["additionalProperties"] is False
        assert source.read_bytes() == before
    finally:
        normalized.unlink()
    shape["components"]["schemas"]["UpdateTCPListenerRequest"]["oneOf"] = [{"required": ["different"]}]
    source.write_text(json.dumps(shape))
    with pytest.raises(ValueError, match="adaptation needs review"):
        generator.pre_normalize_spec(source)


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
    first = generated_snapshot(sdk_root / "faas_sdk")
    subprocess.run([sys.executable, str(script)], check=True, **env_args)
    second = generated_snapshot(sdk_root / "faas_sdk")
    assert first == second, "regen is non-deterministic: the second regen changed generated files"
