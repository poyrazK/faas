import json

import httpx

from faas_sdk import FaaSClient
from faas_sdk.api.projects import preview_environment_git_plan
from faas_sdk.models import PreviewEnvironmentGitRevisionRequest


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
