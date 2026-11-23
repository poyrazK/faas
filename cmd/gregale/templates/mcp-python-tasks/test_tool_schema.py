"""Custom tool schemas guard the HTTP dispatch before application side effects."""
from copy import deepcopy
import json
import unittest
from urllib.request import Request, urlopen
from urllib.error import HTTPError
from unittest.mock import patch

from catalog import RuntimeCatalog
from auth import Authenticator
import server
import test_server


SCHEMA = {
    "type": "object", "required": ["amount", "tags"], "additionalProperties": False,
    "$defs": {"tag": {"type": "string", "enum": ["approved"]}},
    "properties": {
        "amount": {"type": "integer", "minimum": 1},
        "tags": {"type": "array", "minItems": 1, "items": {"$ref": "#/$defs/tag"}},
        "optional": {"type": "string", "default": "annotation only"},
    },
}


class ToolSchemaTests(unittest.TestCase):
    def setUp(self):
        test_server.MCPStarterTests.setUp(self)
        self.config["auth"] = {"mode": "open"}
        self.http.authenticator = Authenticator(self.config)

    tearDown = test_server.MCPStarterTests.tearDown

    def test_boolean_property_schemas_preserve_validation_and_header_checks(self):
        calls = []
        schema = {"type": "object", "properties": {
            "payload": True, "forbidden": False,
            "nested": {"type": "object", "properties": {"payload": True, "forbidden": False}},
            "route": {"type": "string", "x-mcp-header": "Route"},
        }}
        server.register_tool({"name": "boolean_schema", "inputSchema": schema},
                             lambda args, _: calls.append(deepcopy(args)) or {"content": []})
        self.addCleanup(server.remove_tool, "boolean_schema")
        cases = [({}, True), ({"payload": {"value": [1, True, None]}}, True),
                 ({"nested": {"payload": 2}}, True), ({"forbidden": None}, False),
                 ({"nested": {"forbidden": 2}}, False)]
        for version in ("2026-07-28", "2025-11-25"):
            for arguments, valid in cases:
                with self.subTest(version=version, arguments=arguments):
                    before = len(calls)
                    body = {"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                            "params": {"name": "boolean_schema", "arguments": arguments}}
                    headers = {"Content-Type": "application/json", "MCP-Protocol-Version": version,
                               "Mcp-Method": "tools/call", "Mcp-Name": "boolean_schema"}
                    with urlopen(Request(self.base + "/mcp", data=json.dumps(body).encode(), headers=headers), timeout=3) as response:
                        self.assertEqual(response.status, 200)
                        message = json.load(response)
                    self.assertEqual(len(calls) > before, valid)
                    self.assertEqual(message["result"].get("isError", False), not valid)
        body["params"]["arguments"] = {"route": "expected", "payload": {"value": 2}}
        headers.update({"MCP-Protocol-Version": "2026-07-28", "Mcp-Param-Route": "wrong"})
        before = len(calls)
        with self.assertRaises(HTTPError) as failure:
            urlopen(Request(self.base + "/mcp", data=json.dumps(body).encode(), headers=headers), timeout=3)
        self.assertEqual(failure.exception.code, 400)
        self.assertEqual(len(calls), before)
        headers["Mcp-Param-Route"] = "expected"
        with urlopen(Request(self.base + "/mcp", data=json.dumps(body).encode(), headers=headers), timeout=3) as response:
            self.assertFalse(json.load(response)["result"].get("isError", False))
        self.assertEqual(len(calls), before + 1)

    def test_schema_validation_precedes_handler_for_both_protocols(self):
        calls = []
        definition = {"name": "schema_regression", "inputSchema": deepcopy(SCHEMA)}
        server.register_tool(definition, lambda args, _caller: calls.append(deepcopy(args)) or {"content": []})
        self.addCleanup(server.remove_tool, "schema_regression")
        definition["inputSchema"]["required"].clear()
        cases = [
            {"amount": "secret-value", "tags": ["approved"]}, {"amount": -5, "tags": ["approved"]},
            {"tags": ["approved"]}, {"amount": True, "tags": ["approved"]},
            {"amount": 2, "tags": ["rejected"]}, {"amount": 2, "tags": []},
            {"amount": 2, "tags": ["approved"], "unexpected": 1}, {"amount": 2, "tags": ["approved"]},
        ]
        for version in ("2026-07-28", "2025-11-25"):
            for index, args in enumerate(cases):
                with self.subTest(version=version, arguments=args):
                    before = len(calls)
                    body = {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "schema_regression", "arguments": args}}
                    headers = {"Content-Type": "application/json", "MCP-Protocol-Version": version,
                               "Mcp-Method": "tools/call", "Mcp-Name": "schema_regression"}
                    with urlopen(Request(self.base + "/mcp", data=json.dumps(body).encode(), headers=headers), timeout=3) as response:
                        message = json.load(response)
                    valid = index == len(cases) - 1
                    self.assertEqual(len(calls) > before, valid)
                    self.assertEqual(message["result"].get("isError", False), not valid)
                    self.assertNotIn("secret-value", json.dumps(message))
        self.assertTrue(all("optional" not in args for args in calls))

    def test_invalid_replacement_preserves_catalog_and_validator(self):
        catalog = RuntimeCatalog()
        handler = lambda *_: {"content": []}
        catalog.register("tool", {"name": "schema", "inputSchema": SCHEMA}, handler)
        before = catalog.contract_fingerprint()
        for schema in (None, "object", {"type": 42}, {"$schema": "https://unsupported.example/schema"}):
            with self.assertRaises(Exception):
                catalog.register("tool", {"name": "schema", "inputSchema": schema}, handler, replace_name="schema")
            self.assertEqual(catalog.contract_fingerprint(), before)
        catalog.register("tool", {"name": "schema", "inputSchema": {"required": ["new"]}}, handler, replace_name="schema")
        with self.assertRaises(Exception):
            catalog.get("tool", "schema")["input_validator"].validate({})

    def test_external_references_never_fetch_or_invoke_handler(self):
        calls = []
        server.register_tool({"name": "schema_external", "inputSchema": {"$ref": "https://provider.example/schema"}}, lambda *_: calls.append(True))
        self.addCleanup(server.remove_tool, "schema_external")
        with patch("urllib.request.urlopen", side_effect=AssertionError("remote schema fetch")):
            message = server.call_tool(1, {"name": "schema_external", "arguments": {}}, self.http.authenticator, set())
        self.assertTrue(message["result"]["isError"])
        self.assertEqual(calls, [])

    def test_output_schema_for_both_protocols(self):
        current = {"content": []}
        definition = {"name": "output_regression", "inputSchema": {"type": "object"}, "outputSchema": deepcopy(SCHEMA)}
        server.register_tool(definition, lambda *_: deepcopy(current))
        self.addCleanup(server.remove_tool, "output_regression")
        definition["outputSchema"]["required"].clear()
        cases = [
            (None, False), ([], False), ({"amount": "secret-output", "tags": ["approved"]}, False),
            ({"amount": -1, "tags": ["approved"]}, False), ({"tags": ["approved"]}, False),
            ({"amount": 2, "tags": ["rejected"]}, False),
            ({"amount": 2, "tags": ["approved"], "extra": True}, False),
            ({"amount": 2, "tags": ["approved"]}, True),
        ]
        for version in ("2026-07-28", "2025-11-25"):
            for output, valid in cases:
                with self.subTest(version=version, output=output):
                    current["structuredContent"] = output
                    body = {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "output_regression", "arguments": {}}}
                    headers = {"Content-Type": "application/json", "MCP-Protocol-Version": version,
                               "Mcp-Method": "tools/call", "Mcp-Name": "output_regression"}
                    with urlopen(Request(self.base + "/mcp", data=json.dumps(body).encode(), headers=headers), timeout=3) as response:
                        message = json.load(response)
                    self.assertEqual(message["result"].get("isError", False), not valid)
                    self.assertNotIn("secret-output", json.dumps(message))
                    self.assertNotIn("optional", message["result"].get("structuredContent", {}))
            current.clear()
            current.update(content=[])
            missing = server.call_tool(1, {"name": "output_regression"}, self.http.authenticator, set())
            self.assertTrue(missing["result"]["isError"])
            current.update(isError=True, content=[{"type": "text", "text": "business rejection"}])
            error = server.call_tool(1, {"name": "output_regression"}, self.http.authenticator, set())
            self.assertEqual(error["result"]["content"][0]["text"], "business rejection")
            if version == "2026-07-28":
                current.clear()
                current.update(content=[], resultType="input_required", inputRequests={})
                intermediate = server.call_tool(1, {"name": "output_regression"}, self.http.authenticator, set())
                self.assertEqual(intermediate["result"]["resultType"], "input_required")
                self.assertNotIn("isError", intermediate["result"])
            current.clear()
            current.update(content=[])

    def test_output_schema_replacement_is_atomic(self):
        catalog = RuntimeCatalog()
        handler = lambda *_: {"content": []}
        catalog.register("tool", {"name": "output", "outputSchema": {"required": ["old"]}}, handler)
        before = catalog.contract_fingerprint()
        for schema in (None, "object", {"type": 42}, {"$schema": "https://unsupported.example/schema"}):
            with self.assertRaises(Exception):
                catalog.register("tool", {"name": "output", "outputSchema": schema}, handler, replace_name="output")
            self.assertEqual(catalog.contract_fingerprint(), before)
        catalog.register("tool", {"name": "output", "outputSchema": {"required": ["new"]}}, handler, replace_name="output")
        validator = catalog.get("tool", "output")["output_validator"]
        with self.assertRaises(Exception):
            validator.validate({"old": True})
        validator.validate({"new": True})

    def test_output_references_never_retrieve_external_schemas(self):
        server.register_tool({"name": "output_external", "outputSchema": {"$ref": "https://provider.example/schema"}},
                             lambda *_: {"content": [], "structuredContent": {"value": "secret-output"}})
        self.addCleanup(server.remove_tool, "output_external")
        with patch("urllib.request.urlopen", side_effect=AssertionError("remote output schema fetch")):
            message = server.call_tool(1, {"name": "output_external"}, self.http.authenticator, set())
        self.assertTrue(message["result"]["isError"])
        self.assertNotIn("secret-output", json.dumps(message))
