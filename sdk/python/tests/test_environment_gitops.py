"""Exercise generated GitOps contracts through the public transport wrapper."""

import json

import httpx

from faas_sdk import FaaSClient
from faas_sdk.api.projects import (
    approve_environment_git_revision,
    preview_environment_git_revision,
    remove_environment_git_ops_override,
)
from faas_sdk.models import (
    ApproveEnvironmentGitRevisionRequest,
    ApproveEnvironmentGitRevisionResponse,
    PreviewEnvironmentGitRevisionRequest,
    PreviewEnvironmentGitRevisionResponse,
    RemoveEnvironmentGitOpsOverrideRequest,
)


def test_reviewed_authority_and_override_identity() -> None:
    sha, digest = "a" * 40, "b" * 64
    calls: list[httpx.Request] = []
    definition = {
        "api_version": "gregale.dev/environment/v1", "project": "shop",
        "environment": "production", "workloads": {"api": {"app": "shop-api"}},
    }
    source = {
        "id": "source", "account_id": "account", "project_id": "project", "environment_id": "environment",
        "environment": "production", "suspended": False, "generation": 8, "intent_version": 1,
        "created_at": "2026-09-30T00:00:00Z", "updated_at": "2026-09-30T00:00:00Z",
        "approved_revision_id": "approved", "source_commit_sha": "c" * 40, "source_definition_digest": digest,
        "source_verified_at": "2026-10-01T00:00:00Z", "source_error_code": "environment_git_source_unavailable",
        "source": {
            "repository_id": 123, "installation_id": 42, "repository": "example/shop", "ref": "refs/heads/main",
            "manifest_path": "environment.yaml", "mode": "report", "approval_policy": "manual", "prune": False,
        },
    }

    def handle(request: httpx.Request) -> httpx.Response:
        calls.append(request)
        if request.url.path.endswith("/preview"):
            return httpx.Response(200, json={"commit_sha": sha, "definition_digest": digest, "generation": 7, "definition": definition})
        if request.url.path.endswith("/approve"):
            return httpx.Response(202, json={"source": source, "revision": {
                "id": "revision", "source_id": "source", "commit_sha": sha, "definition_digest": digest,
                "definition": definition, "approved_by": "account", "approved_at": "2026-09-30T00:00:00Z",
            }})
        return httpx.Response(204)

    client = FaaSClient(base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)})
    try:
        review = preview_environment_git_revision.sync("my project", "production", client=client.inner,
            body=PreviewEnvironmentGitRevisionRequest(commit_sha=sha))
        assert isinstance(review, PreviewEnvironmentGitRevisionResponse)
        approved = approve_environment_git_revision.sync_detailed("my project", "production", client=client.inner,
            body=ApproveEnvironmentGitRevisionRequest(commit_sha=review.commit_sha,
                definition_digest=review.definition_digest, expected_generation=review.generation))
        assert approved.status_code == 202
        assert isinstance(approved.parsed, ApproveEnvironmentGitRevisionResponse)
        assert approved.parsed.source.source_commit_sha == "c" * 40
        assert approved.parsed.source.source_definition_digest == digest
        assert approved.parsed.source.source_verified_at.isoformat() == "2026-10-01T00:00:00+00:00"
        assert approved.parsed.source.source_error_code == "environment_git_source_unavailable"
        assert approved.parsed.source.approved_revision_id == "approved"
        assert approved.parsed.source.to_dict()["source_commit_sha"] == "c" * 40
        removed = remove_environment_git_ops_override.sync_detailed("my project", "production", client=client.inner,
            body=RemoveEnvironmentGitOpsOverrideRequest(resource="workload/api", path="variables/MODE"))
        assert removed.status_code == 204
        assert len(calls) == 3
        assert b"/projects/my%20project/environments/production/gitops/revisions/preview" in calls[0].url.raw_path
        assert json.loads(calls[1].content) == {"commit_sha": sha, "definition_digest": digest, "expected_generation": 7}
        assert calls[2].method == "DELETE"
        assert json.loads(calls[2].content) == {"resource": "workload/api", "path": "variables/MODE"}
        assert all(call.headers["Authorization"] == "Bearer token" for call in calls)
        assert calls[1].headers.get("Idempotency-Key")
    finally:
        client.close()
