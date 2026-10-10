"""Exercise generated GitOps contracts through the public transport wrapper."""

import json

import httpx

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
    EnvironmentGitOpsStatusResponse,
    PreviewEnvironmentGitRevisionRequest,
    PreviewEnvironmentGitRevisionResponse,
    RemoveEnvironmentGitOpsOverrideRequest,
)


def test_app_manifest_service_binding_transport_round_trip() -> None:
    manifest = AppManifest.from_dict({"entrypoint": ["/app"], "service_binding_transport": "https"})

    assert manifest.service_binding_transport == "https"
    assert manifest.to_dict()["service_binding_transport"] == "https"


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
