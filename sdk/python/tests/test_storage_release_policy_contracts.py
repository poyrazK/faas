import json
import unittest

import httpx

from faas_sdk.preview.api.apps import set_object_storage_binding_release_policy
from faas_sdk.client import AuthenticatedClient
from faas_sdk.preview.models.set_object_storage_binding_release_policy_request import (
    SetObjectStorageBindingReleasePolicyRequest,
)


class StorageReleasePolicyContracts(unittest.TestCase):
    def test_explicit_requirement_and_clearing_survive_http_serialization(self):
        for requirements in ({"ASSETS": "read_write"}, {}):
            with self.subTest(requirements=requirements):
                captured = []

                def handle(request):
                    captured.append(request)
                    self.assertEqual(request.method, "PUT")
                    self.assertEqual(
                        request.url.path,
                        "/v1/apps/demo/bindings/release-policy-with-object-storage",
                    )
                    self.assertEqual(json.loads(request.content)["object_storage_requirements"], requirements)
                    return httpx.Response(
                        200,
                        json={
                            "app_id": "11111111-1111-4111-8111-111111111111",
                            "scope": "production",
                            "mode": "enforce",
                            "revision": 1,
                            "max_verification_age": "10m0s",
                            "require_application_ack": False,
                            "object_storage_requirements": requirements,
                        },
                    )

                body = SetObjectStorageBindingReleasePolicyRequest.from_dict(
                    {"mode": "enforce", "expected_revision": 0, "object_storage_requirements": requirements}
                )
                with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handle)) as transport:
                    client = AuthenticatedClient(base_url="https://api.invalid", token="fixture").set_httpx_client(
                        transport
                    )
                    response = set_object_storage_binding_release_policy.sync_detailed(
                        "demo", client=client, body=body, scope="production"
                    )
                self.assertEqual(response.status_code, 200)
                self.assertEqual(len(captured), 1)
                self.assertEqual(response.parsed.to_dict()["object_storage_requirements"], requirements)

    def test_old_server_rejection_never_falls_back(self):
        captured = []

        def handle(request):
            captured.append(request)
            return httpx.Response(
                404,
                json={
                    "type": "about:blank",
                    "title": "Not found",
                    "status": 404,
                    "code": "not_found",
                    "detail": "Upgrade the server.",
                },
            )

        body = SetObjectStorageBindingReleasePolicyRequest.from_dict(
            {"mode": "enforce", "expected_revision": 0, "object_storage_requirements": {"ASSETS": "write"}}
        )
        with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handle)) as transport:
            client = AuthenticatedClient(base_url="https://api.invalid", token="fixture").set_httpx_client(transport)
            response = set_object_storage_binding_release_policy.sync_detailed("demo", client=client, body=body)
        self.assertEqual(response.status_code, 404)
        self.assertEqual(len(captured), 1)
        self.assertTrue(captured[0].url.path.endswith("release-policy-with-object-storage"))
