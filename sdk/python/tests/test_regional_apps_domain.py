# adr: 785
import datetime
from uuid import uuid4

import httpx

from faas_sdk.api.apps import (
    deliver_global_runtime_quota,
    get_cell_app_binding,
    put_global_runtime_consent,
    revoke_global_runtime_consent,
)
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import (
    DeliverGlobalRuntimeQuotaRequest,
    PutGlobalCellRequest,
    PutGlobalRuntimeConsentRequest,
    RevokeGlobalRuntimeConsentRequest,
)


def test_registry_request_preserves_omission_and_explicit_clear():
    for value in (None, "apps.fsn.example.com", ""):
        request = PutGlobalCellRequest(region="eu-fsn", api_base="https://api.example.com", enabled=True)
        if value is not None:
            request.apps_domain = value
        wire = request.to_dict()
        assert ("apps_domain" in wire) == (value is not None)
        if value is not None:
            assert wire["apps_domain"] == value
        assert PutGlobalCellRequest.from_dict(wire).to_dict() == wire


def test_binding_origin_capability_is_requested_explicitly():
    legacy = get_cell_app_binding._get_kwargs("regional-shop")
    proof = get_cell_app_binding._get_kwargs("regional-shop", traffic_origin=1)
    assert legacy["params"] == {}
    assert proof["params"] == {"traffic_origin": 1}


def test_original_authority_requests_hide_keys_and_refuse_custom_client_redirects():
    key = "request-only-regional-key"
    for endpoint, args, body in (
        (
            put_global_runtime_consent,
            ("global-shop", uuid4()),
            PutGlobalRuntimeConsentRequest(
                region="eu-fsn", expires_at=datetime.datetime.now(datetime.UTC), regional_api_key=key
            ),
        ),
        (
            revoke_global_runtime_consent,
            ("global-shop", uuid4()),
            RevokeGlobalRuntimeConsentRequest(regional_api_key=key),
        ),
        (
            deliver_global_runtime_quota,
            ("global-shop", uuid4(), "eu-fsn"),
            DeliverGlobalRuntimeQuotaRequest(regional_api_key=key),
        ),
    ):
        assert key not in repr(body)
        requests = []

        def handler(request):
            requests.append(request)
            return httpx.Response(307, headers={"Location": "/recipient"})

        client = AuthenticatedClient(
            base_url="https://directory.example.com", token="directory-key", follow_redirects=True
        )
        with httpx.Client(
            base_url="https://directory.example.com", transport=httpx.MockTransport(handler), follow_redirects=True
        ) as transport:
            client.set_httpx_client(transport)
            result = endpoint.sync_detailed(*args, client=client, body=body)
        assert result.status_code == 307
        assert len(requests) == 1
