import json

import httpx

from faas_sdk.api.deploydiff import diff_app
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.diff_request import DiffRequest
from faas_sdk.types import UNSET


def test_regional_preview_transport_and_response():
    requests = []

    def handle(request):
        requests.append((request.method, request.url.path, json.loads(request.content)))
        return httpx.Response(
            200,
            json={
                "slug": "regional",
                "blocking": False,
                "diff": {"slug": "regional", "changes": [], "breaks": [], "compute_region": "eu-fsn"},
            },
        )

    client = AuthenticatedClient(
        base_url="https://api.example.test", token="test-key", httpx_args={"transport": httpx.MockTransport(handle)}
    )
    with client:
        response = diff_app.sync(slug="regional", client=client, body=DiffRequest(compute_region="eu-fsn"))
    assert response is not None
    assert response.diff.compute_region == "eu-fsn"
    assert response.blocking is False
    assert requests == [("POST", "/v1/apps/regional/diff", {"compute_region": "eu-fsn"})]


def test_region_preview_omission_and_explicit_empty_remain_distinct():
    assert "compute_region" not in DiffRequest().to_dict()
    assert DiffRequest(compute_region="").to_dict()["compute_region"] == ""
    assert DiffRequest.from_dict({}).compute_region is UNSET
    assert DiffRequest.from_dict({"compute_region": None}).compute_region is None
