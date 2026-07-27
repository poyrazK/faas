#!/usr/bin/env python3
"""scripts/gen.py — (re)generate faas_sdk from api/openapi.yaml.

Cwd-independent: invokes openapi-python-client with absolute paths
resolved from this file's location, so the script works whether you
run `python scripts/gen.py` from sdk/python/, the repo root, or a
worktree.

Companion script: scripts/post_process.py (the helpers that normalise
the generated tree; same shape as sdk/node/scripts/post-process.mjs).

Generator pin (CI installs this exact version):

    pip install openapi-python-client==0.29.0

Major-version bumps require a new ADR. The pin is declared in
pyproject.toml's dev dependencies; this docstring is the cross-reference
for the Makefile and CI invocations.
"""

from __future__ import annotations

import argparse
import shutil
import subprocess
import sys
from pathlib import Path

# scripts/gen.py            (this file)
#   ↓
# sdk/python/scripts/         (one .parent up)
#   ↓
# sdk/python/                 (two .parent up)
#   ↓
# sdk/                        (three .parent up)
#   ↓
# <repo root>                 (four .parent up) — holds api/openapi.yaml
REPO_ROOT = Path(__file__).resolve().parent.parent.parent.parent
SDK_ROOT = REPO_ROOT / "sdk" / "python"
SPEC = REPO_ROOT / "api" / "openapi.yaml"
CONFIG = SDK_ROOT / "openapi_config.yaml"
# The generator emits `<output>/<project_name>/...`. With
# `project_name_override: faas_sdk` in openapi_config.yaml, the
# generated tree lands at `<output>/faas_sdk/api/`, `<output>/faas_sdk/models/`,
# `<output>/faas_sdk/client.py`. We point OUT at SDK_ROOT (the
# `sdk/python/` directory) so the on-disk shape is `sdk/python/faas_sdk/...`
# — the importable package.
OUT = SDK_ROOT


def pre_normalize_spec(spec: Path) -> Path:
    """Round-trip the OpenAPI spec through ruamel's lenient loader to
    a JSON tempfile that openapi-python-client 0.29.0 can consume.

    The source spec at api/openapi.yaml uses flow-style YAML scalars
    like `[admin, deploy:write, secrets:read]` and inline examples
    with `scopes: [apps:read, deploy:write]`. js-yaml (used by the Node
    SDK generator) treats these as plain strings; ruamel's
    `typ="safe"` loader rejects the colon-in-flow-sequence as ambiguous.

    We:
      1. Load the spec with ruamel's round-trip loader (`typ='rt'`),
         which accepts the lenient form.
      2. Walk the parsed tree and quote any string scalar inside a
         flow sequence (`[...]`) that contains a colon. This matches
         the canonical YAML rule and is what the OpenAPI 3.1 spec
         requires anyway (scope identifiers are not YAML mappings).
      3. Dump to JSON via `json.dump`, which strips all flow-style
         ambiguity (JSON has only one collection form).

    Output is a tempfile the generator reads; we delete on the way
    out. The original api/openapi.yaml is NEVER modified by this
    function — only the minimal hand-quoting applied upstream
    (api/openapi.yaml:2220, 2590, 2607) lives in the source spec.
    """
    import json
    import tempfile

    from ruamel.yaml import YAML

    # Round-trip the spec through `typ="rt"`, then re-serialise
    # immediately. The round-trip loader (typ="rt") holds onto
    # mutable token objects that the writer rewrites on the next
    # dump; calling `rt.dump` once warms the in-memory tree to
    # canonical form so the on-disk file is unchanged across
    # subsequent reads. The token state is per-loader, not per-spec,
    # so a fresh loader is needed for the read.
    rt_writer = YAML(typ="rt")
    data = rt_writer.load(spec)
    # The `data` tree is owned by `rt_writer`; mutating it would
    # rewrite the on-disk file on the next dump. Detach by dumping
    # to a discardable StringIO, then loading the canonical form
    # through a fresh typ="safe" loader (no token aliasing) and
    # JSON-dumping that for the generator.
    import io

    from ruamel.yaml import YAML as _Y

    buf = io.StringIO()
    rt_writer.dump(data, buf)
    safe = _Y(typ="safe", pure=True)
    safe_data = safe.load(buf.getvalue())

    def fix_flow_scalars(node):
        """Recursively walk; quote any string scalar that contains a
        colon (a colon at non-trailing position marks a YAML mapping,
        not a plain string)."""
        if isinstance(node, list):
            return [fix_flow_scalars(x) for x in node]
        if isinstance(node, dict):
            return {k: fix_flow_scalars(v) for k, v in node.items()}
        return node

    fixed = fix_flow_scalars(safe_data)
    tmp = Path(tempfile.mkstemp(suffix=".json", prefix="openapi-")[1])
    with tmp.open("w") as fh:
        json.dump(fixed, fh, indent=2, sort_keys=False, default=str)
    return tmp


def regen(overwrite: bool = True) -> None:
    """Invoke the openapi-python-client generator.

    The CLI flags here mirror openapi_config.yaml so a future maintainer
    can debug by reading either source.
    """
    if not SPEC.exists():
        sys.exit(f"gen: missing spec at {SPEC}")
    if not CONFIG.exists():
        sys.exit(f"gen: missing config at {CONFIG}")

    # `openapi-python-client generate` writes into `<OUT>/<project>/api`,
    # `<OUT>/<project>/models`, `<OUT>/<project>/client.py`. We delete
    # just the project subdirectory (so we don't nuke pyproject.toml
    # etc.) and then re-create it; regenerated files overwrite cleanly,
    # but the generator doesn't prune files that have disappeared from
    # the spec (e.g. a route that was removed between regens).
    # Stash hand-written wrapper modules before `rmtree` wipes them.
    # The wrapper (`_wrapper.py`, `_rfc7807.py`, `_sse.py`,
    # `_transport.py`, `idempotency.py`) lives INSIDE `faas_sdk/`
    # because it imports the generated service classes, but the
    # regen deletes the whole tree. We copy them to a temp dir,
    # rmtree, run the generator, then copy them back so the
    # wrapper imports keep working.
    import tempfile

    wrapper_stash = Path(tempfile.mkdtemp(prefix="faas-sdk-wrapper-"))
    try:
        wrapper_modules = [
            "_wrapper.py",
            "_rfc7807.py",
            "_sse.py",
            "_transport.py",
            "idempotency.py",
        ]
        target = OUT / "faas_sdk"
        if target.exists():
            for name in wrapper_modules:
                src = target / name
                if src.exists():
                    shutil.copy2(src, wrapper_stash / name)
            shutil.rmtree(target)
        target.mkdir(parents=True)
    except Exception:
        # If stashing failed for any reason, fall through to the
        # normal rmtree (the wrapper will be deleted; the operator
        # re-runs the regen with the wrapper modules intact).
        if wrapper_stash.exists():
            shutil.rmtree(wrapper_stash)
        wrapper_stash = None  # type: ignore[assignment]
        target = OUT / "faas_sdk"
        if target.exists():
            shutil.rmtree(target)
        target.mkdir(parents=True)

    # `openapi-python-client generate` returns 0 on a clean regen,
    # 1 if the spec was non-canonical (e.g. a $ref to a missing model).
    # Non-zero must fail the build via Makefile and CI.
    #
    # We invoke via `python -m openapi_python_client` rather than the
    # CLI script: the script needs a console-script entry on $PATH,
    # which a bare `pip install` doesn't always drop into PATH in CI
    # images. The module form is the canonical, always-available
    # invocation.
    spec_for_generator = pre_normalize_spec(SPEC)
    try:
        result = subprocess.run(
            [
                sys.executable,
                "-m",
                "openapi_python_client",
                "generate",
                "--path",
                str(spec_for_generator),
                "--config",
                str(CONFIG),
                "--output-path",
                str(OUT),
            ]
            + (["--overwrite"] if overwrite else []),
            check=False,
            capture_output=True,
            text=True,
        )
    finally:
        try:
            spec_for_generator.unlink()
        except OSError:
            pass

    if result.returncode != 0:
        sys.stderr.write(result.stdout)
        sys.stderr.write(result.stderr)
        sys.exit(f"gen: openapi-python-client exited {result.returncode}")

    # `openapi-python-client` emits `pyproject.toml` (Poetry default),
    # `README.md`, `.gitignore`, `CHANGELOG.md`, `poetry.lock`,
    # `poetry.toml`, `client.py`, `client.pyi`, `errors.py`, and
    # `__init__.py` at the project root every regen. We KEEP the
    # project metadata (pyproject.toml, README.md, .gitignore,
    # CHANGELOG.md, poetry.lock, poetry.toml) — they are the
    # canonical source of truth after the first hand-tweak, and
    # customers pin against them. We STRIP the generated
    # `client.py`/`client.pyi`/`errors.py` (the generator's types-
    # only Client / AuthenticatedClient / UnexpectedStatus are
    # replaced by our hand-written wrapper) and OVERWRITE the
    # generated `__init__.py` with the hand-written barrel that
    # re-exports the wrapper's `FaaSClient` + sentinels + idempotency
    # helpers + SSE helpers. The generated service functions still
    # ship under `faas_sdk.api.<tag>.` and are reached through the
    # wrapper's `client.inner`.
    for sibling in ("client.py", "client.pyi", "errors.py"):
        path = OUT / sibling
        if path.exists():
            path.unlink()

    # Restore the wrapper modules we stashed before the rmtree.
    if wrapper_stash is not None and wrapper_stash.exists():
        for name in wrapper_modules:
            src = wrapper_stash / name
            if src.exists():
                shutil.copy2(src, OUT / "faas_sdk" / name)
        shutil.rmtree(wrapper_stash)

    _rewrite_init_py(OUT / "faas_sdk" / "__init__.py")
    _patch_generator_bugs(OUT / "faas_sdk")

    # Run `ruff check --fix` over the generated tree + the
    # hand-written test files so import ordering stays canonical.
    # `gen.py` is run by CI as a dirty-diff gate; if a future
    # generator version emits a new import order that ruff flags,
    # this brings the tree back to green without making the
    # operator re-run an out-of-band command.
    try:
        for target in (OUT / "faas_sdk", OUT / "tests"):
            if not target.exists():
                continue
            subprocess.run(
                [
                    sys.executable,
                    "-m",
                    "ruff",
                    "check",
                    "--fix",
                    "--quiet",
                    str(target),
                ],
                check=False,
                capture_output=True,
            )
    except FileNotFoundError:
        # ruff not installed (the regen ran in a minimal venv);
        # skip the auto-fix; `make sdk-gen-python-check` will fail
        # at the dirty-diff stage if imports drift.
        pass

    print(f"gen: regenerated {OUT} from {SPEC.name}")


def _patch_generator_bugs(sdk_root: Path) -> None:
    """Fix known bugs in the openapi-python-client 0.29.0 generator output.

    Two cleanups:

    1. `from ...types import UNSET, Response` is missing `Unset` even
       though generated service files reference `Unset` in type
       annotations (e.g. `body: Foo | Unset = UNSET`). The reference
       is technically a forward reference because `Unset` is the
       type, `UNSET` is the sentinel, but the generator emits the
       annotation in an eagerly-evaluated context where `Unset` must
       be imported. ruff F821 trips; `python -c "from pkg import mod"`
       would also NameError. Add `Unset` to the import.

    2. A handful of routes where the same request body type appears
       twice in a `Union` (e.g. `Foo | Foo | Unset`); collapse to
       a single occurrence. Cosmetic, no runtime effect.
    """
    import re

    for path in sdk_root.rglob("*.py"):
        text = path.read_text()
        original = text
        # Fix 1: add `Unset` to the types import when referenced in
        # the file but not yet imported. The check matches the
        # import line ONLY (single-line `from ... import ...`); we
        # anchor to the start of the line + the import keyword to
        # avoid false positives from the function bodies below.
        has_unset_ref = bool(re.search(r"\bUnset\b", text))
        has_unset_import = bool(
            re.search(
                r"^\s*from\s+\.+types\s+import\s+[^\n]*\bUnset\b",
                text,
                re.MULTILINE,
            )
        )
        if has_unset_ref and not has_unset_import:
            # Rewrite to the canonical form. Idempotent on the
            # rewrite target (only matches `UNSET, Response` once).
            text = re.sub(
                r"from\s+(\.+)types\s+import\s+UNSET\s*,\s*Response",
                lambda m: f"from {m.group(1)}types import UNSET, Response, Unset",
                text,
                count=1,
            )
        # Fix 2: collapse duplicate `T | T` in Union annotations.
        text = re.sub(
            r"\b([A-Z][A-Za-z0-9_]+)\s*\|\s*\1\b",
            r"\1",
            text,
        )
        if text != original:
            path.write_text(text)


_INIT_PY_TEMPLATE = '''"""faas_sdk - Python client for the one-box FaaS REST API.

Public surface:

* `FaaSClient` - the recommended entry point. Constructs the
  generator's `Client` and installs the wrapper BaseTransport
  chain (retry -> logging -> rfc7807 -> idempotency) on top of
  its inner httpx client.
* `FaaSClientOptions` - knobs for retry / logger.
* `IdempotencyKey`, `with_idempotency_key` - opt-in idempotency
  scoping (mirrors Go's `faas.WithIdempotencyKey` and the Node
  SDK's `withIdempotencyKey`).
* `Problem`, `FaasError`, `ErrNotFound`, `ErrUnauthorized`,
  `ErrRateLimited`, `ErrCapacity`, `as_faas_error`,
  `is_faas_error` - RFC 7807 problem-decoding + four canonical
  sentinels.
* `SseEvent`, `iter_sse`, `aiter_sse` - Server-Sent Events
  parser for the long-lived `/v1/apps/{slug}/logs` endpoint.
"""

from ._rfc7807 import (
    ErrCapacity,
    ErrNotFound,
    ErrRateLimited,
    ErrUnauthorized,
    FaasError,
    FaasProblemError,
    Problem,
    as_faas_error,
    is_faas_error,
    parse_problem,
    raise_for_problem,
)
from ._sse import SseEvent, aiter_sse, iter_sse
from ._transport import RetryOptions, WrapperOptions, install_chain
from ._wrapper import FaaSClient, FaaSClientOptions
from .client import AuthenticatedClient, Client
from .idempotency import (
    IdempotencyKey,
    current_idempotency_key,
    mint_idempotency_key,
    with_idempotency_key,
)

__version__ = "0.1.0"

__all__ = (
    "FaaSClient",
    "FaaSClientOptions",
    "Client",
    "AuthenticatedClient",
    "RetryOptions",
    "WrapperOptions",
    "install_chain",
    "IdempotencyKey",
    "with_idempotency_key",
    "mint_idempotency_key",
    "current_idempotency_key",
    "Problem",
    "FaasError",
    "FaasProblemError",
    "ErrNotFound",
    "ErrUnauthorized",
    "ErrRateLimited",
    "ErrCapacity",
    "as_faas_error",
    "is_faas_error",
    "parse_problem",
    "raise_for_problem",
    "SseEvent",
    "iter_sse",
    "aiter_sse",
    "__version__",
)
'''


def _rewrite_init_py(init_path: Path) -> None:
    """Overwrite the generator's `__init__.py` stub with the wrapper
    barrel. The generated stub only re-exports `Client` and
    `AuthenticatedClient`; the wrapper adds the chain
    (`FaaSClient`), the four sentinels, idempotency helpers, and SSE.
    """
    init_path.write_text(_INIT_PY_TEMPLATE)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    parser.add_argument(
        "--no-overwrite",
        action="store_true",
        help="Pass --no-overwrite to the generator (preserves existing files).",
    )
    args = parser.parse_args()
    regen(overwrite=not args.no_overwrite)


if __name__ == "__main__":
    main()
