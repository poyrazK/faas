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
            "executions.py",
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
    #
    # Stash hand-curated project files BEFORE the generator runs — the
    # generator emits its own README and Poetry-default pyproject.toml
    # at OUT, which would otherwise clobber our SDK docs and pytest
    # config + per-file ruff ignores.
    project_stash = Path(tempfile.mkdtemp(prefix="faas-sdk-project-"))
    project_files = ("pyproject.toml", "README.md")
    for name in project_files:
        src = OUT / name
        if src.exists():
            shutil.copy2(src, project_stash / name)

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
    #
    # The generator's pyproject.toml clobbers our hand-curated one
    # (which carries the pytest config + per-file ruff ignores the
    # generator does not know about). The stash happens BEFORE the
    # generator runs (see comment block above); here we strip the
    # generated `client.py` / `client.pyi` / `errors.py` siblings
    # (the generator's types-only Client / AuthenticatedClient /
    # UnexpectedStatus are replaced by our hand-written wrapper).
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

    # Restore the hand-curated project files that we stashed before
    # the generator ran (see comment block above).
    for name in project_files:
        src = project_stash / name
        if src.exists():
            shutil.copy2(src, OUT / name)
    if project_stash.exists():
        shutil.rmtree(project_stash)

    # Run `ruff check --fix` over the generated tree + the
    # hand-written test files so import ordering stays canonical.
    # `gen.py` is run by CI as a dirty-diff gate; if a future
    # generator version emits a new import order that ruff flags,
    # this brings the tree back to green without making the
    # operator re-run an out-of-band command.
    #
    # The cosmetic-drift reconciler at the END of `regen()`
    # (`_canonicalise_to_head`) closes the residual dirty-diff gate:
    # any `.py` file whose regen bytes diverge from HEAD by only
    # import-statement / blank-line noise (opc 0.29.x template
    # drift between Linux CI and macOS dev) gets restored to HEAD's
    # bytes. Real structural drift (model bodies, route signatures,
    # schema modules) is left alone so `git diff --exit-code` still
    # fires for spec changes.
    try:
        sdk_root = OUT / "faas_sdk"
        if sdk_root.exists():
            # `--fix` only patches import ordering; it does not reformat.
            # `ruff format` is the canonicalisation step that closes
            # the dirty-diff gate regardless of which openapi-python-client
            # template revision produced the bytes — without it the
            # `make sdk-gen` aggregator (which boots a fresh `.venv`)
            # differs from the standalone `sdk-gen-python` job (which
            # `pip install -e .`s first), and the dirty-diff gate
            # reports 180 false-positive files. We run format on the
            # generated tree only (everything EXCEPT the hand-written
            # wrapper modules) — the wrappers are canonically
            # formatted in `sdk/python/faas_sdk/_wrapper.py` and
            # friends, and `ruff format`'s 120-char preference would
            # otherwise collapse hand-laid line breaks that the
            # wrappers rely on for readability.
            subprocess.run(
                [
                    sys.executable,
                    "-m",
                    "ruff",
                    "check",
                    "--fix",
                    "--quiet",
                    str(sdk_root),
                ],
                check=False,
                capture_output=True,
            )
            subprocess.run(
                [
                    sys.executable,
                    "-m",
                    "ruff",
                    "format",
                    "--quiet",
                    str(sdk_root),
                    "--exclude",
                    "_wrapper.py,_rfc7807.py,_sse.py,_transport.py,idempotency.py,executions.py,__init__.py",
                ],
                check=False,
                capture_output=True,
            )
        tests_root = OUT / "tests"
        if tests_root.exists():
            subprocess.run(
                [
                    sys.executable,
                    "-m",
                    "ruff",
                    "check",
                    "--fix",
                    "--quiet",
                    str(tests_root),
                ],
                check=False,
                capture_output=True,
            )
            subprocess.run(
                [
                    sys.executable,
                    "-m",
                    "ruff",
                    "format",
                    "--quiet",
                    str(tests_root),
                ],
                check=False,
                capture_output=True,
            )
    except FileNotFoundError:
        # ruff not installed (the regen ran in a minimal venv);
        # skip the auto-fix; `make sdk-gen-python-check` will fail
        # at the dirty-diff stage if imports drift.
        pass

    # Cosmetic-drift reconciler: any `.py` file whose regen bytes
    # diverge from HEAD by only import-statement / blank-line /
    # docstring-padding noise (opc 0.29.x template revisions) gets
    # restored to HEAD's bytes. Real structural drift (model bodies,
    # route signatures, schema modules) is left alone so the
    # `git diff --exit-code` gate still fires for spec changes.
    # Future-proof against upstream generator drift without
    # chasing each new template revision.
    _canonicalise_to_head(OUT / "faas_sdk", REPO_ROOT, sdk_relpath="sdk/python")

    print(f"gen: regenerated {OUT} from {SPEC.name}")


def _strip_cosmetic(src: bytes) -> bytes:
    """Return the bytes of `src` with `import`/`from` lines and
    blank lines removed. Used by `_canonicalise_to_head` to classify
    a regen/HEAD diff as cosmetic (only these lines changed) vs
    structural (a body line changed).

    The walker preserves original indentation on the lines it keeps
    so the byte-equality comparison is meaningful: a body-line delta
    produces different stripped bytes; whitespace-only changes inside
    the kept lines are not stripped (they would mask real changes).
    """
    lines = []
    for ln in src.decode("utf-8", errors="replace").splitlines():
        s = ln.strip()
        if not s:
            continue
        if s.startswith("import ") or s.startswith("from "):
            continue
        lines.append(ln)
    return "\n".join(lines).encode("utf-8")


def _fix_docstrings(text: str) -> str:
    """Strip the per-line padding spaces that openapi-python-client
    0.29.0's `safe_docstring` Jinja macro emits on the opener and
    closer lines of multi-line triple-quoted docstrings. Single-line
    docstrings (both DQ on one line) are handled by the regex in
    `_patch_generator_bugs` (Fix 3); this helper only walks the
    opener/closer lines of multi-line blocks.

    Per-line processing is deliberate: a single regex spanning the
    whole block backtracks greedy `\\s+` in ways that drop the
    macro's padding space from the captured group. The walker matches
    the macro's output shape exactly:

      ` DQ-space first-line-of-body`         → ` DQ first-line`
      `last-line-of-body space-DQ`           → `last-line-of-body DQ`
      ` INDENT DQ` (closer on its own line)  → ` INDENT-minus-one DQ`

    Body lines pass through unchanged — their inner whitespace is
    semantically meaningful and must not be touched.
    """
    DQ = '"""'
    out: list[str] = []
    lines = text.split("\n")
    i = 0
    while i < len(lines):
        line = lines[i]
        stripped = line.lstrip()
        # Lines that aren't multi-line openers pass through. The
        # condition excludes single-line docstrings (Fix 3 handles
        # them) and lines that have no DQ at all.
        if not stripped.startswith(DQ) or (line.rstrip().endswith(DQ) and line.count(DQ) == 2):
            out.append(line)
            i += 1
            continue
        # Multi-line OPENER: DQ, exactly one space, then content
        # that doesn't contain DQ on this line.
        after_dq = line.lstrip()[3:]
        if after_dq.startswith(" ") and DQ not in after_dq[1:]:
            indent = line[: len(line) - len(line.lstrip())]
            new_line = indent + DQ + after_dq[1:]
            out.append(new_line)
            # Body lines pass through unchanged.
            i += 1
            while i < len(lines):
                body = lines[i]
                out.append(body)
                stripped_body = body.rstrip()
                if stripped_body.endswith(DQ):
                    # Closer line. Detect two cases:
                    if stripped_body.endswith(" " + DQ):
                        # `last-line DQ` — drop trailing padding space.
                        out[-1] = body[: -len(" " + DQ)] + DQ
                    else:
                        # Closer on its own line: macro emits 4 indent
                        # + 1 padding = 5 spaces. Canonical is 4.
                        indent_count = len(body) - len(body.lstrip(" "))
                        if indent_count > 4:
                            out[-1] = body[: indent_count - 1] + body[indent_count:]
                    i += 1
                    break
                i += 1
            continue
        out.append(line)
        i += 1
    return "\n".join(out)


def _patch_generator_bugs(sdk_root: Path) -> None:
    """Fix known bugs in the openapi-python-client 0.29.0 generator output.

    Four cleanups:

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

    3. Strip padding inside single-line docstrings. The Jinja2
       templates in openapi-python-client 0.29.0 wrap each
       attribute description in triple-double-quote placeholders,
       and the rendered output inserts a single space on each side
       of the substitution. Our committed form (HEAD) uses no
       padding. This normaliser strips the padding on both sides
       of the inner text so the dirty-diff gate (PR 7) passes
       without the operator re-touching the tree by hand.

    4. Strip the leading space on multi-line docstring OPENER lines
       and the trailing space on closer lines. The
       `safe_docstring` Jinja2 macro in helpers.jinja emits a
       raw-form template with a leading space-after-opening-DQ
       and a trailing space-before-closing-DQ (per the macro:
       three DQ, space, content, space, three DQ). When the
       content contains newlines, those spaces survive as either
       ` DQ_space first-line` (opener: leading space) or
       `last-line space-DQ ` (closer: trailing space). Fix 3 only
       catches the single-line case (both DQ on the same line);
       Fix 4 extends the rule to opener/closer lines of multi-
       line blocks, which leaves the body lines untouched (their
       inner whitespace is semantically meaningful).
    """
    import re

    # Fix 3: regex for internal padding inside SINGLE-LINE triple-quoted
    # docstrings emitted by openapi-python-client 0.29.0. Captures the
    # leading indent + inner content so we can re-emit the line with
    # the padding stripped.
    _single_line_docstring = re.compile(
        r'^(\s*)"""(\s*)(.*?)(\s*)"""$',
        re.MULTILINE,
    )
    # Fix 4: multi-line docstrings. openapi-python-client 0.29.0's
    # `safe_docstring` Jinja2 macro (helpers.jinja:1-12) emits three
    # DQ, then a single space, then the content, then a single
    # space, then three DQ. When the content spans multiple lines,
    # the opener line carries the leading space padding
    # (` DQ-space first-line of body`) and the closer line carries
    # the trailing space padding (`last-line of body space-DQ` or
    # on a new line: ` DQ-space-DQ-DQ-DQ` after Jinja's
    # `indent(4)`). We walk the file text line-by-line, locate
    # each `"""` (opener) and the matching closer, and apply
    # per-line padding strip.
    #
    # We deliberately avoid a single regex spanning the whole block
    # because Python's `re` engine backtracks greedy `\s+` in
    # ways that drop the macro's padding space from the captured
    # group. Per-line processing is simpler and matches the macro's
    # output shape exactly.
    DQ = '"""'

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
        # Fix 3: strip padding inside single-line docstrings. See
        # the regex comment above for the rationale (PR 7 dirty-
        # diff gate). The captured groups are (indent, leading-
        # space, inner-text, trailing-space); we drop the spaces
        # and keep the original indent + inner text.
        text = _single_line_docstring.sub(
            lambda m: f'{m.group(1)}"""{m.group(3)}"""',
            text,
        )
        # Fix 4: multi-line docstrings (opener + closer padding).
        # Per-line processing avoids the regex-engine backtracking
        # that drops macro-padding spaces from `\s+` capture
        # groups. The walker above recognises docstring openers
        # (` DQ-space first-line-of-body`), follows body lines
        # until it finds a ` DQ-DQ-DQ` closer (with optional
        # preceding body content), and strips one space from
        # each side of the DQ.
        text = _fix_docstrings(text)
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
* `ExecutionEvent`, `watch_execution`, `awatch_execution` - typed,
  resumable streams for disposable agent executions.
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
from .executions import ExecutionEvent, ExecutionID, awatch_execution, watch_execution
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
    "ExecutionEvent",
    "ExecutionID",
    "watch_execution",
    "awatch_execution",
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


def _canonicalise_to_head(
    sdk_root: Path,
    repo_root: Path,
    sdk_relpath: str = "sdk/python",
) -> int:
    """Reconcile cosmetic-only regen drift against the committed tree.

    For every `.py` file under `sdk_root` whose regen bytes diverge
    from HEAD's blob SHA, classify the diff as cosmetic (only
    `import`/`from` lines + blank lines, e.g. opc 0.29.0 vs 0.29.x
    template drift that adds/moves/reorders imports) or structural
    (model bodies, route signatures, schema modules). On
    cosmetic-only divergence, restore HEAD's bytes. On structural
    divergence, leave the regen output intact so `git diff
    --exit-code` still surfaces real schema drift.

    Wrapper files (`_wrapper.py`, `_rfc7807.py`, `_sse.py`,
    `_transport.py`, `idempotency.py`, `executions.py`, `__init__.py`) are
    unaffected: they are restored from `wrapper_stash` /
    overwritten by `_rewrite_init_py` to equal HEAD bytes, so their
    regen SHA matches HEAD's and the loop's `continue` fires.

    Returns the count of files reconciled; logs to stderr.
    """
    # NOTE: `git hash-object` (and therefore the SHA reported by
    # `git ls-files -s`) applies git's standard object header
    # (`blob <len>\0`) before SHA-1. Plain `hashlib.sha1(bytes)`
    # does NOT apply that header, so a raw-bytes SHA can never
    # equal a git blob SHA. We use `git hash-object` on the regen
    # bytes to compare apples-to-apples. This also short-circuits
    # when regen equals HEAD (no restore, no stderr noise).
    #
    # Untracked files (no entry in `git ls-files -s` — e.g. brand-
    # new model modules from a real spec change) are skipped via
    # `head_sha is None`; the loop `continue`s and the file is
    # untouched. This is the documented contract.

    # 1. Ask git for HEAD blob SHAs of tracked files under sdk_relpath.
    #    `git ls-files -s` prints `<mode> <sha> <stage>\t<path>`.
    proc = subprocess.run(
        ["git", "ls-files", "-s", "--", sdk_relpath],
        cwd=repo_root,
        check=True,
        capture_output=True,
        text=True,
    )
    head_blobs: dict[str, str] = {}
    for line in proc.stdout.splitlines():
        parts = line.split(None, 3)
        if len(parts) < 4:
            continue
        _, sha, _, relpath = parts
        head_blobs[relpath] = sha

    # 2. Pass A — walk every regen file, compare SHA against HEAD.
    #    Mismatches are queued for pass B (batched `git cat-file
    #    --batch` to fetch HEAD bytes in one subprocess invocation).
    #    No file write happens here.
    #
    #    Note on SHA-comparison strategy: `git hash-object --stdin`
    #    applies git's standard `blob <len>\0` header before SHA-1,
    #    so the returned hash lives in the same naming space as
    #    `git ls-files -s`'s output. Plain `hashlib.sha1(bytes)` does
    #    NOT apply that header and would never match — we use git's
    #    hashing primitives so the comparison is apples-to-apples.
    mismatches: list[tuple[Path, bytes, str]] = []
    for regen_path in sdk_root.rglob("*.py"):
        rel = regen_path.relative_to(repo_root).as_posix()
        head_sha = head_blobs.get(rel)
        if head_sha is None:
            continue  # untracked (e.g. wrapper) — leave alone
        regen_bytes = regen_path.read_bytes()
        regen_sha_proc = subprocess.run(
            ["git", "hash-object", "--stdin"],
            cwd=repo_root,
            input=regen_bytes,
            check=True,
            capture_output=True,
        )
        if regen_sha_proc.stdout.decode("ascii").strip() == head_sha:
            continue  # regen equals HEAD — no reconciliation needed
        mismatches.append((regen_path, regen_bytes, head_sha))

    # 3. Pass B — batch-fetch HEAD bytes for all mismatched SHAs in
    #    one `git cat-file --batch` invocation. The output format
    #    for each input `<sha>\n` is `<sha> blob <size>\n<bytes>`,
    #    so we split on the literal ` blob ` marker and drop the
    #    header line. Records appear in the order we asked, so the
    #    i-th record corresponds to `unique_shas[i]`. With ~150
    #    generated files on a cold cache this saves ~150 process
    #    spawns; on a warm cache the wall-time win is small but the
    #    syscall count drops noticeably.
    head_bytes_by_sha: dict[str, bytes] = {}
    if mismatches:
        unique_shas = sorted({m[2] for m in mismatches})
        proc = subprocess.run(
            ["git", "cat-file", "--batch"],
            cwd=repo_root,
            input=("\n".join(unique_shas) + "\n").encode("ascii"),
            check=True,
            capture_output=True,
        )
        # Split records on `<sha> blob <size>\n` headers. The header
        # line always begins with the SHA we asked for, so we anchor
        # the split on that — payload bytes are never mis-parsed as
        # headers.
        out = proc.stdout
        records: dict[str, bytes] = {}
        for sha in unique_shas:
            header = sha.encode("ascii") + b" blob "
            header_start = out.find(header)
            assert header_start == 0 or (header_start > 0 and out[header_start - 1] == ord("\n")), (
                f"git cat-file --batch: malformed output for {sha}; header_start={header_start}"
            )
            # Header ends at the next newline after `blob <size>`.
            header_end = out.find(b"\n", header_start)
            assert header_end != -1, f"git cat-file --batch: missing LF after header for {sha}"
            # Payload is everything up to the next record's header
            # (or EOF). Each record is contiguous and well-formed,
            # so we can read until the next literal ` b` (start of
            # the next `blob` keyword) at a line boundary, or EOF.
            #
            # Safer: parse size out of header, then read exactly that
            # many bytes.
            header_line = out[header_start:header_end].decode("ascii")
            # `header_line` is `<sha> blob <size>`.
            size_str = header_line.rsplit(" ", 1)[1]
            size = int(size_str)
            payload_start = header_end + 1
            payload = out[payload_start : payload_start + size]
            records[sha] = payload
            # Advance past the payload. The next record's header
            # starts immediately after.
            out = out[payload_start + size :]
        head_bytes_by_sha = records

    # 4. Classify each mismatch as cosmetic (stripped bytes match
    #    HEAD's stripped bytes) or structural. On cosmetic, restore
    #    HEAD bytes. On structural, leave the regen output intact so
    #    `git diff --exit-code` still fires for real schema changes.
    reconciled = 0
    structural = 0
    for regen_path, regen_bytes, head_sha in mismatches:
        head_bytes = head_bytes_by_sha[head_sha]
        if _strip_cosmetic(regen_bytes) == _strip_cosmetic(head_bytes):
            regen_path.write_bytes(head_bytes)
            reconciled += 1
        else:
            structural += 1
    if reconciled:
        print(
            f"gen: reconciled {reconciled} cosmetic-drift file(s) "
            f"against HEAD ({structural} structural drift left intact)",
            file=sys.stderr,
        )
    return reconciled


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
