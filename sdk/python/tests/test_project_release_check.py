import datetime
from uuid import UUID

import httpx

from faas_sdk.api.projects import check_project_release_set, publish_project_release_set
from faas_sdk.client import Client
from faas_sdk.models import Problem, ProjectReleaseCheckResponse, PublishProjectReleaseSetRequest
from faas_sdk.models.publish_project_release_set_request_deployments import PublishProjectReleaseSetRequestDeployments


def test_exact_graph_check_and_blocked_publication_receipt():
    members = PublishProjectReleaseSetRequestDeployments.from_dict({"api": "a2b9cc53-907f-4b5c-88a4-fd0c21214556"})
    request = PublishProjectReleaseSetRequest(ttl_seconds=1800, deployments=members, expected_active_release_id="")
    kwargs = check_project_release_set._get_kwargs("shop", "production", body=request)
    assert kwargs["url"] == "/v1/projects/shop/environments/production/release-sets/check"
    assert kwargs["json"]["expected_active_release_id"] == ""
    report = {
        "project_id": "142b7504-f03a-4ee2-aeb3-14d922a845d4",
        "environment": "production",
        "expected_active_release_id": "",
        "ttl_seconds": 1800,
        "members": [],
        "checks": [],
        "passed": False,
        "checked_at": datetime.datetime.now(datetime.UTC).isoformat(),
        "graph_digest": "digest",
        "blockers": [{"code": "verification_missing", "message": "Run the exact probe."}],
    }
    client = Client(base_url="https://example.test")
    check = check_project_release_set._parse_response(client=client, response=httpx.Response(200, json=report))
    assert isinstance(check, ProjectReleaseCheckResponse)
    assert check.project_id == UUID(report["project_id"])
    blocked = publish_project_release_set._parse_response(
        client=client,
        response=httpx.Response(
            409,
            json={
                "type": "about:blank",
                "title": "Blocked",
                "status": 409,
                "code": "project_release_check_failed",
                "project_release_check": report,
            },
        ),
    )
    assert isinstance(blocked, Problem)
    assert blocked.project_release_check.blockers[0].code == "verification_missing"
