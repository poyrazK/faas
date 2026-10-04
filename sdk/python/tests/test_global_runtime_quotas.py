import httpx
import pytest

from faas_sdk.api.apps import get_global_runtime_quota, put_global_runtime_quota, retire_global_runtime_quota
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import GlobalRuntimeQuotaShare, PutGlobalRuntimeQuotaRequest


@pytest.mark.parametrize("operation", [put_global_runtime_quota, get_global_runtime_quota, retire_global_runtime_quota])
def test_quota_forces_no_redirect_with_custom_client(operation):
    requests = []

    def handler(request):
        requests.append(request)
        return httpx.Response(307, headers={"Location": "/recipient"})

    client = AuthenticatedClient(base_url="https://directory.example.com", token="directory-key", follow_redirects=True)
    kwargs = {"slug": "quota-global", "allocation_id": "4225983b-5e81-477c-a77d-c3f7e302ef87", "client": client}
    if operation is put_global_runtime_quota:
        kwargs["body"] = PutGlobalRuntimeQuotaRequest(shares=[GlobalRuntimeQuotaShare(region="eu-fsn", concurrency=2)])
    with httpx.Client(
        base_url="https://directory.example.com", transport=httpx.MockTransport(handler), follow_redirects=True
    ) as transport:
        client.set_httpx_client(transport)
        response = operation.sync_detailed(**kwargs)
    assert response.status_code == 307
    assert len(requests) == 1
    assert requests[0].url.path.endswith("/runtime-quotas/4225983b-5e81-477c-a77d-c3f7e302ef87")


def test_quota_request_serializes_positive_regional_shares():
    body = PutGlobalRuntimeQuotaRequest(
        shares=[
            GlobalRuntimeQuotaShare(region="eu-fsn", concurrency=2),
            GlobalRuntimeQuotaShare(region="eu-hel", concurrency=3),
        ],
        rollout_region="eu-fsn",
    )
    assert body.to_dict() == {
        "shares": [{"region": "eu-fsn", "concurrency": 2}, {"region": "eu-hel", "concurrency": 3}],
        "rollout_region": "eu-fsn",
    }
