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
                "ManagedPostgresCutoverPreflight": {
                    "type": "object",
                    "properties": {"activation_allowed": {"type": "boolean", "enum": [False]}},
                },
                "UpdateTCPListenerRequest": {
                    "type": "object",
                    "additionalProperties": False,
                    "properties": {"enabled": {"type": "boolean"}, "tls": {"type": "object"}},
                    "oneOf": [{"required": ["enabled"]}, {"required": ["tls"]}],
                },
            }
        }
    }
    source.write_text(json.dumps(shape))
    before = source.read_bytes()
    normalized = generator.pre_normalize_spec(source)
    try:
        schemas = json.loads(normalized.read_text())["components"]["schemas"]
        assert schemas["ManagedPostgresCutoverPreflight"]["properties"]["activation_allowed"] == {"type": "boolean"}
        request = schemas["UpdateTCPListenerRequest"]
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
def test_regen_is_deterministic(_regen_skip_reason: None, tmp_path: Path) -> None:
    """Regenerating twice produces no diff. The Makefile tripwire
    (`make sdk-gen-python-twice`) is the canonical assertion in CI;
    this pytest is the local dev path for operators who run
    `pytest -m regen`.
    """
    import os
    import shutil
    import subprocess
    import sys
    from pathlib import Path

    repo_root = Path(__file__).resolve().parent.parent.parent.parent
    sdk_root = repo_root / "sdk" / "python"
    script = sdk_root / "scripts" / "gen.py"
    if not script.exists():
        pytest.skip("gen.py not present")

    # Never delete/reformat the package currently imported by this test runner
    # or by its child workloads. Preserve the same Git cosmetic-drift baseline
    # in an owned local clone, then copy current (including uncommitted) sources.
    fixture = tmp_path / "repo"
    subprocess.run(["git", "clone", "--quiet", "--shared", "--no-checkout", str(repo_root), str(fixture)], check=True)
    subprocess.run(["git", "-C", str(fixture), "checkout", "HEAD", "--", "sdk/python", "api/openapi.yaml"], check=True)
    shutil.copy2(repo_root / "sdk" / "openapi.yaml", fixture / "sdk" / "openapi.yaml")
    copied_sdk = fixture / "sdk" / "python"
    shutil.copytree(
        sdk_root,
        copied_sdk,
        dirs_exist_ok=True,
        ignore=shutil.ignore_patterns(".venv", "__pycache__", "tests", ".pytest_cache", ".ruff_cache"),
    )
    helpers = {
        name: (copied_sdk / "faas_sdk" / name).read_bytes()
        for name in (
            "preview/object_storage_runtime.py",
            "secret_reload_runtime.py",
            "_binding_client_runtime.py",
            "preview/postgres_pool_runtime.py",
            "preview/async_postgres_pool_runtime.py",
            "async_secret_reload_runtime.py",
            "_async_binding_client_runtime.py",
            "preview/async_object_storage_runtime.py",
        )
    }
    script = copied_sdk / "scripts" / "gen.py"
    env_args = {"cwd": str(copied_sdk), "env": {**os.environ, "PYTHONDONTWRITEBYTECODE": "1"}}
    subprocess.run([sys.executable, str(script)], check=True, **env_args)
    first = generated_snapshot(copied_sdk / "faas_sdk")
    subprocess.run([sys.executable, str(script)], check=True, **env_args)
    second = generated_snapshot(copied_sdk / "faas_sdk")
    assert first == second, "regen is non-deterministic: the second regen changed generated files"
    assert {name: (copied_sdk / "faas_sdk" / name).read_bytes() for name in helpers} == helpers


def _fixture_generator(tmp_path, monkeypatch):
    script = Path(__file__).resolve().parents[1] / "scripts/gen.py"
    spec = importlib.util.spec_from_file_location("sdk_generator_preservation", script)
    generator = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(generator)
    output = tmp_path / "sdk"
    package = output / "faas_sdk"
    package.mkdir(parents=True)
    originals = {
        "faas_sdk/_wrapper.py": b"# existing custom wrapper\n",
        "faas_sdk/preview/_binding_runner.py": b"# uncommitted verification runner\n",
        "faas_sdk/preview/_binding_runner_diagnostics.py": b"# uncommitted automatic diagnostics\n",
        "faas_sdk/preview/_binding_creation.py": b"# uncommitted reviewed creation\n",
        "faas_sdk/preview/_binding_qualification.py": b"# uncommitted qualification waiting\n",
        "faas_sdk/preview/operations_runtime.py": b"# uncommitted runtime authority\n",
        "faas_sdk/secret_reload_runtime.py": b"# uncommitted secret reload authority\n",
        "faas_sdk/_binding_client_runtime.py": b"# shared owned-resource lifecycle\n",
        "faas_sdk/preview/postgres_pool_runtime.py": b"# PostgreSQL pool adoption\n",
        "faas_sdk/preview/async_postgres_pool_runtime.py": b"# async PostgreSQL pool adoption\n",
        "faas_sdk/_async_binding_client_runtime.py": b"# async owned resource lifecycle\n",
        "faas_sdk/preview/async_object_storage_runtime.py": b"# async object storage adoption\n",
        "faas_sdk/async_secret_reload_runtime.py": b"# async secret adoption\n",
        "faas_sdk/preview/operations.py": b"# managed operation transaction helper\n",
        "faas_sdk/preview/_operation_contract.py": b"# managed operation wire contract\n",
        "faas_sdk/preview/operation_schema.sql": b"-- managed operation receipt schema\n",
        "pyproject.toml": b"# curated project configuration\n",
        "README.md": b"curated SDK documentation\n",
    }
    for relative, body in originals.items():
        (output / relative).parent.mkdir(parents=True, exist_ok=True)
        (output / relative).write_bytes(body)
    (package / "obsolete_generated.py").write_text("# obsolete generated model\n")
    source, config = tmp_path / "spec.json", tmp_path / "config.yaml"
    source.write_text('{"components": {"schemas": {}}}')
    config.write_text("project_name_override: faas_sdk\n")
    monkeypatch.setattr(generator, "OUT", output)
    monkeypatch.setattr(generator, "SPEC", source)
    monkeypatch.setattr(generator, "PUBLIC_SPEC", source)
    monkeypatch.setattr(generator, "CONFIG", config)
    return generator, output, originals


@pytest.mark.parametrize("failure", ["normalization", "generator", "partial_cleanup"])
def test_failed_regeneration_preserves_handwritten_source(tmp_path, monkeypatch, failure):
    """ADR-521: regeneration must retain the uncommitted Operations runtime."""
    import subprocess

    generator, output, originals = _fixture_generator(tmp_path, monkeypatch)
    if failure == "normalization":

        def fail_normalization(_):
            raise ValueError("invalid spec")

        monkeypatch.setattr(generator, "pre_normalize_spec", fail_normalization)
        expected = ValueError
    elif failure == "generator":
        normalized = tmp_path / "normalized.json"
        normalized.write_text("{}")
        monkeypatch.setattr(generator, "pre_normalize_spec", lambda _: normalized)

        def failed_process(*args, **kwargs):
            (output / "pyproject.toml").write_text("# generator replacement\n")
            return subprocess.CompletedProcess(args, 1, "generator failed\n", "")

        monkeypatch.setattr(generator.subprocess, "run", failed_process)
        expected = SystemExit
    else:
        original_rmtree = generator.shutil.rmtree

        def partial_cleanup(path, *args, **kwargs):
            if path == output / "faas_sdk":
                (path / "preview" / "operations_runtime.py").unlink()
                raise OSError("cleanup interrupted")
            return original_rmtree(path, *args, **kwargs)

        monkeypatch.setattr(generator.shutil, "rmtree", partial_cleanup)
        expected = OSError
    with pytest.raises(expected):
        generator.regen()
    assert {name: (output / name).read_bytes() for name in originals} == originals


def test_backup_failure_leaves_original_tree_untouched(tmp_path, monkeypatch):
    """ADR-521: never begin deletion after an incomplete source backup."""
    generator, output, originals = _fixture_generator(tmp_path, monkeypatch)
    original_copy = generator.shutil.copy2

    def interrupted_copy(source, destination, *args, **kwargs):
        if source == output / "faas_sdk" / "preview" / "operations_runtime.py":
            raise OSError("backup unavailable")
        return original_copy(source, destination, *args, **kwargs)

    monkeypatch.setattr(generator.shutil, "copy2", interrupted_copy)
    with pytest.raises(OSError, match="backup unavailable"):
        generator.regen()
    assert {name: (output / name).read_bytes() for name in originals} == originals
    assert (output / "faas_sdk" / "obsolete_generated.py").exists()


def test_postprocessing_cannot_modify_preserved_handwritten_source(tmp_path, monkeypatch):
    """ADR-521: generator formatting must not rewrite runtime authority helpers."""
    generator, output, originals = _fixture_generator(tmp_path, monkeypatch)
    with generator._preserve_handwritten_sdk(output) as restore:
        generator.shutil.rmtree(output / "faas_sdk")
        restore()
        (output / "faas_sdk" / "preview" / "operations_runtime.py").write_text("# modified by formatter\n")
        (output / "pyproject.toml").write_text("# overwritten metadata\n")
    assert {name: (output / name).read_bytes() for name in originals} == originals


def test_postprocessing_preserves_authored_test_bytes(tmp_path, monkeypatch):
    """ADR-1623: SDK regeneration retains current handwritten test inputs."""
    generator, output, _originals = _fixture_generator(tmp_path, monkeypatch)
    authored = output / "tests" / "nested" / "test_local_edit.py"
    authored.parent.mkdir(parents=True)
    original = b"# uncommitted test input\nvalue= 'original'\n"
    authored.write_bytes(original)
    with generator._preserve_handwritten_sdk(output):
        authored.write_text("# rewritten by import fixer/formatter\n")
    assert authored.read_bytes() == original


def test_restore_failure_retains_recoverable_source_backup(tmp_path, monkeypatch):
    """ADR-521: interrupted restoration must retain the original runtime bytes."""
    generator, output, originals = _fixture_generator(tmp_path, monkeypatch)
    stash = tmp_path / "preserved-backup"
    stash.mkdir()
    monkeypatch.setattr(generator.tempfile, "mkdtemp", lambda **_: str(stash))
    original_copy = generator.shutil.copy2

    def failed_restore(source, destination, *args, **kwargs):
        if source == stash / "faas_sdk" / "preview" / "operations_runtime.py":
            raise OSError("destination unavailable")
        return original_copy(source, destination, *args, **kwargs)

    monkeypatch.setattr(generator.shutil, "copy2", failed_restore)
    with pytest.raises(RuntimeError, match="source backup retained") as error:
        with generator._preserve_handwritten_sdk(output):
            generator.shutil.rmtree(output / "faas_sdk")
    assert str(stash) in str(error.value)
    assert {name: (stash / name).read_bytes() for name in originals} == originals


def test_regenerated_barrel_exports_binding_runner(tmp_path):
    import importlib.util

    script = Path(__file__).resolve().parents[1] / "scripts" / "gen.py"
    spec = importlib.util.spec_from_file_location("sdk_generator_runner_exports", script)
    generator = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(generator)
    # ADR-20261023115741: binding verification runs are a preview capability.
    assert "from ._binding_runner import" in generator._PREVIEW_INIT_PY_TEMPLATE
    assert '"BindingVerificationRecoveryError"' in generator._PREVIEW_INIT_PY_TEMPLATE
    barrel = tmp_path / "__init__.py"
    generator._rewrite_init_py(barrel)
    assert "_binding_runner" not in barrel.read_text()
