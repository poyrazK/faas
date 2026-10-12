import unittest
from types import SimpleNamespace

from auth import Authenticator
from server import CURRENT_PROTOCOL, MCPHandler, dispatch


AUTH_CONFIG = {
    "mode": "external-oauth",
    "issuer": "https://issuer.example",
    "jwks_url": "https://issuer.example/jwks",
    "resource": "https://mcp.example/mcp",
    "scopes": ["mcp:catalog"],
    "resource_scopes": {
        "greeting://welcome": [],
        "customer://records/{recordId}": ["records:read"],
    },
    "prompt_scopes": {"summarize": ["reports:read"]},
}


def authenticator(auth_config=None):
    return Authenticator({"endpoint": "/mcp", "auth": auth_config or AUTH_CONFIG})


class ResponseHarness:
    def __init__(self, instance):
        self.server = SimpleNamespace(authenticator=instance)
        self.status = None
        self.value = None
        self.headers = {}

    def send_json(self, status, value, headers=None):
        self.status = status
        self.value = value
        self.headers.update(headers or {})


class ScopePolicyTest(unittest.TestCase):
    def setUp(self):
        self.auth = authenticator()
        self.reader = {"mcp:catalog"}
        self.writer = {"mcp:catalog", "records:read", "reports:read"}

    def test_catalogs_filter_resources_templates_and_prompts_by_scopes(self):
        status, result = dispatch({"id": 1, "method": "resources/list"}, CURRENT_PROTOCOL, self.auth, self.reader)
        self.assertEqual(status, 200)
        self.assertEqual([item["uri"] for item in result["result"]["resources"]], ["greeting://welcome"])

        status, result = dispatch({"id": 1, "method": "resources/templates/list"}, CURRENT_PROTOCOL, self.auth, self.reader)
        self.assertEqual(status, 200)
        self.assertEqual(result["result"]["resourceTemplates"], [])

        status, result = dispatch({"id": 1, "method": "prompts/list"}, CURRENT_PROTOCOL, self.auth, self.reader)
        self.assertEqual(status, 200)
        self.assertEqual(result["result"]["prompts"], [])

        status, result = dispatch({"id": 1, "method": "resources/templates/list"}, CURRENT_PROTOCOL, self.auth, self.writer)
        self.assertEqual(status, 200)
        self.assertEqual(result["result"]["resourceTemplates"][0]["uriTemplate"], "customer://records/{recordId}")
        status, result = dispatch({"id": 1, "method": "prompts/list"}, CURRENT_PROTOCOL, self.auth, self.writer)
        self.assertEqual(status, 200)
        self.assertEqual([item["name"] for item in result["result"]["prompts"]], ["summarize"])

    def test_resource_and_prompt_scope_failures_include_endpoint_and_entry_scopes(self):
        cases = (
            ("resources/read", {"uri": "customer://records/example-1"}, "records:read"),
            ("prompts/get", {"name": "summarize", "arguments": {"text": "private"}}, "reports:read"),
            (
                "completion/complete",
                {"ref": {"type": "ref/resource", "uri": "customer://records/{recordId}"}, "argument": {"name": "recordId", "value": "example-"}},
                "records:read",
            ),
            (
                "completion/complete",
                {"ref": {"type": "ref/prompt", "name": "summarize"}, "argument": {"name": "style", "value": "exec"}},
                "reports:read",
            ),
        )
        for method, params, entry_scope in cases:
            with self.subTest(method=method):
                failure = self.auth.authorize_request(method, params, self.reader)
                self.assertIsNotNone(failure)
                self.assertEqual(failure.status, 403)
                self.assertEqual(failure.code, "insufficient_scope")
                harness = ResponseHarness(self.auth)
                MCPHandler.send_auth_failure(harness, failure)
                challenge = harness.headers["WWW-Authenticate"]
                self.assertIn(f'scope="mcp:catalog {entry_scope}"', challenge)
                self.assertIn('error="insufficient_scope"', challenge)

    def test_unlisted_resource_prompt_and_completion_are_denied_without_challenge(self):
        cases = (
            ("resources/read", {"uri": "task://tasks/task-1"}, "resource_access_denied"),
            ("prompts/get", {"name": "private_prompt"}, "prompt_access_denied"),
            (
                "completion/complete",
                {"ref": {"type": "ref/resource", "uri": "task://tasks/{taskId}"}, "argument": {"name": "taskId", "value": ""}},
                "resource_access_denied",
            ),
            (
                "completion/complete",
                {"ref": {"type": "ref/prompt", "name": "private_prompt"}, "argument": {"name": "style", "value": ""}},
                "prompt_access_denied",
            ),
        )
        for method, params, code in cases:
            with self.subTest(method=method):
                failure = self.auth.authorize_request(method, params, self.writer)
                self.assertEqual(failure.code, code)
                self.assertFalse(failure.challenge)
                harness = ResponseHarness(self.auth)
                MCPHandler.send_auth_failure(harness, failure)
                self.assertNotIn("WWW-Authenticate", harness.headers)

    def test_prompt_and_resource_completions_are_prefix_matched_and_bounded(self):
        status, result = dispatch({
            "id": 1,
            "method": "completion/complete",
            "params": {
                "ref": {"type": "ref/prompt", "name": "summarize"},
                "argument": {"name": "style", "value": "EXEC"},
            },
        }, CURRENT_PROTOCOL, self.auth, self.writer)
        self.assertEqual(status, 200)
        self.assertEqual(result["result"]["completion"], {
            "values": ["executive"], "total": 1, "hasMore": False,
        })

        status, result = dispatch({
            "id": 2,
            "method": "completion/complete",
            "params": {
                "ref": {"type": "ref/resource", "uri": "customer://records/{recordId}"},
                "argument": {"name": "recordId", "value": "EXAMPLE-"},
            },
        }, CURRENT_PROTOCOL, self.auth, self.writer)
        self.assertEqual(status, 200)
        self.assertEqual(result["result"]["completion"]["values"], [
            "example-1", "example-2", "example-3",
        ])
        self.assertEqual(result["result"]["completion"]["total"], 3)
        self.assertFalse(result["result"]["completion"]["hasMore"])

    def test_completion_rejects_unknown_reference_or_argument(self):
        status, result = dispatch({
            "id": 1,
            "method": "completion/complete",
            "params": {
                "ref": {"type": "ref/prompt", "name": "private_prompt"},
                "argument": {"name": "style", "value": "e"},
            },
        }, CURRENT_PROTOCOL, self.auth, self.writer)
        self.assertEqual(status, 400)
        self.assertEqual(result["error"]["code"], -32602)

    def test_explicit_empty_maps_hide_and_deny_all_resources_and_prompts(self):
        empty = authenticator({
            "mode": "open",
            "resource_scopes": {},
            "prompt_scopes": {},
        })
        self.assertFalse(empty.can_access_resource("greeting://welcome", set()))
        self.assertFalse(empty.can_access_resource("customer://records/example-1", set()))
        self.assertFalse(empty.can_access_prompt("summarize", set()))
        self.assertEqual(empty.resource_failure("greeting://welcome", set()).code, "resource_access_denied")
        self.assertEqual(empty.prompt_failure("summarize", set()).code, "prompt_access_denied")

    def test_protected_resource_metadata_advertises_resource_and_prompt_scopes(self):
        self.assertEqual(
            self.auth.metadata()["scopes_supported"],
            ["mcp:catalog", "records:read", "reports:read"],
        )


if __name__ == "__main__":
    unittest.main()
