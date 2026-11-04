"""ADR-20261023115741: `faas_sdk` exposes generally available and beta
capabilities; `faas_sdk.preview` adds the preview ones."""

import importlib.util

import faas_sdk
import faas_sdk.preview as preview


def test_default_package_leaves_preview_helpers_out() -> None:
    for name in ("FaaSClient", "verify_webhook", "ErrNotFound", "with_idempotency_key"):
        assert name in faas_sdk.__all__, name
    for name in ("GregaleOperations", "insert_commit_event", "watch_execution"):
        assert name not in faas_sdk.__all__, name
        assert name in preview.__all__, name
        assert getattr(preview, name)


def test_only_the_preview_client_runs_disposable_executions() -> None:
    assert not hasattr(faas_sdk.FaaSClient, "run_execution")
    assert hasattr(preview.FaaSClient, "run_execution")
    assert issubclass(preview.FaaSClient, faas_sdk.FaaSClient)


def test_generated_trees_split_by_maturity() -> None:
    assert importlib.util.find_spec("faas_sdk.api.apps") is not None
    assert importlib.util.find_spec("faas_sdk.api.realtime") is None
    assert importlib.util.find_spec("faas_sdk.preview.api.realtime") is not None


def test_preview_shares_the_generator_types_and_clients() -> None:
    from faas_sdk.client import Client
    from faas_sdk.preview.client import Client as PreviewClient
    from faas_sdk.preview.types import UNSET as preview_unset
    from faas_sdk.types import UNSET

    assert preview_unset is UNSET
    assert PreviewClient is Client


def test_root_model_reexports_load_lazily() -> None:
    from faas_sdk import OperationWorkflowBlocker
    from faas_sdk.models.operation_workflow_blocker import OperationWorkflowBlocker as Direct

    assert OperationWorkflowBlocker is Direct
    assert preview.OperationWorkflowBlocker is Direct
