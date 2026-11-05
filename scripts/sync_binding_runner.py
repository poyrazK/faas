#!/usr/bin/env python3
"""Keep the leaf Go SDK runner identical to the CLI's canonical runner."""
from pathlib import Path
import argparse
parser = argparse.ArgumentParser()
parser.add_argument("--check", action="store_true")
args = parser.parse_args()
root = Path(__file__).resolve().parents[1]
for name in ("runner.go", "runner_test.go", "test_context_test.go", "diagnostics.go", "diagnostics_test.go", "create.go", "create_test.go", "status_retry.go", "status_retry_test.go", "qualification.go", "qualification_test.go", "reconcile_test.go", "create_reconcile.go", "create_reconcile_test.go"):
    source = (root / "pkg/bindingrun" / name).read_text().replace(
        '"github.com/onebox-faas/faas/pkg/api"',
        '"github.com/poyrazK/faas/sdk/go/internal/api"',
    ).replace('"../../sdk/binding-runner-tests/contracts.json"', '"../../../binding-runner-tests/contracts.json"')
    target = root / "sdk/go/internal/bindingrun" / name
    if args.check:
        if target.read_text() != source:
            raise SystemExit("Go SDK binding runner differs; run scripts/sync_binding_runner.py")
    else:
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(source)

# These client budgets share the root API limits table.
import re
for name in ("BindingVerificationRunnerDefaultConcurrency", "BindingVerificationRunnerMaxConcurrency", "ProjectReleaseSetMaxMembers", "BindingVerificationRunMaxSteps", "BindingVerificationRunMaxTTLSeconds", "BindingVerificationRunnerDiagnosticsTimeoutMilliseconds", "BindingVerificationRunnerDefaultStatusReadRetries", "BindingVerificationRunnerMaxStatusReadRetries", "BindingVerificationRunnerStatusReadRetryInitialMilliseconds", "BindingVerificationRunnerStatusReadRetryMaxMilliseconds", "BindingVerificationRunListDefaultLimit", "BindingVerificationRunListMaxLimit", "BindingVerificationRunListCursorMaxBytes", "BindingVerificationQualificationMaxConditions", "BindingVerificationCreationReconcileTimeoutMilliseconds"):
    values = []
    for path in (root / "pkg/api/limits.go", root / "sdk/go/internal/api/limits.go"):
        match = re.search(r"\b" + name + r"\s*=\s*(\d+)\b", path.read_text())
        values.append(match.group(1) if match else None)
    if None in values or values[0] != values[1]:
        raise SystemExit("SDK runner limit differs from pkg/api/limits.go: " + name)

source = (root / "pkg/api/binding_verification_run_list.go").read_text()
target = root / "sdk/go/internal/api/binding_verification_run_list.go"
if args.check:
    if target.read_text() != source:
        raise SystemExit("Go SDK receipt list client differs from pkg/api")
else:
    target.write_text(source)

source = (root / "pkg/api/binding_verification_qualification.go").read_text()
target = root / "sdk/go/internal/api/binding_verification_qualification.go"
if args.check:
    if target.read_text() != source:
        raise SystemExit("Go SDK qualification DTO differs from pkg/api")
else:
    target.write_text(source)

# Creation recovery mirrors the same central budget in the other SDKs.
limit = re.search(r"\bBindingVerificationCreationReconcileTimeoutMilliseconds\s*=\s*(\d+)", (root / "pkg/api/limits.go").read_text())
node = re.search(r"\bcreationRecoveryMilliseconds\s*=\s*(\d+)", (root / "sdk/node/src/binding-creation.ts").read_text())
python = re.search(r"\b_CREATION_RECOVERY_SECONDS\s*=\s*(\d+)", (root / "sdk/python/faas_sdk/preview/_binding_creation.py").read_text())
if not limit or not node or not python or int(limit[1]) != int(node[1]) or int(limit[1]) != 1000 * int(python[1]):
    raise SystemExit("Creation recovery SDK time budgets differ from pkg/api/limits.go")
