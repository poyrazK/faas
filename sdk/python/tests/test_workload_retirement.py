"""Reviewed workload identities and retirement progress survive SDK transport."""

from faas_sdk.preview.models.environment_definition import EnvironmentDefinition
from faas_sdk.preview.models.environment_workload_retirement import EnvironmentWorkloadRetirement


def test_reviewed_workload_recovery_round_trip():
    original = "11111111-2222-4333-8444-555555555555"
    body = {
        "api_version": "gregale.dev/environment/v1",
        "project": "shop",
        "environment": "staging",
        "workload_pruning_policy": "retain",
        "workload_recoveries": {"worker": original},
        "workloads": {"worker": {"app": "orders-worker"}},
    }
    assert EnvironmentDefinition.from_dict(body).to_dict() == body


def test_retirement_keeps_original_and_recovery_generation_separate():
    original = "11111111-2222-4333-8444-555555555555"
    body = {
        "id": original,
        "source_id": original,
        "environment_id": original,
        "app_id": original,
        "resource": "workload/worker",
        "revision_id": original,
        "generation": 3,
        "phase": "recovering",
        "requested_at": "2026-10-15T10:00:00+00:00",
        "retired_at": "2026-10-15T10:02:00+00:00",
        "recovery_revision_id": original,
        "recovery_generation": 4,
    }
    assert EnvironmentWorkloadRetirement.from_dict(body).to_dict() == body
