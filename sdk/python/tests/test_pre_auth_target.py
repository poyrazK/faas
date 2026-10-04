import importlib.util
from pathlib import Path
from types import SimpleNamespace

import pytest

from faas_sdk import PRE_AUTH_TARGET_HEADER, pre_auth_target_digest


def test_target_digest_matches_gateway_format_and_node_vectors():
    assert PRE_AUTH_TARGET_HEADER == "X-Gregale-Abuse-Target"
    assert pre_auth_target_digest(bytes([0x0B]) * 32, "Hi There") == (
        "198a607eb44bfbc69903a0f1cf2bbdc5ba0aa3f3d9ae3c1c7a3b1696a0b68cf7"
    )
    assert pre_auth_target_digest("a" * 32, "é@example.com") == (
        "b0759e2dbe2de6f20fca64ac31d0dcd0828401f3d62ae4146267607dd77cb52a"
    )


def test_caller_controls_normalization_and_errors_do_not_reveal_inputs():
    secret = "private-app-owned-secret-that-is-long-enough"
    assert pre_auth_target_digest(secret, "user@example.com") != pre_auth_target_digest(secret, "User@example.com")
    assert pre_auth_target_digest(secret, "user@example.com") != pre_auth_target_digest(secret, " user@example.com ")
    with pytest.raises(ValueError, match="at least 32 bytes") as exc:
        pre_auth_target_digest("short", "user@example.com")
    assert "short" not in str(exc.value)
    assert "user@example.com" not in str(exc.value)
    with pytest.raises(TypeError, match="nonempty string"):
        pre_auth_target_digest(secret, "")
    with pytest.raises(ValueError, match="valid Unicode"):
        pre_auth_target_digest(secret, "\ud800")


def test_regen_preserves_pre_auth_target_wrapper(monkeypatch, tmp_path):
    script = Path(__file__).resolve().parents[1] / "scripts" / "gen.py"
    spec = importlib.util.spec_from_file_location("faas_sdk_gen_target_test", script)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    sdk_root = tmp_path / "sdk" / "python"
    package = sdk_root / "faas_sdk"
    package.mkdir(parents=True)
    helper = package / "pre_auth_target.py"
    helper.write_text("sentinel = 'hand-written helper'\n")
    webhook = package / "webhook.py"
    webhook.write_text("sentinel = 'hand-written webhook helper'\n")
    source_spec = tmp_path / "openapi.yaml"
    source_spec.write_text("openapi: 3.1.0\n")
    config = tmp_path / "config.yaml"
    config.write_text("project_name_override: faas_sdk\n")
    normalized = tmp_path / "normalized.json"
    normalized.write_text("{}")

    monkeypatch.setattr(module, "REPO_ROOT", tmp_path)
    monkeypatch.setattr(module, "OUT", sdk_root)
    monkeypatch.setattr(module, "SPEC", source_spec)
    monkeypatch.setattr(module, "CONFIG", config)
    monkeypatch.setattr(module, "pre_normalize_spec", lambda _: normalized)
    monkeypatch.setattr(module, "_rewrite_init_py", lambda _: None)
    monkeypatch.setattr(module, "_patch_generator_bugs", lambda _: None)
    monkeypatch.setattr(module, "_canonicalise_to_head", lambda *args, **kwargs: None)

    def generate(command, **kwargs):
        if "openapi_python_client" in command:
            (package / "__init__.py").write_text("# generated\n")
            models = package / "models"
            models.mkdir()
            (models / "__init__.py").write_text(
                "from .create_commit_source_request import CreateCommitSourceRequest\n"
                '__all__ = ("CreateCommitSourceRequest",)\n'
            )
            (models / "create_commit_source_request.py").write_text("class CreateCommitSourceRequest: ...\n")
        return SimpleNamespace(returncode=0, stdout="", stderr="")

    monkeypatch.setattr(module.subprocess, "run", generate)
    module.regen()

    assert helper.read_text() == "sentinel = 'hand-written helper'\n"
    assert webhook.read_text() == "sentinel = 'hand-written webhook helper'\n"
    assert (package / "__init__.py").exists()
