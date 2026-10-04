import json
from uuid import UUID

import httpx
import pytest

from faas_sdk.api.deployments import put_global_image_release
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import PutGlobalImageReleaseRequest, PutGlobalImageReleaseRequestRegionalApiKeys


@pytest.mark.parametrize("status", [307, 308])
def test_global_release_refuses_redirect_and_hides_keys(status):
    keys = PutGlobalImageReleaseRequestRegionalApiKeys.from_dict({"eu-fsn": "regional-secret"})
    body = PutGlobalImageReleaseRequest(image="image", regional_api_keys=keys)
    assert "regional-secret" not in repr(keys)
    assert "regional-secret" not in repr(body)
    calls = []

    def handler(request):
        calls.append(request)
        assert json.loads(request.content) == {"image": "image", "regional_api_keys": {"eu-fsn": "regional-secret"}}
        return httpx.Response(status, headers={"Location": "/recipient"})

    client = AuthenticatedClient(base_url="https://directory.example.com", token="directory-key", follow_redirects=True)
    with httpx.Client(
        base_url="https://directory.example.com",
        headers={"Authorization": "Bearer directory-key"},
        transport=httpx.MockTransport(handler),
        follow_redirects=True,
    ) as transport:
        client.set_httpx_client(transport)
        result = put_global_image_release.sync_detailed(
            "global-shop", UUID("4225983b-5e81-477c-a77d-c3f7e302ef87"), client=client, body=body
        )
    assert result.status_code == status
    assert len(calls) == 1
