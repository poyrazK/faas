"""test_canonicalise_to_head — three-branch unit tests for the
SHA-stripping reconciler at sdk/python/scripts/gen.py.

The reconciler runs at the end of `regen()` and is the load-bearing
fix for the aggregator's dirty-diff gate (PR #365). It walks
`sdk/python/faas_sdk/*.py`, asks git for each regen file's blob SHA
via `git hash-object --stdin` (which applies git's standard
`blob <len>\0` object header before SHA-1) and compares against
HEAD's blob SHA from `git ls-files -s`. On mismatch, it classifies
the diff as cosmetic (only `import`/`from` lines + blank lines) or
structural (model bodies, route signatures, schema modules) by
stripping import lines + blanks and comparing the two stripped
byte sequences directly. On cosmetic-only divergence, it restores
HEAD's bytes. On structural divergence, it leaves the regen output
intact so `git diff --exit-code` still fires for real schema changes.

These tests build a hermetic git repo in a tmpdir, populate it
with a HEAD commit, then run the reconciler against an isolated
working tree. They cover:

1. Cosmetic-only drift: import-only changes are reconciled.
2. Structural drift: body-line deltas are left intact.
3. Untracked file: loop `continue`s (no SHA comparison, no write).
4. No-diff case: regen bytes equal HEAD bytes, no reconciliation.

The tests do NOT depend on the real sdk/python tree, the fixture,
or the openapi-python-client generator. They exercise only the
reconciler's classification + write behaviour.

NOTE on SHA strategy: `hashlib.sha1(bytes)` does NOT equal a git
blob SHA — git prepends `blob <len>\0` before hashing. Plain
Python `hashlib` cannot reproduce this without writing the header
explicitly. The reconciler uses `git hash-object --stdin` for the
regen side so the SHAs are comparable. The tests therefore build
their fixtures via `git commit` (not via `hashlib`) so HEAD's
`git ls-files -s` SHA and the regen-side `git hash-object` SHA
live in the same naming space.
"""

from __future__ import annotations

import subprocess
from pathlib import Path

import pytest


def _build_hermetic_repo(tmp_path: Path, files: dict[str, str], tracked: list[str]) -> Path:
    """Create a fresh git repo at tmp_path, populate it with the
    supplied files, and commit them. Returns the repo root.

    `files` is a {rel_path: content} map; `tracked` lists the paths
    to add to the index (others are written but untracked).
    """
    repo = tmp_path
    subprocess.run(["git", "init", "--initial-branch=main"], cwd=repo, check=True, capture_output=True)
    subprocess.run(["git", "config", "user.email", "test@example.com"], cwd=repo, check=True, capture_output=True)
    subprocess.run(["git", "config", "user.name", "Test"], cwd=repo, check=True, capture_output=True)
    subprocess.run(["git", "config", "commit.gpgsign", "false"], cwd=repo, check=True, capture_output=True)
    for rel, content in files.items():
        p = repo / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(content)
    for rel in tracked:
        subprocess.run(["git", "add", rel], cwd=repo, check=True, capture_output=True)
    subprocess.run(["git", "commit", "-m", "init"], cwd=repo, check=True, capture_output=True)
    return repo


def _load_canonicalise_to_head():
    """Import `_canonicalise_to_head` from sdk/python/scripts/gen.py.

    Returns the unbound function so tests can call it with arbitrary
    cwd/path arguments without instantiating `regen()`.
    """
    repo_root = Path(__file__).resolve().parent.parent.parent.parent
    script = repo_root / "sdk" / "python" / "scripts" / "gen.py"
    if not script.exists():
        pytest.skip(f"gen.py not present at {script}")
    import importlib.util

    spec = importlib.util.spec_from_file_location("_gen_under_test", script)
    assert spec is not None and spec.loader is not None
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod._canonicalise_to_head


@pytest.mark.parametrize("barrel", ["__init__.py", "preview/__init__.py"])
def test_authored_barrel_exports_are_not_removed_as_cosmetic_drift(tmp_path, barrel):
    relative = f"sdk/python/faas_sdk/{barrel}"
    repo = _build_hermetic_repo(tmp_path, files={relative: "from .old import Existing\n"}, tracked=[relative])
    output = repo / relative
    exported = "from .old import Existing\nfrom .new import RequestOptions\n"
    output.write_text(exported)
    assert _load_canonicalise_to_head()(repo / "sdk/python/faas_sdk", repo) == 0
    assert output.read_text() == exported


def test_cosmetic_only_drift_is_reconciled(tmp_path: Path) -> None:
    """Import-only divergence (e.g. opc 0.29.x template drift that
    reorders imports) is reconciled — the regen file is restored to
    HEAD bytes; the print statement reports reconciled=1,
    structural=0.
    """
    # HEAD file: two imports + one body line.
    head_content = 'import os\nfrom typing import Any\n\ndef main() -> None:\n    print("hello")\n'
    repo = _build_hermetic_repo(
        tmp_path,
        files={"sdk/python/faas_sdk/foo.py": head_content},
        tracked=["sdk/python/faas_sdk/foo.py"],
    )

    # Regen "output" — same body, different import ordering
    # (cosmetic-only divergence).
    regen_content = 'from typing import Any\nimport os\n\ndef main() -> None:\n    print("hello")\n'
    regen_path = repo / "sdk" / "python" / "faas_sdk" / "foo.py"
    regen_path.write_text(regen_content)
    assert regen_content != head_content

    captured_stderr: list[str] = []
    canonicalise = _load_canonicalise_to_head()
    import io
    from contextlib import redirect_stderr

    buf = io.StringIO()
    with redirect_stderr(buf):
        count = canonicalise(
            repo / "sdk" / "python" / "faas_sdk",
            repo,
            sdk_relpath="sdk/python",
        )
    captured_stderr.append(buf.getvalue())

    assert count == 1, f"expected 1 reconciled, got {count}"
    assert "reconciled 1 cosmetic-drift file" in captured_stderr[0]
    assert "0 structural drift left intact" in captured_stderr[0]
    # Regen file was restored to HEAD bytes.
    assert regen_path.read_text() == head_content


def test_structural_drift_is_left_intact(tmp_path: Path) -> None:
    """A real spec change (body-line delta) is left intact — the
    reconciler counts it as structural and does NOT restore HEAD
    bytes. `git diff --exit-code` will fail correctly.
    """
    head_content = 'from typing import Any\n\ndef main() -> str:\n    return "head"\n'
    repo = _build_hermetic_repo(
        tmp_path,
        files={"sdk/python/faas_sdk/foo.py": head_content},
        tracked=["sdk/python/faas_sdk/foo.py"],
    )

    # Regen output: imports unchanged, body has an extra docstring
    # line (a real spec change that the reconciler MUST preserve).
    regen_content = 'from typing import Any\n\ndef main() -> str:\n    """Updated body."""\n    return "regen"\n'
    regen_path = repo / "sdk" / "python" / "faas_sdk" / "foo.py"
    regen_path.write_text(regen_content)

    canonicalise = _load_canonicalise_to_head()
    # The reconciler's diagnostic print is gated on `if reconciled`
    # — when no files were reconciled, nothing is written to stderr.
    # We assert here that count == 0 (no cosmetic files were
    # rewritten as HEAD bytes) and that the regen file is unchanged.
    count = canonicalise(
        repo / "sdk" / "python" / "faas_sdk",
        repo,
        sdk_relpath="sdk/python",
    )

    assert count == 0, f"expected 0 reconciled, got {count}"
    # Regen file is left intact (the diff is structural).
    assert regen_path.read_text() == regen_content


def test_untracked_file_is_skipped(tmp_path: Path) -> None:
    """Files not in the git index (new model files from a real
    schema addition) are skipped — `head_blobs.get(rel)` returns
    None, the loop `continue`s, and the file is untouched.
    """
    # HEAD tree has one tracked non-empty file. The regen output
    # adds a new model file that is not tracked yet — the reconciler
    # sees no entry for it in `git ls-files -s` and `continue`s.
    repo = _build_hermetic_repo(
        tmp_path,
        files={
            "sdk/python/faas_sdk/existing.py": "x = 1\n",
        },
        tracked=[
            "sdk/python/faas_sdk/existing.py",
        ],
    )

    new_file = repo / "sdk" / "python" / "faas_sdk" / "new_model.py"
    new_file.write_text("class NewModel:\n    pass\n")

    canonicalise = _load_canonicalise_to_head()
    import io
    from contextlib import redirect_stderr

    buf = io.StringIO()
    with redirect_stderr(buf):
        count = canonicalise(
            repo / "sdk" / "python" / "faas_sdk",
            repo,
            sdk_relpath="sdk/python",
        )
    stderr_text = buf.getvalue()

    # The tracked `existing.py` is byte-identical to HEAD, so its
    # SHA matches and the loop `continue`s on the third line — no
    # reconciliation, no stderr message (the print is gated on
    # `if reconciled`). The untracked `new_model.py` is also
    # skipped via `head_sha is None`.
    assert count == 0
    assert stderr_text == "", f"unexpected stderr output: {stderr_text!r}"
    # Untracked file is untouched.
    assert new_file.read_text() == "class NewModel:\n    pass\n"


def test_no_diff_when_regen_matches_head(tmp_path: Path) -> None:
    """Sanity: when regen bytes equal HEAD bytes for every tracked
    file, the loop short-circuits on the SHA equality check, no
    reconciliation runs, no stderr message is emitted.
    """
    head_content = "import os\n\nprint('ok')\n"
    repo = _build_hermetic_repo(
        tmp_path,
        files={"sdk/python/faas_sdk/foo.py": head_content},
        tracked=["sdk/python/faas_sdk/foo.py"],
    )

    canonicalise = _load_canonicalise_to_head()
    import io
    from contextlib import redirect_stderr

    buf = io.StringIO()
    with redirect_stderr(buf):
        count = canonicalise(
            repo / "sdk" / "python" / "faas_sdk",
            repo,
            sdk_relpath="sdk/python",
        )
    stderr_text = buf.getvalue()

    assert count == 0
    assert stderr_text == ""
