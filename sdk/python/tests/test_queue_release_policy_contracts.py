import json
import unittest

import httpx

from faas_sdk.api.apps import set_queue_binding_release_policy
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.set_queue_binding_release_policy_request import SetQueueBindingReleasePolicyRequest


class QueueReleasePolicyContracts(unittest.TestCase):
    def test_explicit_requirements_clearing_and_old_server_rejection(self):
        for scenario in ("set", "clear", "old server"):
            with self.subTest(scenario=scenario):
                requirements = {} if scenario == "clear" else {"orders": "consumer_readiness"}
                captured = []

                def handle(request):
                    captured.append(request)
                    self.assertEqual(request.method, "PUT")
                    self.assertEqual(request.url.path, "/v1/apps/demo/bindings/release-policy-with-queues")
                    body = json.loads(request.content)
                    self.assertEqual(body["queue_requirements"], requirements)
                    self.assertEqual(body["object_storage_requirements"], {"ASSETS": "read_write"})
                    if scenario == "old server":
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
                    return httpx.Response(
                        404 if scenario == "old server" else 200,
                        json={
                            "app_id": "11111111-1111-4111-8111-111111111111",
                            "scope": "production",
                            "mode": "enforce",
                            "revision": 1,
                            "max_verification_age": "10m0s",
                            "require_application_ack": False,
                            "queue_requirements": requirements,
                        },
                    )

                body = SetQueueBindingReleasePolicyRequest.from_dict(
                    {
                        "mode": "enforce",
                        "expected_revision": 0,
                        "queue_requirements": requirements,
                        "object_storage_requirements": {"ASSETS": "read_write"},
                    }
                )
                with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handle)) as transport:
                    client = AuthenticatedClient(base_url="https://api.invalid", token="fixture").set_httpx_client(
                        transport
                    )
                    response = set_queue_binding_release_policy.sync_detailed(
                        "demo", client=client, body=body, scope="production"
                    )
                self.assertEqual(response.status_code, 404 if scenario == "old server" else 200)
                self.assertEqual(len(captured), 1)
                if scenario != "old server":
                    self.assertEqual(response.parsed.to_dict()["queue_requirements"], requirements)
