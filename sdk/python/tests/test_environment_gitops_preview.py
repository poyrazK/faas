import json
from uuid import UUID

import httpx

from faas_sdk import FaaSClient
from faas_sdk.api.projects import (
    list_environment_git_releases,
    preview_environment_git_plan,
    preview_environment_git_promotion,
    preview_environment_git_rollback,
)
from faas_sdk.models import (
    PreviewEnvironmentGitPromotionRequest,
    PreviewEnvironmentGitRevisionRequest,
    PreviewEnvironmentGitRollbackRequest,
)


def test_built_artifact_reference_round_trip_keeps_typed_provenance() -> None:
    from faas_sdk.models import EnvironmentGitPromotionArtifact, EnvironmentWorkloadSource

    artifact_id = "018f22b4-1d51-7d55-a5d8-fdc06ec7c663"
    source = {
        "kind": "source",
        "directory": "apps/api",
        "build_artifact_id": artifact_id,
        "build_artifact_hash": "a" * 64,
    }
    decoded = EnvironmentWorkloadSource.from_dict(source)
    assert str(decoded.build_artifact_id) == artifact_id
    assert decoded.build_artifact_hash == "a" * 64
    assert decoded.to_dict() == source
    artifact = {
        "resource": "workload/api",
        "deployment_id": "deployment",
        "kind": "github",
        "build_id": artifact_id,
        "build_artifact_id": artifact_id,
        "build_artifact_hash": "a" * 64,
        "artifact_digest": "sha256:" + "b" * 64,
        "build_commit_sha": "c" * 40,
    }
    decoded_artifact = EnvironmentGitPromotionArtifact.from_dict(artifact)
    assert str(decoded_artifact.build_artifact_id) == artifact_id
    assert decoded_artifact.build_commit_sha == "c" * 40
    assert decoded_artifact.to_dict() == artifact


def test_promotion_preview_preserves_artifact_identity_and_expected_hash() -> None:
    sha, digest, plan_hash = "a" * 40, "b" * 64, "c" * 64
    body = {
        "project": "shop",
        "from_environment": "staging",
        "to_environment": "production",
        "observed_at": "2026-10-16T12:00:00+00:00",
        "read_only": True,
        "values_redacted": True,
        "requires_target_qualification": True,
        "origin": {
            "source_id": "source",
            "revision_id": "revision",
            "commit_sha": sha,
            "definition_digest": digest,
            "generation": 2,
            "intent_version": 3,
            "graph_id": "graph",
            "release_set_id": "release",
            "provider": "github",
            "repository_id": 123,
        },
        "target": {
            "source_id": "target",
            "generation": 4,
            "intent_version": 5,
            "commit_sha": sha,
            "definition_digest": digest,
            "proposed_digest": digest,
            "manifest_path": "environments/production.yaml",
        },
        "artifacts": [
            {
                "resource": "workload/api",
                "deployment_id": "deployment",
                "kind": "image",
                "image_digest": "sha256:" + digest,
            }
        ],
        "changes": [],
        "blocking_reasons": [],
        "plan_hash": plan_hash,
        "target_plan": {
            "manager": "target",
            "revision": "candidate:" + sha,
            "commit_sha": sha,
            "generation": 4,
            "observed_version": 5,
            "desired_digest": digest,
            "plan_hash": plan_hash,
            "changes": [],
            "blocking_reasons": [],
        },
    }

    def handle(request: httpx.Request) -> httpx.Response:
        assert request.method == "POST"
        assert request.url.path == "/v1/projects/shop/environments/production/gitops/promotion-plan"
        assert json.loads(request.content) == {
            "from_environment": "staging",
            "target_commit_sha": sha,
            "expected_plan_hash": plan_hash,
        }
        return httpx.Response(200, json=body)

    client = FaaSClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    )
    try:
        preview = preview_environment_git_promotion.sync(
            slug="shop",
            environment="production",
            client=client.inner,
            body=PreviewEnvironmentGitPromotionRequest(
                from_environment="staging", target_commit_sha=sha, expected_plan_hash=plan_hash
            ),
        )
        assert preview is not None
        assert preview.to_dict() == body
    finally:
        client.close()


def test_rollback_preview_preserves_artifact_identity_and_expected_hash() -> None:
    sha, digest, plan_hash = "a" * 40, "b" * 64, "c" * 64
    body = {
        "project": "shop",
        "environment": "production",
        "current_release_set_id": "current-release",
        "observed_at": "2026-10-16T12:00:00+00:00",
        "read_only": True,
        "values_redacted": True,
        "requires_target_qualification": True,
        "origin": {
            "source_id": "source",
            "revision_id": "revision",
            "commit_sha": sha,
            "definition_digest": digest,
            "generation": 2,
            "intent_version": 3,
            "graph_id": "graph",
            "release_set_id": "release",
            "provider": "github",
            "repository_id": 123,
        },
        "target": {
            "source_id": "target",
            "generation": 4,
            "intent_version": 5,
            "commit_sha": sha,
            "definition_digest": digest,
            "proposed_digest": digest,
            "manifest_path": "environments/production.yaml",
        },
        "artifacts": [
            {
                "resource": "workload/api",
                "deployment_id": "deployment",
                "kind": "image",
                "image_digest": "sha256:" + digest,
            }
        ],
        "changes": [],
        "blocking_reasons": [],
        "plan_hash": plan_hash,
        "target_plan": {
            "manager": "target",
            "revision": "candidate:" + sha,
            "commit_sha": sha,
            "generation": 4,
            "observed_version": 5,
            "desired_digest": digest,
            "plan_hash": plan_hash,
            "changes": [],
            "blocking_reasons": [],
        },
    }

    def handle(request: httpx.Request) -> httpx.Response:
        assert request.method == "POST"
        assert request.url.path == "/v1/projects/shop/environments/production/gitops/rollback-plan"
        assert json.loads(request.content) == {
            "release_set_id": "2ba0aa40-2362-4c0e-aeb1-6381666bb514",
            "target_commit_sha": sha,
            "expected_plan_hash": plan_hash,
        }
        return httpx.Response(200, json=body)

    client = FaaSClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    )
    try:
        preview = preview_environment_git_rollback.sync(
            slug="shop",
            environment="production",
            client=client.inner,
            body=PreviewEnvironmentGitRollbackRequest(
                release_set_id=UUID("2ba0aa40-2362-4c0e-aeb1-6381666bb514"),
                target_commit_sha=sha,
                expected_plan_hash=plan_hash,
            ),
        )
        assert preview is not None
        assert preview.to_dict() == body
    finally:
        client.close()


def test_candidate_preview_preserves_read_only_observation() -> None:
    sha = "a" * 40

    def handle(request: httpx.Request) -> httpx.Response:
        assert request.method == "POST"
        assert request.url.path == "/v1/projects/shop/environments/staging/gitops/plan"
        assert json.loads(request.content) == {"commit_sha": sha}
        return httpx.Response(
            200,
            json={
                "source_id": "11111111-2222-4333-8444-555555555555",
                "provider": "gitlab",
                "project": "shop",
                "environment": "staging",
                "observed_at": "2026-10-13T00:00:00Z",
                "read_only": True,
                "values_redacted": True,
                "plan": {
                    "manager": "source",
                    "revision": "candidate:" + sha,
                    "commit_sha": sha,
                    "generation": 0,
                    "observed_version": 7,
                    "desired_digest": "b" * 64,
                    "plan_hash": "c" * 64,
                    "changes": [],
                    "blocking_reasons": [],
                },
            },
        )

    client = FaaSClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    )
    try:
        preview = preview_environment_git_plan.sync(
            slug="shop",
            environment="staging",
            client=client.inner,
            body=PreviewEnvironmentGitRevisionRequest(commit_sha=sha),
        )
        assert preview is not None
        assert preview.read_only is True
        assert preview.values_redacted is True
        assert preview.plan.generation == 0
        assert preview.plan.observed_version == 7
    finally:
        client.close()


def test_recovery_catalog_keeps_target_cursor_and_eligibility_typed() -> None:
    sha = "a" * 40
    cursor = UUID("2ba0aa40-2362-4c0e-aeb1-6381666bb514")
    body = {
        "project": "shop",
        "environment": "production",
        "source_id": "11111111-2222-4333-8444-555555555555",
        "target_commit_sha": sha,
        "observed_at": "2026-10-17T12:00:00+00:00",
        "read_only": True,
        "values_redacted": True,
        "requires_target_qualification": True,
        "recovery_retention": {"releases": 3, "seconds": 86400, "max_bytes": 1000},
        "retention_bytes": 100,
        "releases": [
            {
                "release_set_id": str(cursor),
                "active": False,
                "created_at": "2026-10-16T12:00:00+00:00",
                "expires_at": "2026-10-16T13:00:00+00:00",
                "protection_status": "protected",
                "artifacts_protected_until": "2026-10-18T12:00:00+00:00",
                "retained_bytes": 100,
                "retention_status": "compatibility_window_elapsed",
                "commit_sha": "b" * 40,
                "eligibility": "blocked",
                "blocking_reasons": ["workload/api: rollback_artifact_replaced"],
                "artifacts": [],
            }
        ],
        "next_before": str(cursor),
    }

    def handle(request: httpx.Request) -> httpx.Response:
        assert request.method == "GET"
        assert request.url.path == "/v1/projects/shop/environments/production/gitops/releases"
        assert request.url.params["target_commit_sha"] == sha
        assert request.url.params["before"] == str(cursor)
        assert request.content == b""
        assert request.headers["Authorization"] == "Bearer token"
        return httpx.Response(200, json=body)

    client = FaaSClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    )
    try:
        catalog = list_environment_git_releases.sync(
            slug="shop", environment="production", target_commit_sha=sha, before=cursor, client=client.inner
        )
        assert catalog is not None
        assert catalog.to_dict() == body
    finally:
        client.close()
