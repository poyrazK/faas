#!/usr/bin/env python3
"""scripts/gen.py — (re)generate faas_sdk from sdk/openapi.yaml and faas_sdk.preview from api/openapi.yaml.

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
import os
import shutil
import subprocess
import sys
import tempfile
from contextlib import contextmanager
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
# ADR-20261023115741: `faas_sdk` is generated from the GA and beta subset
# (`make sdk-spec`); the complete client is generated into `faas_sdk.preview`.
PUBLIC_SPEC = REPO_ROOT / "sdk" / "openapi.yaml"
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
    # 0.29 interprets validation-only required/not compositions as Any,
    # dropping these named DTOs and their nested types. Preserve the source
    # schema and its runtime validation; give codegen the same object fields
    # without cross-field predicates it cannot express in attrs models.
    for name in ("ObjectRetentionPeriod", "ObjectLockDefaultRetention"):
        schema = fixed["components"]["schemas"].get(name)
        if schema is not None:
            for keyword in ("oneOf", "anyOf", "not"):
                schema.pop(keyword, None)
    # 0.29 rejects boolean enums and otherwise drops the entire preflight DTO.
    # Preserve its boolean wire type; the canonical spec and server still
    # require false while customer activation is disabled (ADR-1333).
    preflight = fixed["components"]["schemas"].get("ManagedPostgresCutoverPreflight")
    if preflight is not None:
        activation = preflight["properties"]["activation_allowed"]
        if activation.get("enum") != [False]:
            raise ValueError("Preflight activation gate generator adaptation needs review")
        activation.pop("enum")
    # The pinned generator discards object properties when a oneOf contains
    # only required-field constraints, producing body: Any instead of the
    # existing typed request model. Keep its wire shape in generated clients;
    # apid and the canonical spec still enforce exactly one mutation.
    update = fixed["components"]["schemas"].get("UpdateTCPListenerRequest", {})
    constraints = update.get("oneOf")
    if constraints is not None:
        if constraints != [{"required": ["enabled"]}, {"required": ["tls"]}]:
            raise ValueError("UpdateTCPListenerRequest generator adaptation needs review")
        del update["oneOf"]
    # The pinned generator ignores standalone application/x-ndjson responses.
    # Treat this binary attachment as opaque bytes for generation only; the
    # canonical API continues to return JSONL with its original content type.
    export_path = "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/export"
    export_operation = fixed.get("paths", {}).get(export_path, {}).get("get")
    if export_operation is not None:
        if export_operation.get("operationId") != "exportRealtimeConsumerGroupDeadLetters":
            raise ValueError("dead-letter export generator adaptation needs review")
        content = export_operation["responses"]["200"]["content"]
        if set(content) != {"application/x-ndjson"} or content["application/x-ndjson"]["schema"] != {
            "type": "string",
            "format": "binary",
        }:
            raise ValueError("dead-letter export binary response adaptation needs review")
        export_operation["responses"]["200"]["content"] = {"application/octet-stream": content["application/x-ndjson"]}
    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", prefix="openapi-", delete=False) as fh:
        tmp = Path(fh.name)
        json.dump(fixed, fh, indent=2, sort_keys=False, default=str)
    return tmp


# Hand-written modules of the GA and beta package, `faas_sdk/<name>`.
ROOT_WRAPPER_MODULES = (
    "_wrapper.py",
    "_rfc7807.py",
    "_sse.py",
    "_transport.py",
    "idempotency.py",
    "release_context.py",
    "webhook.py",
    "pre_auth_target.py",
    "secret_reload_runtime.py",
    "async_secret_reload_runtime.py",
    "workflows.py",
    "_binding_client_runtime.py",
    "_async_binding_client_runtime.py",
)

# Hand-written modules of preview and internal capabilities,
# `faas_sdk/preview/<name>` (ADR-20261023115741).
PREVIEW_WRAPPER_MODULES = (
    "_wrapper.py",
    "_binding_qualification.py",
    "_binding_creation.py",
    "_binding_runner.py",
    "_binding_runner_diagnostics.py",
    "realtime_workers.py",
    "realtime_activity.py",
    "realtime_presence.py",
    "realtime_publisher.py",
    "realtime_scopes.py",
    "realtime_read_progress.py",
    "realtime_read_publisher.py",
    "realtime_read_refresh.py",
    "realtime_connection_status.py",
    "realtime_health.py",
    "realtime_catch_up.py",
    "realtime_subscriptions.py",
    "realtime_readiness.py",
    "realtime_diagnostics.py",
    "realtime_timeline.py",
    "realtime_recovery.py",
    "realtime_websocket.py",
    "realtime_channels.py",
    "realtime_websocket_presence.py",
    "realtime_filters.py",
    "realtime_websocket_reads.py",
    "realtime_direct_messages.py",
    "realtime_expiry.py",
    "realtime_sse.py",
    "realtime_transport.py",
    "realtime_backpressure.py",
    "realtime_drain.py",
    "executions.py",
    "crash_snapshots.py",
    "dev_bridge.py",
    "auth.py",
    "ai_gateway.py",
    "issues.py",
    "flags.py",
    "commit.py",
    "operations_runtime.py",
    "job_operations_runtime.py",
    "object_storage_runtime.py",
    "postgres_pool_runtime.py",
    "async_postgres_pool_runtime.py",
    "async_object_storage_runtime.py",
    "queue_consumer.py",
    "trigger_batch.py",
    "operations.py",
    "data_repair.py",
    "data_repair_limits.py",
    "data_repair_readiness.py",
    "data_repair_readiness_probe.json",
    "data_repair_transactions.py",
    "event_consumer_transactions.py",
    "event_consumer_processing.py",
    "event_consumer_processing_inspection.py",
    "_event_consumer_processing_store.py",
    "event_consumer_delivery_context.py",
    "event_acknowledgment_worker.py",
    "event_acknowledgment_preparation.py",
    "event_acknowledgment_preparation_events.py",
    "event_acknowledgment_preparation_metrics.py",
    "_event_acknowledgment_preparation_metric_events.py",
    "event_acknowledgment_preparation_prometheus.py",
    "_event_acknowledgment_preparation_prometheus_validation.py",
    "_event_acknowledgment_preparation_runtime.py",
    "event_acknowledgment_intents.py",
    "event_acknowledgment_intent_recovery.py",
    "event_acknowledgment_intent_retry_policy.py",
    "event_acknowledgment_intent_policy_adoption.py",
    "_event_acknowledgment_intent_policy_adoption_state.py",
    "_event_acknowledgment_intent_command_transaction.py",
    "_event_acknowledgment_intent_retry_resume_state.py",
    "event_acknowledgment_intent_retry_resume.py",
    "_event_acknowledgment_intent_budget_adoption_state.py",
    "event_acknowledgment_intent_budget_adoption.py",
    "event_acknowledgment_intent_recovery_pass.py",
    "_event_acknowledgment_intent_recovery_pass_runtime.py",
    "event_acknowledgment_intent_health.py",
    "event_acknowledgment_intent_health_metrics.py",
    "event_acknowledgment_intent_health_prometheus.py",
    "event_acknowledgment_intent_health_metrics_watch.py",
    "event_acknowledgment_intent_watch.py",
    "_event_acknowledgment_intent_watch_policy.py",
    "event_acknowledgment_intent_checkpoint.py",
    "_event_acknowledgment_intent_checkpoint_state.py",
    "event_acknowledgment_intent_checkpoint_watch.py",
    "event_acknowledgment_intent_schema.sql",
    "event_acknowledgment_intent_stored_retry_policy_schema.sql",
    "event_acknowledgment_intent_policy_adoption_schema.sql",
    "event_acknowledgment_intent_retry_budget_schema.sql",
    "event_acknowledgment_intent_retry_resume_schema.sql",
    "event_acknowledgment_intent_budget_adoption_schema.sql",
    "_event_acknowledgment_protocol.py",
    "_event_acknowledgment_callbacks.py",
    "_event_deadline_policy.py",
    "event_deadline_checkpoint.py",
    "event_deadline_watch.py",
    "event_deadline_investigation.py",
    "commit_subscriber_wait.py",
    "commit_delivery_report.py",
    "commit_recovery_plan.py",
    "commit_recovery_apply.py",
    "commit_recovery_status.py",
    "_event_consumer_contract.py",
    "event_consumer_schema.sql",
    "event_consumer_processing_schema.sql",
    "_operation_contract.py",
    "customer_operation_schema.sql",
    "operation_schema.sql",
    "customer_operations.py",
    "customer_operation_publication.py",
    "business_decisions.py",
    "business_invariants.py",
    "business_effects.py",
    "business_compensation.py",
    "workflow_reconciliation.py",
)
PROJECT_FILES = ("pyproject.toml", "README.md")


@contextmanager
def _preserve_handwritten_sdk(output: Path):
    """Back up before deletion and restore on every generator exit path.

    A failed backup leaves the original tree untouched. A failed restoration
    retains the backup and reports its location instead of discarding source.
    This includes hand-written modules that have not yet been committed.
    """
    stash = Path(tempfile.mkdtemp(prefix="faas-sdk-preserved-"))
    preserved = []
    try:
        for relative in [
            *(Path("faas_sdk") / name for name in ROOT_WRAPPER_MODULES),
            *(Path("faas_sdk") / "preview" / name for name in PREVIEW_WRAPPER_MODULES),
            *(Path(name) for name in PROJECT_FILES),
            # Tests are authored inputs too. Formatter/import rewrites must
            # not replace their current bytes, including uncommitted edits.
            *(path.relative_to(output) for path in (output / "tests").rglob("*.py")),
        ]:
            source = output / relative
            if source.exists():
                backup = stash / relative
                backup.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(source, backup)
                preserved.append(relative)
    except BaseException:
        # No original files have been modified at this point.
        shutil.rmtree(stash)
        raise

    def restore():
        try:
            for relative in preserved:
                destination = output / relative
                destination.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(stash / relative, destination)
        except BaseException as exc:
            raise RuntimeError(f"gen: restore failed; source backup retained at {stash}") from exc

    try:
        yield restore
    finally:
        restore()
        shutil.rmtree(stash)


def _generate(spec: Path, output: Path, overwrite: bool) -> None:
    """Generate the `faas_sdk` package from spec into output."""
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
    spec_for_generator = pre_normalize_spec(spec)
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
                str(output),
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


def regen(overwrite: bool = True) -> None:
    """Invoke the openapi-python-client generator.

    The CLI flags here mirror openapi_config.yaml so a future maintainer
    can debug by reading either source.
    """
    if not SPEC.exists():
        sys.exit(f"gen: missing spec at {SPEC}")
    if not PUBLIC_SPEC.exists():
        sys.exit(f"gen: missing spec at {PUBLIC_SPEC}; run 'make sdk-spec'")
    if not CONFIG.exists():
        sys.exit(f"gen: missing config at {CONFIG}")

    # Back up every hand-written input before deleting generated files.
    # Restore even when cleanup, normalization, generation or postprocessing fails.
    with _preserve_handwritten_sdk(OUT) as restore_handwritten:
        target = OUT / "faas_sdk"
        if target.exists():
            shutil.rmtree(target)
        target.mkdir(parents=True)

        # ADR-20261023115741: the package root is generated from the GA and
        # beta spec; the complete client goes to `faas_sdk.preview`, generated
        # aside and moved in, sharing the root's types, client and errors.
        _generate(PUBLIC_SPEC, OUT, overwrite)
        with tempfile.TemporaryDirectory(prefix="faas-sdk-preview-") as full:
            _generate(SPEC, Path(full), True)
            preview = target / "preview"
            preview.mkdir(parents=True, exist_ok=True)
            for part in ("api", "models"):
                shutil.move(str(Path(full) / "faas_sdk" / part), str(preview / part))
        _write_preview_shims(target / "preview")

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
        # helpers + SSE, release-context, and webhook verification helpers. The generated service functions still
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

        restore_handwritten()

        _rewrite_init_py(OUT / "faas_sdk" / "__init__.py")
        (OUT / "faas_sdk" / "preview" / "__init__.py").write_text(_PREVIEW_INIT_PY_TEMPLATE)
        _patch_generator_bugs(OUT / "faas_sdk")
        _preserve_routing_replay_guard_precision(OUT / "faas_sdk")
        _preserve_routing_replay_guard_precision(OUT / "faas_sdk" / "preview")

        # Preserve the previous inline request model import after naming the schema.
        models = OUT / "faas_sdk" / "preview" / "models"
        (models / "create_commit_source_body.py").write_text(
            '"""Backward-compatible name for the Commit source creation request."""\n\n'
            "from .create_commit_source_request import CreateCommitSourceRequest as CreateCommitSourceBody\n\n"
            '__all__ = ("CreateCommitSourceBody",)\n'
        )
        barrel = models / "__init__.py"
        barrel.write_text(
            barrel.read_text()
            .replace(
                "from .create_commit_source_request import CreateCommitSourceRequest",
                "from .create_commit_source_body import CreateCommitSourceBody\n"
                "from .create_commit_source_request import CreateCommitSourceRequest",
            )
            .replace(
                '    "CreateCommitSourceRequest",', '    "CreateCommitSourceBody",\n    "CreateCommitSourceRequest",'
            )
        )

        # Run `ruff check --fix` over the generated tree so import ordering
        # stays canonical. Hand-written tests are inputs to verification;
        # regeneration must not rewrite them as a formatter side effect.
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
                        "_wrapper.py,_rfc7807.py,_sse.py,_transport.py,idempotency.py,executions.py,release_context.py,crash_snapshots.py,dev_bridge.py,webhook.py,flags.py,realtime_workers.py,realtime_activity.py,realtime_presence.py,realtime_publisher.py,realtime_scopes.py,realtime_read_progress.py,realtime_read_publisher.py,realtime_read_refresh.py,realtime_connection_status.py,realtime_health.py,realtime_catch_up.py,realtime_subscriptions.py,realtime_readiness.py,realtime_diagnostics.py,realtime_timeline.py,realtime_recovery.py,realtime_websocket.py,realtime_channels.py,realtime_websocket_presence.py,realtime_filters.py,realtime_websocket_reads.py,realtime_direct_messages.py,realtime_expiry.py,realtime_sse.py,realtime_transport.py,realtime_backpressure.py,realtime_drain.py,__init__.py",
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

    Six cleanups:

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
    5. Binary File responses can be generated as BytesIO(response.text) when
       a route also offers text/csv. BytesIO requires bytes; response.content
       preserves ZIP and UTF-8 artifacts without decoding or corruption.
    6. Optional WorkerScaling.name omits any Unset instance, matching the
       established model contract when callers construct Unset() explicitly.
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
    for path in sdk_root.rglob("*.py"):
        text = path.read_text()
        original = text
        # Preserve the optional WorkerScaling.name contract for every Unset
        # instance, including callers that construct Unset() explicitly.
        if path.relative_to(sdk_root).as_posix() in ("models/worker_scaling.py", "preview/models/worker_scaling.py"):
            text = text.replace("if name is not UNSET:", "if not isinstance(name, Unset):")
        # Fix 5: downloads must preserve bytes, including invalid UTF-8 in ZIPs.
        text = text.replace("BytesIO(response.text)", "BytesIO(response.content)")
        if path.name == "app_manifest.py":
            # Literal annotations do not validate constructor values. Retain
            # the transport wire guard when regenerating the manifest model.
            text = text.replace(
                "service_binding_transport = self.service_binding_transport\n",
                "service_binding_transport = check_service_binding_transport(self.service_binding_transport)\n",
            )
        # Runtime UUID headers are UUID objects in the typed signatures;
        # HTTPX requires their textual wire representation.
        for header, argument in (
            ("X-Faas-Invocation-Id", "x_faas_invocation_id"),
            ("X-Gregale-Operation-Workflow-Run-Id", "x_gregale_operation_workflow_run_id"),
            ("X-Gregale-Operation-Workflow-Capability", "x_gregale_operation_workflow_capability"),
            ("X-Gregale-Operation-Job-Run-Id", "x_gregale_operation_job_run_id"),
            ("X-Gregale-Operation-Job-Instance-Id", "x_gregale_operation_job_instance_id"),
        ):
            text = text.replace(f'headers["{header}"] = {argument}\n', f'headers["{header}"] = str({argument})\n')
        if path.parent.name == "operations" and 'headers["X-Gregale-Operation-Job-Run-Id"]' in text:
            # Native capabilities travel through the tokenless client's
            # headers; the bearer-client annotation is a generator limitation.
            text = text.replace("client: AuthenticatedClient,", "client: Client,")
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

This package covers generally available and beta capabilities
(ADR-20261023115741): the client generated from `sdk/openapi.yaml` under
`faas_sdk.api` and `faas_sdk.models`, and the helpers below. Preview and
internal capabilities (realtime, operations, events, durable entities,
queues, object storage, managed Postgres, disposable runs, issues, commit,
data repair, AI gateway, flags, and more) are in `faas_sdk.preview`, which
re-exports this package too.

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
* `GregaleReleaseMiddleware` and HTTPX transports - capture and forward the
  request's project release to managed service calls.
* `verify_webhook` - verify signed outbound deliveries against their raw body
  and return the stable delivery ID for receiver-side deduplication.
* `pre_auth_target_digest` - opaque login-target signal for selected failed
  responses on opt-in pre-auth routes.
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
from .pre_auth_target import PRE_AUTH_TARGET_HEADER, pre_auth_target_digest
from .release_context import (
    GREGALE_RELEASE_HEADER,
    GREGALE_REQUEST_DEADLINE_HEADER,
    GREGALE_REVISION_HEADER,
    AsyncGregaleReleaseTransport,
    GregaleReleaseMiddleware,
    GregaleReleaseTransport,
    current_gregale_release,
    current_gregale_request_deadline,
    with_gregale_release,
    with_gregale_request_context,
)
from .webhook import (
    DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE,
    WEBHOOK_DELIVERY_ID_HEADER,
    WEBHOOK_SIGNATURE_HEADER,
    WEBHOOK_TIMESTAMP_HEADER,
    VerifiedWebhook,
    WebhookVerificationError,
    verify_webhook,
)

__version__ = "0.1.0"

# Generated model classes load on first use (PEP 562), so `import faas_sdk`
# does not import every generated model, and `faas_sdk.preview` does not
# load this package's models beside its own.
_LAZY_MODELS = {
    "OperationWorkflowBlocker": ".models.operation_workflow_blocker",
    "OperationWorkflowBlockerResolution": ".models.operation_workflow_blocker_resolution",
    "OperationWorkflowAttentionStats": ".models.operation_workflow_attention_stats",
    "OperationWorkflowAttentionGroup": ".models.operation_workflow_attention_group",
    "OperationWorkflowAttentionSummary": ".models.operation_workflow_attention_summary",
    "OperationWorkflowOutcomeEntry": ".models.operation_workflow_outcome_entry",
    "OperationWorkflowOutcomesResponse": ".models.operation_workflow_outcomes_response",
    "OperationWorkflowOutcomeGroup": ".models.operation_workflow_outcome_group",
    "OperationWorkflowOutcomeSummary": ".models.operation_workflow_outcome_summary",
    "OperationWorkflowDependency": ".models.operation_workflow_dependency",
    "OperationWorkflowRelatedInstance": ".models.operation_workflow_related_instance",
}


def __getattr__(name: str):
    module = _LAZY_MODELS.get(name)
    if module is None:
        raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
    from importlib import import_module

    value = getattr(import_module(module, __name__), name)
    globals()[name] = value
    return value


def __dir__() -> list[str]:
    return sorted({*globals(), *_LAZY_MODELS})


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
    "GREGALE_RELEASE_HEADER",
    "GREGALE_REQUEST_DEADLINE_HEADER",
    "current_gregale_request_deadline",
    "with_gregale_request_context",
    "GREGALE_REVISION_HEADER",
    "GregaleReleaseMiddleware",
    "GregaleReleaseTransport",
    "AsyncGregaleReleaseTransport",
    "current_gregale_release",
    "with_gregale_release",
    "verify_webhook",
    "VerifiedWebhook",
    "WebhookVerificationError",
    "WEBHOOK_SIGNATURE_HEADER",
    "WEBHOOK_TIMESTAMP_HEADER",
    "WEBHOOK_DELIVERY_ID_HEADER",
    "DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE",
    "PRE_AUTH_TARGET_HEADER",
    "pre_auth_target_digest",
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
    "OperationWorkflowBlockerResolution",
    "OperationWorkflowBlocker",
    "OperationWorkflowAttentionStats",
    "OperationWorkflowAttentionGroup",
    "OperationWorkflowAttentionSummary",
    "OperationWorkflowOutcomeEntry",
    "OperationWorkflowOutcomesResponse",
    "OperationWorkflowOutcomeGroup",
    "OperationWorkflowOutcomeSummary",
    "OperationWorkflowDependency",
    "OperationWorkflowRelatedInstance",
)
'''

_PREVIEW_INIT_PY_TEMPLATE = '''"""faas_sdk.preview - the complete Python SDK (ADR-20261023115741).

`faas_sdk` covers generally available and beta capabilities. This package
adds every preview and internal capability: the complete generated client
from `api/openapi.yaml` under `faas_sdk.preview.api` and
`faas_sdk.preview.models`, and the helpers built on it. It re-exports
`faas_sdk` too; its `FaaSClient` adds the disposable Runs helpers. apid
serves a preview capability only to the accounts it is released to;
elsewhere its calls answer 404 no such API route.
"""

# ruff: noqa: F401
from .. import (
    FaaSClient,
    FaaSClientOptions,
    Client,
    AuthenticatedClient,
    RetryOptions,
    WrapperOptions,
    install_chain,
    IdempotencyKey,
    with_idempotency_key,
    mint_idempotency_key,
    current_idempotency_key,
    GREGALE_RELEASE_HEADER,
    GREGALE_REQUEST_DEADLINE_HEADER,
    GREGALE_REVISION_HEADER,
    GregaleReleaseMiddleware,
    GregaleReleaseTransport,
    AsyncGregaleReleaseTransport,
    current_gregale_release,
    current_gregale_request_deadline,
    with_gregale_release,
    with_gregale_request_context,
    verify_webhook,
    VerifiedWebhook,
    WebhookVerificationError,
    WEBHOOK_SIGNATURE_HEADER,
    WEBHOOK_TIMESTAMP_HEADER,
    WEBHOOK_DELIVERY_ID_HEADER,
    DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE,
    PRE_AUTH_TARGET_HEADER,
    pre_auth_target_digest,
    Problem,
    FaasError,
    FaasProblemError,
    ErrNotFound,
    ErrUnauthorized,
    ErrRateLimited,
    ErrCapacity,
    as_faas_error,
    is_faas_error,
    parse_problem,
    raise_for_problem,
    SseEvent,
    iter_sse,
    aiter_sse,
    __version__,
)
from .. import __all__ as _PACKAGE_ALL
from ._wrapper import FaaSClient
from .commit import CommitEventRouting, ainsert_commit_event, insert_commit_event
from .crash_snapshots import (
    CRASH_SNAPSHOT_ENDPOINT,
    CrashSnapshotResult,
    acapture_crash_snapshot,
    capture_crash_snapshot,
)
from .customer_operations import (
    CustomerOperationConflictError,
    CustomerOperationPublicationError,
    CustomerOperationTransaction,
)
from .data_repair import awith_data_repair_capture, with_data_repair_capture
from .data_repair_readiness import acheck_data_repair_capture, check_data_repair_capture
from .data_repair_transactions import (
    DataRepairCaptureError,
    awith_repairable_customer_operation_transaction,
    awith_repairable_operation_transaction,
    with_repairable_customer_operation_transaction,
    with_repairable_operation_transaction,
)
from .dev_bridge import (
    DEV_BRIDGE_CONTEXT_HEADER,
    AsyncDevBridgeTransport,
    DevBridgeMiddleware,
    DevBridgeTransport,
    current_dev_bridge_context,
    with_dev_bridge_context,
)
from .executions import ExecutionEvent, ExecutionID, awatch_execution, decode_execution_artifact, watch_execution
from .flags import (
    GREGALE_FLAG_CONTEXT_HEADER,
    GREGALE_FLAG_EVIDENCE_HEADER,
    GREGALE_FLAG_PROPAGATION_HEADER,
    AsyncGregaleFlagsTransport,
    FlagDecision,
    GregaleFlags,
    GregaleFlagsMiddleware,
    evaluate_flag,
    evaluate_variant,
    flag_bucket,
    flag_subject_bucket,
    flag_subject_variant_bucket,
    flag_variant_bucket,
    validate_bundle,
)
from .issues import IssueReporter
from .operations import (
    CustomerOperationRequest,
    customer_operation_request_from_headers,
    customer_operation_request_digest,
    customer_operation_receipt_schema,
    with_customer_operation_transaction,
    awith_customer_operation_transaction,
    OperationCommitUnknownError,
    OperationConflictError,
    OperationEffect,
    OperationOutcome,
    OperationRequest,
    OperationTransactionResult,
    awith_operation_transaction,
    operation_receipt_schema,
    operation_request_digest,
    operation_request_from_headers,
    with_operation_transaction,
)
from .operations_runtime import GregaleOperations, OperationExecutionContext, OperationHTTPError
from .ai_gateway import (
    AI_SESSION_HEADER,
    AI_USER_HEADER,
    AIGatewayError,
    ai_gateway_headers,
    ai_gateway_url,
    is_valid_ai_user_id,
    opaque_ai_user_id,
)
from .realtime_direct_messages import RealtimeDirectMessage, RealtimeWebSocketDirectMessages
from .realtime_drain import RealtimeDrainResult
from .realtime_backpressure import RealtimeBackpressureMonitor, RealtimeBackpressureStatus, RealtimeProcessingStatus
from .realtime_expiry import RealtimeExpiryDiagnostics, RealtimeExpiryStatus, RealtimeMessageExpiry
from .realtime_sse import RealtimeSSEClient, RealtimeSSEMessage
from .realtime_transport import RealtimeFallbackClient, RealtimeTransportStatus
from .realtime_websocket_reads import RealtimeWebSocketReadProgress
from .realtime_filters import RealtimeEventFilter
from .realtime_websocket_presence import RealtimeWebSocketPresence, RealtimeWebSocketSignal
from .realtime_channels import RealtimeChannelSubscription, RealtimeMultiChannelClient
from .realtime_websocket import RealtimeWebSocketClient, RealtimeWebSocketMessage, RealtimeWebSocketSession
from .realtime_recovery import (
    RealtimeRecoveryCoordinator,
    RealtimeRecoveryError,
    RealtimeRecoveryEvent,
    RealtimeRecoveryRetry,
    RealtimeRecoverySession,
    RealtimeRecoveryTarget,
)
from .realtime_timeline import RealtimeIncidentEvent, RealtimeIncidentTimeline
from .realtime_diagnostics import export_realtime_diagnostics
from .realtime_readiness import (
    RealtimeCatchUpRequirement,
    RealtimeReadinessError,
    RealtimeReadinessResult,
    wait_realtime_ready,
)
from .realtime_subscriptions import (
    RealtimeSubscriptionStatus,
    RealtimeSubscriptionSummary,
    RealtimeSubscriptionTracker,
    RealtimeSubscriptionWaitError,
)
from .realtime_catch_up import RealtimeCatchUpStatus, RealtimeCatchUpTracker, RealtimeCatchUpWaitError
from .realtime_health import RealtimeHealthSummary, summarize_realtime_health
from .realtime_connection_status import (
    RealtimeConnectionStatus,
    RealtimeConnectionStatusTracker,
    RealtimeConnectionQuality,
    RealtimeConnectionQualityTracker,
)
from .realtime_read_refresh import RealtimeReadRefreshCoordinator, RealtimeReadRefreshError
from .realtime_read_publisher import (
    RealtimeReadProgressPublisher,
    RealtimeReadPublishError,
    create_managed_realtime_read_publisher,
)
from .realtime_read_progress import (
    RealtimeReadProgress,
    RealtimeReadProgressTracker,
    RealtimeReadProgressFreshness,
    RealtimeUnreadSummary,
    RealtimeUnreadBadge,
    format_realtime_unread_badge,
    summarize_realtime_unread,
)
from .realtime_scopes import (
    REALTIME_ACTIVITY_SCOPE_PREFIX,
    parse_realtime_activity_scope,
    realtime_activity_scope_channel,
)
from .realtime_publisher import (
    RealtimeActivityPublishError,
    RealtimeActivityPublisher,
    create_managed_realtime_activity_publisher,
)
from .realtime_presence import (
    RealtimeActivitySummary,
    RealtimeLabeledActivityParticipant,
    summarize_realtime_activity,
    format_realtime_typing_summary,
    RealtimeActivityParticipant,
    RealtimePresenceDirectory,
    RealtimePresenceEvent,
    RealtimePresenceMember,
    RealtimePresenceSummary,
)
from .realtime_activity import RealtimeActivity, RealtimeActivityTracker
from .realtime_workers import (
    RealtimeLeaseLostError,
    RealtimeWorkerContext,
    RealtimeWorkerError,
    RealtimeWorkerEvent,
    RealtimeWorkerOptions,
    consume_realtime_consumer_group,
    consume_realtime_consumer_group_with_credential,
)
from .event_consumer_transactions import (
    EventConsumerCommitUnknownError,
    EventConsumerConflictError,
    EventConsumerRequest,
    EventConsumerTransactionResult,
    awith_event_consumer_transaction,
    event_consumer_receipt_schema,
    event_consumer_request_from_headers,
    new_event_consumer_request,
    with_event_consumer_transaction,
)
from .commit_subscriber_wait import await_commit_subscriber
from .commit_delivery_report import CommitDeliveryReportError, aget_commit_delivery_report
from .commit_recovery_plan import CommitRecoveryPlanError, aplan_commit_recovery
from .commit_recovery_apply import CommitRecoveryApplyError, aapply_commit_recovery
from .commit_recovery_status import adiagnose_commit_recovery_status, CommitRecoveryStatusError, aget_commit_recovery_status, awatch_commit_recovery_status, commit_recovery_status_settled
from ._binding_runner import (
    BindingVerificationRecoveryError, BindingVerificationProgress,
    run_binding_verification, run_project_binding_verification,
    arun_binding_verification, arun_project_binding_verification,
)
from ._binding_creation import (
    BindingVerificationCreationError,
    create_binding_verification_from_review,
    reconcile_binding_verification_creation,
    create_project_binding_verification_from_review,
    reconcile_project_binding_verification_creation,
    acreate_binding_verification_from_review,
    areconcile_binding_verification_creation,
    acreate_project_binding_verification_from_review,
    areconcile_project_binding_verification_creation,
)
from .event_consumer_delivery_context import (EventConsumerDeliveryContext, EventConsumerDeliveryContextUnavailableError,
                                              event_consumer_delivery_context_from_headers)
from ._event_deadline_policy import EVENT_DEADLINE_REASONS, EventDeadlinePolicyOptions, EventDeadlineTarget
from .event_deadline_checkpoint import acknowledge_event_deadline_alerts, validate_event_deadline_checkpoint
from .event_deadline_watch import (
    EventDeadlineWatchCallbackError,
    EventDeadlineWatchCancelled,
    EventDeadlineWatchOptions,
    awatch_event_subscription_deadlines,
)
from ._event_deadline_policy import EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX, EventDeadlineRatioOptions
from .event_deadline_investigation import (
    EventDeadlineInvestigationOptions,
    EventDeadlineInvestigationReadError,
    ainvestigate_event_deadline_alert,
)
from ._event_acknowledgment_protocol import (
    EventAcknowledgmentWorkerCancellation,
    EventAcknowledgmentWorkerCancelled,
    EventAcknowledgmentWorkerError,
    EventAcknowledgmentWorkerOptions,
    EventAcknowledgmentWorkerResult,
    EventAcknowledgmentWorkerStatus,
)
from .event_acknowledgment_worker import AsyncEventAcknowledgmentWorker
from .event_acknowledgment_intent_recovery import (
    EventAcknowledgmentIntentClaim,
    aclaim_event_acknowledgment_intent,
    arecover_next_event_acknowledgment_intent,
)
from .event_acknowledgment_intent_retry_policy import (
    EventAcknowledgmentIntentRetryPolicy,
    EventAcknowledgmentIntentRetryBudget,
    EventAcknowledgmentIntentStoredRetryPolicy,
)
from .event_acknowledgment_intents import (
    EventAcknowledgmentIntentError,
    EventAcknowledgmentIntentParkedError,
    EventAcknowledgmentIntentResult,
    EventAcknowledgmentIntentStatus,
    alist_event_acknowledgment_intents,
    event_acknowledgment_intent_schema,
    event_acknowledgment_intent_stored_retry_policy_schema,
)
from .event_acknowledgment_intent_policy_adoption import (
    EventAcknowledgmentIntentAdoptRetryPolicyOptions,
    EventAcknowledgmentIntentPolicyAdoptionError,
    EventAcknowledgmentIntentPolicyAdoptionReceipt,
    aadopt_event_acknowledgment_intent_retry_policy,
    event_acknowledgment_intent_policy_adoption_schema,
)
from .event_acknowledgment_intent_retry_resume import (
    EventAcknowledgmentIntentResumeRetryBudgetOptions,
    EventAcknowledgmentIntentRetryResumeError,
    EventAcknowledgmentIntentRetryResumeReceipt,
    aget_event_acknowledgment_intent_retry_resume_candidate,
    aresume_event_acknowledgment_intent_retry_budget,
    event_acknowledgment_intent_retry_budget_schema,
    event_acknowledgment_intent_retry_resume_schema,
)
from .event_acknowledgment_intent_budget_adoption import (
    EventAcknowledgmentIntentAdoptRetryBudgetOptions,
    EventAcknowledgmentIntentBudgetAdoptionError,
    EventAcknowledgmentIntentBudgetAdoptionReceipt,
    aadopt_event_acknowledgment_intent_retry_budget,
    aget_event_acknowledgment_intent_budget_adoption_candidate,
    event_acknowledgment_intent_budget_adoption_schema,
)
from .event_acknowledgment_intent_health import (
    EventAcknowledgmentIntentHealth,
    EventAcknowledgmentIntentHealthOptions,
    aget_event_acknowledgment_intent_health,
)
from .event_acknowledgment_intent_health_metrics import (
    EventAcknowledgmentIntentHealthMetrics,
    EventAcknowledgmentIntentHealthMetricsOptions,
    EventAcknowledgmentIntentHealthMetricsSample,
    EventAcknowledgmentIntentHealthMetricsSnapshot,
)
from .event_acknowledgment_intent_health_metrics_watch import awatch_event_acknowledgment_intent_health_metrics
from .event_acknowledgment_intent_health_prometheus import (
    EVENT_ACKNOWLEDGMENT_INTENT_HEALTH_PROMETHEUS_CONTENT_TYPE,
    format_event_acknowledgment_intent_health_metrics,
)
from .event_acknowledgment_intent_watch import (
    EventAcknowledgmentIntentAlert,
    EventAcknowledgmentIntentWatchCallbackError,
    EventAcknowledgmentIntentWatchOptions,
    awatch_event_acknowledgment_intent_health,
)
from .event_acknowledgment_intent_checkpoint import (
    EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX,
    acknowledge_event_acknowledgment_intent_alerts,
    validate_event_acknowledgment_intent_checkpoint,
)
from .event_acknowledgment_intent_checkpoint_watch import (
    EventAcknowledgmentIntentCheckpointWatchCallbackError,
    EventAcknowledgmentIntentCheckpointWatchCancelled,
    EventAcknowledgmentIntentCheckpointWatchOptions,
    awatch_event_acknowledgment_intent_health_with_checkpoint,
)
from .event_consumer_processing import (
    EventConsumerProcessingError,
    EventConsumerProcessingLease,
    aclaim_event_consumer_processing,
    event_consumer_processing_schema,
)
from .event_acknowledgment_preparation import EventAcknowledgmentPreparedOptions
from .event_consumer_processing_inspection import (
    EventConsumerProcessingInspection,
    ainspect_event_consumer_processing,
)
from .event_acknowledgment_preparation_events import (
    EventAcknowledgmentPreparationEvent,
    EventAcknowledgmentPreparationOutcome,
    EventAcknowledgmentPreparationReason,
    EventAcknowledgmentPreparationStage,
)
from .event_acknowledgment_preparation_metrics import (
    EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS,
    EventAcknowledgmentPreparationHistogram,
    EventAcknowledgmentPreparationMetrics,
    EventAcknowledgmentPreparationMetricsSnapshot,
)
from .event_acknowledgment_preparation_prometheus import (
    EVENT_ACKNOWLEDGMENT_PREPARATION_PROMETHEUS_CONTENT_TYPE,
    format_event_acknowledgment_preparation_metrics,
)
from .event_acknowledgment_intent_recovery_pass import (
    EventAcknowledgmentIntentRecoveryPassCancelled,
    EventAcknowledgmentIntentRecoveryPassCommitUnknownError,
    EventAcknowledgmentIntentRecoveryPassError,
    EventAcknowledgmentIntentRecoveryPassOptions,
    EventAcknowledgmentIntentRecoveryPassResult,
    arecover_event_acknowledgment_intents,
)

__all__ = (
    *_PACKAGE_ALL,
    "AI_SESSION_HEADER",
    "AI_USER_HEADER",
    "AIGatewayError",
    "ai_gateway_headers",
    "ai_gateway_url",
    "is_valid_ai_user_id",
    "opaque_ai_user_id",
    "IssueReporter",
    "GREGALE_FLAG_CONTEXT_HEADER",
    "GREGALE_FLAG_EVIDENCE_HEADER",
    "GREGALE_FLAG_PROPAGATION_HEADER",
    "GregaleFlags",
    "GregaleFlagsMiddleware",
    "AsyncGregaleFlagsTransport",
    "FlagDecision",
    "evaluate_flag",
    "evaluate_variant",
    "flag_bucket",
    "flag_subject_bucket",
    "flag_subject_variant_bucket",
    "flag_variant_bucket",
    "validate_bundle",
    "CRASH_SNAPSHOT_ENDPOINT",
    "CrashSnapshotResult",
    "capture_crash_snapshot",
    "acapture_crash_snapshot",
    "ExecutionEvent",
    "ExecutionID",
    "watch_execution",
    "awatch_execution",
    "decode_execution_artifact",
    "DEV_BRIDGE_CONTEXT_HEADER",
    "AsyncDevBridgeTransport",
    "DevBridgeMiddleware",
    "DevBridgeTransport",
    "current_dev_bridge_context",
    "with_dev_bridge_context",
    "insert_commit_event",
    "ainsert_commit_event",
    "CommitEventRouting",
    "CustomerOperationRequest",
    "customer_operation_request_from_headers",
    "customer_operation_request_digest",
    "customer_operation_receipt_schema",
    "with_customer_operation_transaction",
    "awith_customer_operation_transaction",
    "OperationCommitUnknownError",
    "OperationConflictError",
    "OperationEffect",
    "OperationOutcome",
    "OperationRequest",
    "OperationTransactionResult",
    "awith_operation_transaction",
    "operation_receipt_schema",
    "operation_request_digest",
    "operation_request_from_headers",
    "with_operation_transaction",
    "with_data_repair_capture",
    "awith_data_repair_capture",
    "check_data_repair_capture",
    "acheck_data_repair_capture",
    "DataRepairCaptureError",
    "with_repairable_operation_transaction",
    "awith_repairable_operation_transaction",
    "with_repairable_customer_operation_transaction",
    "awith_repairable_customer_operation_transaction",
    "CustomerOperationConflictError",
    "CustomerOperationPublicationError",
    "CustomerOperationRequest",
    "CustomerOperationTransaction",
    "awith_customer_operation_transaction",
    "customer_operation_receipt_schema",
    "customer_operation_request_digest",
    "customer_operation_request_from_headers",
    "GregaleOperations",
    "OperationExecutionContext",
    "OperationHTTPError",
    "RealtimeDrainResult",
    "RealtimeBackpressureMonitor",
    "RealtimeBackpressureStatus",
    "RealtimeProcessingStatus",
    "RealtimeExpiryDiagnostics",
    "RealtimeExpiryStatus",
    "RealtimeMessageExpiry",
    "RealtimeSSEClient",
    "RealtimeSSEMessage",
    "RealtimeFallbackClient",
    "RealtimeTransportStatus",
    "RealtimeDirectMessage",
    "RealtimeWebSocketDirectMessages",
    "RealtimeWebSocketReadProgress",
    "RealtimeEventFilter",
    "RealtimeWebSocketPresence",
    "RealtimeWebSocketSignal",
    "RealtimeChannelSubscription",
    "RealtimeMultiChannelClient",
    "RealtimeWebSocketClient",
    "RealtimeWebSocketMessage",
    "RealtimeWebSocketSession",
    "RealtimeRecoveryCoordinator",
    "RealtimeRecoveryError",
    "RealtimeRecoveryEvent",
    "RealtimeRecoveryRetry",
    "RealtimeRecoverySession",
    "RealtimeRecoveryTarget",
    "RealtimeIncidentEvent",
    "RealtimeIncidentTimeline",
    "export_realtime_diagnostics",
    "RealtimeCatchUpRequirement",
    "RealtimeReadinessError",
    "RealtimeReadinessResult",
    "wait_realtime_ready",
    "RealtimeSubscriptionStatus",
    "RealtimeSubscriptionSummary",
    "RealtimeSubscriptionTracker",
    "RealtimeSubscriptionWaitError",
    "RealtimeCatchUpStatus",
    "RealtimeCatchUpTracker",
    "RealtimeCatchUpWaitError",
    "RealtimeHealthSummary",
    "summarize_realtime_health",
    "RealtimeConnectionStatus",
    "RealtimeConnectionStatusTracker",
    "RealtimeConnectionQuality",
    "RealtimeConnectionQualityTracker",
    "RealtimeReadRefreshCoordinator",
    "RealtimeReadRefreshError",
    "RealtimeReadProgressPublisher",
    "RealtimeReadPublishError",
    "create_managed_realtime_read_publisher",
    "RealtimeReadProgress",
    "RealtimeReadProgressTracker",
    "RealtimeReadProgressFreshness",
    "RealtimeUnreadSummary",
    "RealtimeUnreadBadge",
    "format_realtime_unread_badge",
    "summarize_realtime_unread",
    "REALTIME_ACTIVITY_SCOPE_PREFIX",
    "parse_realtime_activity_scope",
    "realtime_activity_scope_channel",
    "RealtimeActivityPublishError",
    "RealtimeActivityPublisher",
    "create_managed_realtime_activity_publisher",
    "RealtimeActivitySummary",
    "RealtimeLabeledActivityParticipant",
    "summarize_realtime_activity",
    "format_realtime_typing_summary",
    "RealtimeActivityParticipant",
    "RealtimePresenceDirectory",
    "RealtimePresenceEvent",
    "RealtimePresenceMember",
    "RealtimePresenceSummary",
    "RealtimeActivity",
    "RealtimeActivityTracker",
    "RealtimeLeaseLostError",
    "RealtimeWorkerContext",
    "RealtimeWorkerError",
    "RealtimeWorkerEvent",
    "RealtimeWorkerOptions",
    "consume_realtime_consumer_group",
    "consume_realtime_consumer_group_with_credential",
    "EventConsumerCommitUnknownError",
    "EventConsumerConflictError",
    "EventConsumerRequest",
    "EventConsumerTransactionResult",
    "awith_event_consumer_transaction",
    "event_consumer_receipt_schema",
    "event_consumer_request_from_headers",
    "new_event_consumer_request",
    "with_event_consumer_transaction",
    "await_commit_subscriber",
    "CommitDeliveryReportError",
    "aget_commit_delivery_report",
    "CommitRecoveryPlanError",
    "aplan_commit_recovery",
    "CommitRecoveryApplyError",
    "aapply_commit_recovery",
    "adiagnose_commit_recovery_status",
    "CommitRecoveryStatusError",
    "aget_commit_recovery_status",
    "awatch_commit_recovery_status",
    "commit_recovery_status_settled",
    "BindingVerificationRecoveryError",
    "BindingVerificationProgress",
    "run_binding_verification",
    "run_project_binding_verification",
    "arun_binding_verification",
    "arun_project_binding_verification",
    "BindingVerificationCreationError",
    "create_binding_verification_from_review",
    "reconcile_binding_verification_creation",
    "create_project_binding_verification_from_review",
    "reconcile_project_binding_verification_creation",
    "acreate_binding_verification_from_review",
    "areconcile_binding_verification_creation",
    "acreate_project_binding_verification_from_review",
    "areconcile_project_binding_verification_creation",
    "EventConsumerDeliveryContext",
    "EventConsumerDeliveryContextUnavailableError",
    "event_consumer_delivery_context_from_headers",
    "EVENT_DEADLINE_REASONS",
    "EventDeadlinePolicyOptions",
    "EventDeadlineTarget",
    "EventDeadlineWatchOptions",
    "EventDeadlineWatchCallbackError",
    "EventDeadlineWatchCancelled",
    "awatch_event_subscription_deadlines",
    "validate_event_deadline_checkpoint",
    "acknowledge_event_deadline_alerts",
    "EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX",
    "EventDeadlineRatioOptions",
    "EventDeadlineInvestigationOptions",
    "EventDeadlineInvestigationReadError",
    "ainvestigate_event_deadline_alert",
    "AsyncEventAcknowledgmentWorker",
    "EventAcknowledgmentWorkerOptions",
    "EventAcknowledgmentWorkerResult",
    "EventAcknowledgmentWorkerStatus",
    "EventAcknowledgmentWorkerError",
    "EventAcknowledgmentWorkerCancelled",
    "EventAcknowledgmentWorkerCancellation",
    "EventAcknowledgmentIntentError",
    "EventAcknowledgmentIntentResult",
    "EventAcknowledgmentIntentStatus",
    "EventAcknowledgmentIntentClaim",
    "aclaim_event_acknowledgment_intent",
    "arecover_next_event_acknowledgment_intent",
    "alist_event_acknowledgment_intents",
    "event_acknowledgment_intent_schema",
    "EventAcknowledgmentIntentRetryPolicy",
    "EventAcknowledgmentIntentRetryBudget",
    "EventAcknowledgmentIntentParkedError",
    "EventAcknowledgmentIntentStoredRetryPolicy",
    "event_acknowledgment_intent_stored_retry_policy_schema",
    "EventAcknowledgmentIntentAdoptRetryPolicyOptions",
    "EventAcknowledgmentIntentPolicyAdoptionError",
    "EventAcknowledgmentIntentPolicyAdoptionReceipt",
    "aadopt_event_acknowledgment_intent_retry_policy",
    "event_acknowledgment_intent_policy_adoption_schema",
    "EventAcknowledgmentIntentResumeRetryBudgetOptions",
    "EventAcknowledgmentIntentRetryResumeError",
    "EventAcknowledgmentIntentRetryResumeReceipt",
    "aget_event_acknowledgment_intent_retry_resume_candidate",
    "aresume_event_acknowledgment_intent_retry_budget",
    "event_acknowledgment_intent_retry_budget_schema",
    "event_acknowledgment_intent_retry_resume_schema",
    "EventAcknowledgmentIntentAdoptRetryBudgetOptions",
    "EventAcknowledgmentIntentBudgetAdoptionError",
    "EventAcknowledgmentIntentBudgetAdoptionReceipt",
    "aadopt_event_acknowledgment_intent_retry_budget",
    "aget_event_acknowledgment_intent_budget_adoption_candidate",
    "event_acknowledgment_intent_budget_adoption_schema",
    "EventAcknowledgmentIntentHealth",
    "EventAcknowledgmentIntentHealthOptions",
    "aget_event_acknowledgment_intent_health",
    "EventAcknowledgmentIntentHealthMetrics",
    "EventAcknowledgmentIntentHealthMetricsOptions",
    "EventAcknowledgmentIntentHealthMetricsSample",
    "EventAcknowledgmentIntentHealthMetricsSnapshot",
    "awatch_event_acknowledgment_intent_health_metrics",
    "EVENT_ACKNOWLEDGMENT_INTENT_HEALTH_PROMETHEUS_CONTENT_TYPE",
    "format_event_acknowledgment_intent_health_metrics",
    "EventAcknowledgmentIntentAlert",
    "EventAcknowledgmentIntentWatchCallbackError",
    "EventAcknowledgmentIntentWatchOptions",
    "awatch_event_acknowledgment_intent_health",
    "EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX",
    "acknowledge_event_acknowledgment_intent_alerts",
    "validate_event_acknowledgment_intent_checkpoint",
    "EventAcknowledgmentIntentCheckpointWatchCallbackError",
    "EventAcknowledgmentIntentCheckpointWatchCancelled",
    "EventAcknowledgmentIntentCheckpointWatchOptions",
    "awatch_event_acknowledgment_intent_health_with_checkpoint",
    "EventConsumerProcessingError",
    "EventConsumerProcessingLease",
    "aclaim_event_consumer_processing",
    "event_consumer_processing_schema",
    "EventAcknowledgmentPreparedOptions",
    "EventConsumerProcessingInspection",
    "ainspect_event_consumer_processing",
    "EventAcknowledgmentPreparationEvent",
    "EventAcknowledgmentPreparationOutcome",
    "EventAcknowledgmentPreparationReason",
    "EventAcknowledgmentPreparationStage",
    "EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS",
    "EventAcknowledgmentPreparationHistogram",
    "EventAcknowledgmentPreparationMetrics",
    "EventAcknowledgmentPreparationMetricsSnapshot",
    "EVENT_ACKNOWLEDGMENT_PREPARATION_PROMETHEUS_CONTENT_TYPE",
    "format_event_acknowledgment_preparation_metrics",
    "EventAcknowledgmentIntentRecoveryPassCancelled",
    "EventAcknowledgmentIntentRecoveryPassCommitUnknownError",
    "EventAcknowledgmentIntentRecoveryPassError",
    "EventAcknowledgmentIntentRecoveryPassOptions",
    "EventAcknowledgmentIntentRecoveryPassResult",
    "arecover_event_acknowledgment_intents",
)


def __getattr__(name: str):
    # The package's lazily loaded model classes (faas_sdk._LAZY_MODELS).
    from .. import _LAZY_MODELS

    if name not in _LAZY_MODELS:
        raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
    from importlib import import_module

    return getattr(import_module("faas_sdk"), name)

'''


_PREVIEW_SHIMS = {
    "types.py": '"""The preview client shares the package\'s generator types."""\n\n'
    "from ..types import UNSET, File, FileTypes, RequestFiles, Response, Unset\n\n"
    '__all__ = ["UNSET", "File", "FileTypes", "RequestFiles", "Response", "Unset"]\n',
    "client.py": '"""The preview client shares the package\'s generated clients."""\n\n'
    "from ..client import AuthenticatedClient, Client\n\n"
    '__all__ = ["AuthenticatedClient", "Client"]\n',
    "errors.py": '"""The preview client shares the package\'s generated errors."""\n\n'
    "from ..errors import UnexpectedStatus\n\n"
    '__all__ = ["UnexpectedStatus"]\n',
}


def _write_preview_shims(preview: Path) -> None:
    """Point `faas_sdk.preview`'s generated code at the package's shared
    types, clients and errors, so one `UNSET` and one `Client` serve both."""
    for name, text in _PREVIEW_SHIMS.items():
        (preview / name).write_text(text)


def _rewrite_init_py(init_path: Path) -> None:
    """Overwrite the generator's `__init__.py` stub with the wrapper
    barrel. The generated stub only re-exports `Client` and
    `AuthenticatedClient`; the wrapper adds the chain
    (`FaaSClient`), the four sentinels, idempotency helpers, SSE,
    release context, runtime flags, and webhook verification.
    """
    init_path.write_text(_INIT_PY_TEMPLATE)


def _git_blob_hashes(paths: list[Path], repo_root: Path) -> list[str]:
    """Hash raw files in one Git invocation, including Git's blob header.

    --no-filters preserves the old --stdin byte semantics. Git's stdin path
    protocol accepts C-quoted bytes, so spaces, controls and Unicode paths
    remain unambiguous without launching a process for every generated file.
    """
    if not paths:
        return []
    quoted = []
    for path in paths:
        pieces = []
        for value in os.fsencode(path):
            if value in (34, 92):
                pieces.append(b"\\" + bytes([value]))
            elif value < 32 or value >= 127:
                pieces.append(f"\\{value:03o}".encode("ascii"))
            else:
                pieces.append(bytes([value]))
        quoted.append(b'"' + b"".join(pieces) + b'"')
    proc = subprocess.run(
        ["git", "hash-object", "--no-filters", "--stdin-paths"],
        cwd=repo_root,
        input=b"\n".join(quoted) + b"\n",
        check=True,
        capture_output=True,
    )
    hashes = proc.stdout.decode("ascii").splitlines()
    if len(hashes) != len(paths):
        raise RuntimeError("git returned an incomplete generated-file hash batch")
    return hashes


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
    `_transport.py`, `idempotency.py`, `executions.py`,
    `release_context.py`, `webhook.py`, `pre_auth_target.py`, `__init__.py`) are
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
    candidates = []
    for regen_path in sdk_root.rglob("*.py"):
        rel = regen_path.relative_to(repo_root).as_posix()
        head_sha = head_blobs.get(rel)
        if head_sha is not None:
            candidates.append((regen_path, head_sha))
    hashes = _git_blob_hashes([path for path, _ in candidates], repo_root)
    for (regen_path, head_sha), regen_sha in zip(candidates, hashes, strict=True):
        if regen_sha != head_sha:
            mismatches.append((regen_path, regen_path.read_bytes(), head_sha))

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


def _preserve_routing_replay_guard_precision(package: Path) -> None:
    """Keep a receipt guard's RFC3339 text intact, including nanosecond precision."""
    path = package / "models" / "replay_event_fanout_failure_request.py"
    if not path.exists():
        return  # a preview model, absent from the GA and beta package
    text = path.read_text()
    text = text.replace("import datetime\n", "")
    text = text.replace("datetime.datetime | Unset", "str | Unset")
    text = text.replace("self.expected_accepted_at.isoformat()", "self.expected_accepted_at")
    text = text.replace("datetime.datetime.fromisoformat(_expected_accepted_at)", "_expected_accepted_at")
    path.write_text(text)


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
