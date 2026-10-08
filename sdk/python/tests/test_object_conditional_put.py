import json
from uuid import UUID

import httpx
import pytest

from faas_sdk.api.storage import sign_bucket_object
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import ObjectSignedRequest, ObjectSignRequest


@pytest.mark.parametrize("condition", [{"if_match": '"old"'}, {"if_none_match": "*"}])
def test_conditional_put_contract(condition: dict[str, str]) -> None:
    headers = (
        {"If-Match": condition["if_match"]}
        if "if_match" in condition
        else {"If-None-Match": condition["if_none_match"]}
    )

    def handle(request: httpx.Request) -> httpx.Response:
        assert json.loads(request.content) == {
            "method": "PUT",
            "key": "key",
            "size_bytes": 3,
            "expires_in": 300,
            **condition,
        }
        return httpx.Response(
            200,
            json={
                "url": "https://s3.example.test/assets/key",
                "method": "PUT",
                "headers": headers,
                "expires_at": "2026-10-08T20:00:00Z",
            },
        )

    with AuthenticatedClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    ) as client:
        out = sign_bucket_object.sync(
            "demo",
            UUID("11111111-1111-4111-8111-111111111111"),
            client=client,
            body=ObjectSignRequest(method="PUT", key="key", size_bytes=3, **condition),
        )
        assert isinstance(out, ObjectSignedRequest)
        assert out.headers.to_dict() == headers
