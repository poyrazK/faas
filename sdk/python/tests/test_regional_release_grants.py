from datetime import datetime, timezone
from uuid import UUID

import httpx
import pytest

from faas_sdk.api.deployments import (
    get_regional_release_grant,
    put_regional_release_grant,
    revoke_regional_release_grant,
)
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import PutRegionalReleaseGrantRequest


@pytest.mark.parametrize(
    "operation", [put_regional_release_grant, get_regional_release_grant, revoke_regional_release_grant]
)
@pytest.mark.parametrize("status", [307, 308])
def test_regional_grant_operations_refuse_redirect(operation, status):
    calls = []

    def handler(request):
        calls.append(request)
        return httpx.Response(status, headers={"Location": "/recipient"})

    client = AuthenticatedClient(base_url="https://cell.example.com", token="regional-key", follow_redirects=True)
    with httpx.Client(
        base_url="https://cell.example.com", transport=httpx.MockTransport(handler), follow_redirects=True
    ) as transport:
        client.set_httpx_client(transport)
        ids = [UUID("4225983b-5e81-477c-a77d-c3f7e302ef87")] * 3
        kwargs = {"client": client}
        if operation is put_regional_release_grant:
            kwargs["body"] = PutRegionalReleaseGrantRequest(
                global_app_id=ids[0], token_sha256="a" * 64, expires_at=datetime(2026, 10, 5, tzinfo=timezone.utc)
            )
        response = operation.sync_detailed(*ids, **kwargs)
    assert response.status_code == status
    assert len(calls) == 1
