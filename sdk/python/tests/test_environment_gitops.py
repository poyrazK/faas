"""Exercise generated GitOps contracts through the public transport wrapper."""

import json
from typing import cast

import httpx
import pytest

from faas_sdk import FaaSClient
from faas_sdk.api.projects import (
    approve_environment_git_revision,
    get_environment_git_ops,
    preview_environment_git_revision,
    remove_environment_git_ops_override,
)
from faas_sdk.models import (
    AppManifest,
    ApproveEnvironmentGitRevisionRequest,
    ApproveEnvironmentGitRevisionResponse,
    CreateEnvironmentGitSourceRequest,
    EnvironmentGitOpsExplanation,
    EnvironmentGitOpsStatusResponse,
    EnvironmentWorkloadActivationEvidence,
    PreviewEnvironmentGitRevisionRequest,
    PreviewEnvironmentGitRevisionResponse,
    RemoveEnvironmentGitOpsOverrideRequest,
)
from faas_sdk.models.service_binding_transport import ServiceBindingTransport


def test_gitops_evidence_commit_is_optional_and_independent_of_ref() -> None:
    body = {
        "graph_id": "11111111-2222-4333-8444-555555555555",
        "source_id": "11111111-2222-4333-8444-555555555555",
        "environment_id": "11111111-2222-4333-8444-555555555555",
        "revision_id": "11111111-2222-4333-8444-555555555555",
        "definition_digest": "a" * 64,
        "generation": 2,
        "intent_version": 3,
        "plan_hash": "b" * 64,
        "graph_phase": "prepared",
        "artifacts_prepared": True,
        "candidates": 1,
        "retained_workloads_recorded": 0,
        "captures_recorded": 1,
        "guest_config_acknowledgements_recorded": 1,
        "restores_recorded": 1,
        "smokes_recorded": 1,
        "job_smokes_recorded": 0,
        "framework_ready_acknowledgements_recorded": 1,
        "qualified": True,
        "activated": True,
        "serving": True,
        "blocking_reasons": [],
    }
    assert "commit_sha" not in EnvironmentWorkloadActivationEvidence.from_dict(body).to_dict()
    body["commit_sha"] = "c" * 40
    evidence = EnvironmentWorkloadActivationEvidence.from_dict(body)
    assert evidence.commit_sha == "c" * 40
    assert evidence.to_dict() == body


@pytest.mark.parametrize("transport", ["http", "https"])
def test_app_manifest_service_binding_transport_round_trip(transport: str) -> None:
    manifest = AppManifest.from_dict({"entrypoint": ["/app"], "service_binding_transport": transport})

    assert manifest.service_binding_transport == transport
    assert manifest.to_dict()["service_binding_transport"] == transport


def test_app_manifest_service_binding_transport_rejects_invalid_wire_values() -> None:
    with pytest.raises(TypeError, match="Unexpected value"):
        AppManifest.from_dict({"entrypoint": ["/app"], "service_binding_transport": "ftp"})

    manifest = AppManifest(entrypoint=["/app"], service_binding_transport=cast(ServiceBindingTransport, "ftp"))
    with pytest.raises(TypeError, match="Unexpected value"):
        manifest.to_dict()


def test_gitlab_source_request_round_trip() -> None:
    request = CreateEnvironmentGitSourceRequest.from_dict(
        {
            "provider": "gitlab",
            "repository": "team/subgroup/service",
            "ref": "refs/heads/main",
            "manifest_path": "environments/production.yaml",
        }
    )
    assert request.to_dict()["provider"] == "gitlab"
    assert request.to_dict()["repository"] == "team/subgroup/service"


def test_gitops_explanation_preserves_unknown_serving_and_scoped_actions() -> None:
    body = {
        "read_only": True,
        "assessed_at": "2026-10-16T12:00:00+00:00",
        "status": "blocked",
        "summary": "Waiting for current serving proof.",
        "source_id": "source",
        "generation": 3,
        "intent_version": 5,
        "source_freshness": "stale",
        "approved_revision_id": "approved",
        "applied_revision_id": "historical",
        "workloads": [
            {
                "resource": "workload/report",
                "app_id": "app",
                "execution_mode": "job",
                "preparation": "complete",
                "qualification": "complete",
                "activation": "complete",
                "serving": "unknown",
                "blocking_reasons": ["environment_serving_evidence_missing"],
            }
        ],
        "issues": [
            {
                "resource": "workload/report",
                "stage": "serving",
                "code": "scheduled_occurrence_unproven",
                "message": "No current serving proof.",
                "actor": "customer",
                "action": "Inspect current scheduled task logs.",
            }
        ],
    }
    explanation = EnvironmentGitOpsExplanation.from_dict(body)
    assert explanation.to_dict() == body
    assert "serving_revision_id" not in explanation.to_dict()
    assert explanation.workloads[0].serving == "unknown"


def test_reviewed_authority_and_override_identity() -> None:
    sha, digest = "a" * 40, "b" * 64
    calls: list[httpx.Request] = []
    definition = {
        "api_version": "gregale.dev/environment/v1",
        "project": "shop",
        "environment": "production",
        "queue_pruning_policy": "retain",
        "workloads": {
            "api": {
                "app": "shop-api",
                "queue_bindings": {"orders": {"queue_name": "orders", "workload_class": "worker"}},
                "queue_smoke": {"orders": {"payload": {"type": "qualification-probe", "order_id": 42}}},
                "queue_recoveries": {"orders": "11111111-2222-4333-8444-555555555555"},
            },
            "daily-report": {
                "runtime": {"entrypoint": ["node", "scripts/report.js"], "execution_mode": "job"},
                "job_smoke": {"command": ["node", "scripts/smoke.js"], "timeout_seconds": 30},
                "schedule": {"cron": "0 3 * * *", "timezone": "Europe/Istanbul"},
            },
        },
    }
    source = {
        "id": "source",
        "account_id": "account",
        "project_id": "project",
        "environment_id": "environment",
        "environment": "production",
        "suspended": False,
        "generation": 8,
        "intent_version": 1,
        "created_at": "2026-09-30T00:00:00Z",
        "updated_at": "2026-09-30T00:00:00Z",
        "approved_revision_id": "approved",
        "source_commit_sha": "c" * 40,
        "source_definition_digest": digest,
        "source_verified_at": "2026-10-01T00:00:00Z",
        "source_error_code": "environment_git_source_unavailable",
        "source": {
            "provider": "github",
            "repository_id": 123,
            "installation_id": 42,
            "repository": "example/shop",
            "ref": "refs/heads/main",
            "manifest_path": "environment.yaml",
            "mode": "report",
            "approval_policy": "manual",
            "prune": False,
        },
    }

    def handle(request: httpx.Request) -> httpx.Response:
        calls.append(request)
        if request.url.path.endswith("/preview"):
            return httpx.Response(
                200, json={"commit_sha": sha, "definition_digest": digest, "generation": 7, "definition": definition}
            )
        if request.url.path.endswith("/approve"):
            return httpx.Response(
                202,
                json={
                    "source": source,
                    "revision": {
                        "id": "revision",
                        "source_id": "source",
                        "commit_sha": sha,
                        "definition_digest": digest,
                        "definition": definition,
                        "approved_by": "account",
                        "approved_at": "2026-09-30T00:00:00Z",
                    },
                },
            )
        if request.url.path.endswith("/gitops"):
            return httpx.Response(
                200,
                json={
                    "source": source,
                    "runs": [],
                    "approval": {
                        "id": "11111111-1111-1111-1111-111111111111",
                        "source_id": "22222222-2222-2222-2222-222222222222",
                        "revision_id": "33333333-3333-3333-3333-333333333333",
                        "generation": 8,
                        "definition_digest": digest,
                        "recorded_at": "2026-10-01T00:00:00Z",
                        "evidence": {
                            "qualified": True,
                            "profile": "reviewed_merge/v1",
                            "reviewed_definition_digest": digest,
                            "pull_request_id": 1234,
                            "pull_request_number": 7,
                            "author_id": 10,
                            "head_sha": "d" * 40,
                            "merged_at": "2026-09-30T23:00:00Z",
                            "checked_at": "2026-10-01T00:00:00Z",
                            "policy": {
                                "qualified": True,
                                "profile": "classic_reviewed_branch/v1",
                                "installation_id": 42,
                                "repository_id": 123,
                                "repository": "example/shop",
                                "branch": "main",
                                "commit_sha": sha,
                                "policy_digest": "e" * 64,
                                "required_review_count": 1,
                                "checked_at": "2026-10-01T00:00:00Z",
                            },
                            "reviews": [
                                {
                                    "id": 1,
                                    "reviewer_id": 11,
                                    "reviewer": "reviewer",
                                    "head_sha": "d" * 40,
                                    "submitted_at": "2026-09-30T22:00:00Z",
                                }
                            ],
                        },
                    },
                },
            )
        return httpx.Response(204)

    client = FaaSClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    )
    try:
        review = preview_environment_git_revision.sync(
            "my project", "production", client=client.inner, body=PreviewEnvironmentGitRevisionRequest(commit_sha=sha)
        )
        assert isinstance(review, PreviewEnvironmentGitRevisionResponse)
        assert review.definition.to_dict() == definition
        assert review.definition.workloads["daily-report"].job_smoke.to_dict() == {
            "command": ["node", "scripts/smoke.js"],
            "timeout_seconds": 30,
        }
        assert review.definition.workloads["daily-report"].schedule.to_dict() == {
            "cron": "0 3 * * *",
            "timezone": "Europe/Istanbul",
        }
        approved = approve_environment_git_revision.sync_detailed(
            "my project",
            "production",
            client=client.inner,
            body=ApproveEnvironmentGitRevisionRequest(
                commit_sha=review.commit_sha,
                definition_digest=review.definition_digest,
                expected_generation=review.generation,
            ),
        )
        assert approved.status_code == 202
        assert isinstance(approved.parsed, ApproveEnvironmentGitRevisionResponse)
        assert approved.parsed.source.source_commit_sha == "c" * 40
        assert approved.parsed.source.source_definition_digest == digest
        assert approved.parsed.source.source_verified_at.isoformat() == "2026-10-01T00:00:00+00:00"
        assert approved.parsed.source.source_error_code == "environment_git_source_unavailable"
        assert approved.parsed.source.approved_revision_id == "approved"
        assert approved.parsed.source.to_dict()["source_commit_sha"] == "c" * 40
        removed = remove_environment_git_ops_override.sync_detailed(
            "my project",
            "production",
            client=client.inner,
            body=RemoveEnvironmentGitOpsOverrideRequest(resource="workload/api", path="variables/MODE"),
        )
        assert removed.status_code == 204
        status = get_environment_git_ops.sync("my project", "production", client=client.inner)
        assert isinstance(status, EnvironmentGitOpsStatusResponse)
        assert status.approval.definition_digest == digest
        assert status.approval.evidence.pull_request_id == 1234
        assert status.approval.evidence.policy.repository_id == 123
        assert status.approval.evidence.reviews[0].reviewer_id == 11
        assert status.to_dict()["approval"]["evidence"]["policy"]["commit_sha"] == sha
        assert len(calls) == 4
        assert b"/projects/my%20project/environments/production/gitops/revisions/preview" in calls[0].url.raw_path
        assert json.loads(calls[1].content) == {
            "commit_sha": sha,
            "definition_digest": digest,
            "expected_generation": 7,
        }
        assert calls[2].method == "DELETE"
        assert json.loads(calls[2].content) == {"resource": "workload/api", "path": "variables/MODE"}
        assert all(call.headers["Authorization"] == "Bearer token" for call in calls)
        assert calls[1].headers.get("Idempotency-Key")
    finally:
        client.close()


def test_gitlab_review_policy_has_no_github_installation_identity():
    from faas_sdk.models.environment_git_protected_branch_evidence import EnvironmentGitProtectedBranchEvidence

    body = {
        "qualified": True,
        "profile": "gitlab_reviewed_branch/v1",
        "repository_id": 41,
        "repository": "team/subgroup/shop",
        "branch": "main",
        "commit_sha": "a" * 40,
        "policy_digest": "b" * 64,
        "required_review_count": 1,
        "checked_at": "2026-10-15T16:00:00Z",
    }
    evidence = EnvironmentGitProtectedBranchEvidence.from_dict(body)
    assert evidence.repository_id == 41
    assert "installation_id" not in evidence.to_dict()


def test_unchanged_build_context_and_cache_progress_round_trip() -> None:
    from faas_sdk.models import EnvironmentWorkloadProgress, EnvironmentWorkloadSource

    source = {
        "kind": "source",
        "directory": "workspace/apps/api",
        "reuse_unchanged_build": True,
        "build_context": "workspace",
    }
    assert EnvironmentWorkloadSource.from_dict(source).to_dict() == source
    progress = {
        "resource": "workload/api",
        "app_id": "app",
        "execution_mode": "http",
        "build_status": "succeeded",
        "build_cache_status": "hit",
        "build_cache_key_sha256": "a" * 64,
        "preparation": "complete",
        "qualification": "pending",
        "activation": "pending",
        "serving": "unknown",
        "blocking_reasons": [],
    }
    assert EnvironmentWorkloadProgress.from_dict(progress).to_dict() == progress


def test_rollout_notification_preserves_exact_approval_and_diagnostics() -> None:
    from faas_sdk.models import EnvironmentGitOpsChangedWebhookPayload

    for status in ("serving", "failed", "superseded"):
        body = {
            "version": 1,
            "transition_id": "5fd00a70-e0ce-41ea-94f5-613b34c65923",
            "source_id": "dcda57d8-569c-4d2b-b901-cb4f09f00b31",
            "project": "shop",
            "environment": "production",
            "axis": "rollout",
            "previous_status": "progressing",
            "status": status,
            "generation": 3,
            "observed_version": 7,
            "approved_revision_id": "93d593fb-37b4-45bc-a81a-7f86f183b7b2",
            "commit_sha": "a" * 40,
            "definition_digest": "b" * 64,
            "graph_id": "3bb2f69c-8479-4d3e-b5f0-68e77bf25240",
            "plan_hash": "c" * 64,
            "observed_at": "2026-10-17T12:00:00Z",
            "dashboard_path": "/dashboard/projects/shop/environments/production/gitops",
            "changes": [],
            "blocking_reasons": ["environment_workload_build_failed"],
            "details_truncated": False,
            "diagnostics": [
                {
                    "resource": "workload/api",
                    "stage": "preparation",
                    "code": "environment_workload_build_failed",
                    "actor": "customer",
                    "message": "The candidate build failed.",
                    "action": "Inspect build diagnostics.",
                }
            ],
        }
        parsed = EnvironmentGitOpsChangedWebhookPayload.from_dict(body)
        assert parsed.axis == "rollout"
        assert parsed.status == status
        assert parsed.definition_digest == "b" * 64
        assert parsed.diagnostics[0].resource == "workload/api"
        assert parsed.diagnostics[0].code == "environment_workload_build_failed"
        encoded = parsed.to_dict()
        # Datetime serialization normalizes Z to an explicit UTC offset.
        encoded["observed_at"] = body["observed_at"]
        assert encoded == body
