"""Public HTTP regression contracts for the modern stateless starter."""
import json
import http.client
import tempfile
import threading
import unittest
from pathlib import Path
from urllib.request import Request,urlopen
from urllib.error import HTTPError
from server import DrainingHTTPServer,MCPHandler,load_config
from auth import Authenticator,AuthConfigurationError,AuthFailure

class MCPStarterTests(unittest.TestCase):
    def setUp(self):
        self.config=json.loads(Path("gregale-mcp.json").read_text())
        self.config["allowed_origins"]=["https://trusted.example"]
        self.http=DrainingHTTPServer(("127.0.0.1",0),MCPHandler)
        self.http.app_config=self.config;self.http.authenticator=Authenticator(self.config);self.http.task_runtime=None;self.http.expected_catalog_sha256=""
        self.http.daemon_threads=True
        self.thread=threading.Thread(target=self.http.serve_forever,daemon=True);self.thread.start()
        self.base="http://127.0.0.1:"+str(self.http.server_address[1])
    def tearDown(self):
        self.http.shutdown();self.http.server_close();self.thread.join(timeout=2)
    def test_tools_round_trip(self):
        for name,args,want in [("greet",{"name":"Ada"},"Ada"),("add",{"a":2.5,"b":3},"5.5")]:
            body={"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":name,"arguments":args}}
            headers={"Content-Type":"application/json","Accept":"application/json","MCP-Protocol-Version":"2026-07-28","Mcp-Method":"tools/call","Mcp-Name":name}
            with urlopen(Request(self.base+"/mcp",data=json.dumps(body).encode(),headers=headers),timeout=3) as response:
                self.assertEqual(response.status,200);self.assertIn(want,response.read().decode())
    def test_catalog_result_metadata(self):
        for version in ["2026-07-28", "2025-11-25"]:
            body={"jsonrpc":"2.0","id":1,"method":"tools/list"}
            headers={"Content-Type":"application/json","MCP-Protocol-Version":version,"Mcp-Method":"tools/list"}
            with urlopen(Request(self.base+"/mcp",data=json.dumps(body).encode(),headers=headers),timeout=3) as response:
                result=json.load(response)["result"]
            if version=="2026-07-28":
                self.assertEqual(result["resultType"],"complete")
                self.assertEqual(result["ttlMs"],0)
                self.assertEqual(result["cacheScope"],"private")
            else:self.assertNotIn("resultType",result)
    def test_origin_and_health(self):
        for origin in [None,"https://trusted.example"]:
            with urlopen(Request(self.base+"/healthz",headers={} if origin is None else {"Origin":origin}),timeout=3) as response:
                self.assertTrue(json.load(response)["ok"])
        with self.assertRaises(HTTPError) as failure:
            urlopen(Request(self.base+"/healthz",headers={"Origin":"https://untrusted.example"}),timeout=3)
        self.assertEqual(failure.exception.code,403)
    def test_health_names_serving_instance(self):
        # Instances restored from one snapshot share their boot env.
        with urlopen(Request(self.base+"/healthz",headers={"X-Faas-Instance-Id":"inst-restored"}),timeout=3) as response:
            self.assertEqual(json.load(response)["instance_id"],"inst-restored")
    def test_draining_rejects_new_work(self):
        self.http.draining.set()
        with self.assertRaises(HTTPError) as failure:urlopen(self.base+"/healthz",timeout=3)
        self.assertEqual(failure.exception.code,503)
    def test_config_fails_closed(self):
        for change in [{"endpoint":"/healthz"},{"allowed_origins":["https://trusted.example:bad"]},{"allowed_origins":["https://trusted.example:"]},{"allowed_origins":["https://trusted.example/path"]}]:
            with tempfile.TemporaryDirectory() as directory:
                path=Path(directory)/"gregale-mcp.json";path.write_text(json.dumps({**self.config,**change}))
                with self.assertRaises(AuthConfigurationError):load_config(path)


class MCPKeepAliveFramingTests(unittest.TestCase):
    setUp = MCPStarterTests.setUp
    tearDown = MCPStarterTests.tearDown

    def post(self, connection, headers=None, path="/mcp"):
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
        connection.request("POST", path, body, headers={
            "Content-Type": "application/json", "MCP-Protocol-Version": "2026-07-28",
            "Mcp-Method": "tools/list", **(headers or {}),
        })
        response = connection.getresponse()
        response.read()
        return response

    def assert_retry_reconnects(self, status, headers=None, path="/mcp", reset=lambda: None):
        connection = http.client.HTTPConnection(*self.http.server_address, timeout=3)
        try:
            rejected = self.post(connection, headers, path)
            self.assertEqual(rejected.status, status)
            self.assertEqual(rejected.getheader("Connection"), "close")
            self.assertTrue(rejected.will_close)
            reset()
            # HTTPConnection automatically reconnects after the close response.
            self.assertEqual(self.post(connection).status, 200)
        finally:
            connection.close()

    def test_content_type_retry_reconnects(self):
        self.assert_retry_reconnects(415, {"Content-Type": "text/plain"})

    def test_auth_challenge_retry_reconnects(self):
        original = self.http.authenticator.authenticate
        def require_auth(_authorization):
            raise AuthFailure(401, "authentication_required", challenge=False)
        self.http.authenticator.authenticate = require_auth
        self.assert_retry_reconnects(401, reset=lambda: setattr(self.http.authenticator, "authenticate", original))

    def test_other_early_rejections_close_unread_bodies(self):
        self.assert_retry_reconnects(403, {"Origin": "https://untrusted.example"})
        self.assert_retry_reconnects(404, path="/unknown")
        self.http.draining.set()
        self.assert_retry_reconnects(503, reset=self.http.draining.clear)
        self.http.expected_catalog_sha256 = "f" * 64
        self.assert_retry_reconnects(503, reset=lambda: setattr(self.http, "expected_catalog_sha256", ""))

    def test_successful_requests_keep_the_connection(self):
        connection = http.client.HTTPConnection(*self.http.server_address, timeout=3)
        try:
            self.assertFalse(self.post(connection).will_close)
            first = connection.sock
            self.assertFalse(self.post(connection).will_close)
            self.assertIs(connection.sock, first)
            self.assertTrue(self.post(connection, {"Connection": "close"}).will_close)
        finally:
            connection.close()


class MCPCORSTests(unittest.TestCase):
    setUp = MCPStarterTests.setUp
    tearDown = MCPStarterTests.tearDown
    def test_browser_preflight(self):
        for origin, method, headers, path, status in [
            ("https://trusted.example", "POST", "Authorization, Content-Type, MCP-Protocol-Version, MCP-Method, MCP-Name, MCP-Param-report", "/mcp", 204),
            ("https://evil.example", "POST", "Authorization", "/mcp", 403),
            (None, "POST", "Authorization", "/mcp", 405),
            ("https://trusted.example", "DELETE", "Authorization", "/mcp", 400),
            ("https://trusted.example", None, "Authorization", "/mcp", 400),
            ("https://trusted.example", "POST", "X-Secret", "/mcp", 400),
            ("https://trusted.example", "POST", "MCP-Param-", "/mcp", 400),
            ("https://trusted.example", "POST", "Authorization", "/missing", 404),
        ]:
            with self.subTest(origin=origin,method=method,headers=headers,path=path):
                request_headers={"Access-Control-Request-Headers":headers}
                if origin is not None:request_headers["Origin"]=origin
                if method is not None:request_headers["Access-Control-Request-Method"]=method
                request=Request(self.base+path,method="OPTIONS",headers=request_headers)
                try:response=urlopen(request,timeout=3)
                except HTTPError as error:response=error
                with response:
                    self.assertEqual(response.status,status)
                    if status==204:
                        self.assertEqual(response.read(),b"")
                        self.assertEqual(response.headers["Access-Control-Allow-Origin"],origin)
                        self.assertIn("authorization",response.headers["Access-Control-Allow-Headers"])
                    else:self.assertIsNone(response.headers.get("Access-Control-Allow-Methods"))
                    if status==403:self.assertIsNone(response.headers.get("Access-Control-Allow-Origin"))
    def test_duplicate_origin_is_rejected(self):
        connection=http.client.HTTPConnection("127.0.0.1",self.http.server_address[1],timeout=3)
        try:
            connection.putrequest("OPTIONS","/mcp")
            connection.putheader("Origin","https://trusted.example")
            connection.putheader("Origin","https://trusted.example")
            connection.putheader("Access-Control-Request-Method","POST")
            connection.endheaders()
            response=connection.getresponse()
            self.assertEqual(response.status,403)
            self.assertIsNone(response.getheader("Access-Control-Allow-Origin"))
            response.read()
        finally:connection.close()

    def test_actual_response_headers(self):
        with urlopen(Request(self.base+"/healthz",headers={"Origin":"https://trusted.example"}),timeout=3) as response:
            self.assertEqual(response.headers["Access-Control-Allow-Origin"],"https://trusted.example")
            self.assertEqual(response.headers["Access-Control-Expose-Headers"],"WWW-Authenticate, MCP-Protocol-Version, Streaming-Status")



class MCPProtocolHeaderTests(unittest.TestCase):
    setUp = MCPStarterTests.setUp
    tearDown = MCPStarterTests.tearDown

    def post(self, message, version="2026-07-28", extra=()):
        connection = http.client.HTTPConnection("127.0.0.1", self.http.server_address[1], timeout=3)
        body = json.dumps(message).encode()
        try:
            connection.putrequest("POST", "/mcp")
            for name, value in [("Content-Type", "application/json"), ("Content-Length", str(len(body))), ("MCP-Protocol-Version", version), *extra]:
                connection.putheader(name, value)
            connection.endheaders(body)
            response = connection.getresponse()
            return response.status, response.read()
        finally:
            connection.close()

    def test_legacy_setting_controls_negotiation_discovery_and_notifications(self):
        for legacy in (False, True):
            self.config["legacy"] = legacy
            for version in ("2026-07-28", "2025-11-25"):
                status, _ = self.post({"jsonrpc":"2.0", "id":1, "method":"initialize", "params":{"protocolVersion":version}}, version, [("MCP-Method", "initialize")])
                self.assertEqual(status, 200 if legacy or version=="2026-07-28" else 400)
            status, _ = self.post({"jsonrpc":"2.0", "id":1, "method":"initialize", "params":{"protocolVersion":"2025-11-25"}}, extra=[("MCP-Method", "initialize")])
            self.assertEqual(status, 200 if legacy else 400)
            status, body = self.post({"jsonrpc":"2.0", "id":1, "method":"server/discover"}, extra=[("MCP-Method", "server/discover")])
            self.assertEqual(status, 200)
            self.assertEqual("2025-11-25" in json.loads(body)["result"]["supportedVersions"], legacy)
            status, _ = self.post({"jsonrpc":"2.0", "method":"notifications/initialized"}, "2025-11-25")
            self.assertEqual(status, 202 if legacy else 400)

    def test_resource_routing_accepts_encoded_uri(self):
        status, body = self.post({"jsonrpc":"2.0", "id":1, "method":"resources/read", "params":{"uri":"greeting://welcome"}},
            extra=[("MCP-Method", "resources/read"), ("MCP-Name", "=?base64?Z3JlZXRpbmc6Ly93ZWxjb21l?=")])
        self.assertEqual(status, 200, body)

    def test_invalid_headers_never_execute_tool(self):
        from server import MCP_CATALOG
        calls = []
        self.config["auth"]["tool_scopes"]["header_probe"] = []
        self.http.authenticator = Authenticator(self.config)
        MCP_CATALOG.register("tool", {"name":"header_probe", "inputSchema":{"type":"object", "properties":{"region":{"type":"string", "x-mcp-header":"region"}}}},
                             lambda args, caller: calls.append(args) or {"content":[]})
        self.addCleanup(lambda: MCP_CATALOG.remove("tool", "header_probe"))
        for method, name, value, meta, duplicate, wanted in [
            ("tools/call", "header_probe", "us-east", "2026-07-28", False, 200),
            ("", "header_probe", "us-east", "2026-07-28", False, 400),
            ("tools/list", "header_probe", "us-east", "2026-07-28", False, 400),
            ("tools/call", "header_probe", "us-east", "2026-07-28", True, 400),
            ("tools/call", "", "us-east", "2026-07-28", False, 400),
            ("tools/call", "greet", "us-east", "2026-07-28", False, 400),
            ("tools/call", "header_probe", "", "2026-07-28", False, 400),
            ("tools/call", "header_probe", "eu-west", "2026-07-28", False, 400),
            ("tools/call", "header_probe", "=?base64?!!!?=", "2026-07-28", False, 400),
            ("tools/call", "header_probe", "=?base64?dXMtZWFzdA==?=", "2026-07-28", False, 200),
            ("tools/call", "header_probe", "us-east", "2025-11-25", False, 400),
        ]:
            with self.subTest(method=method, name=name, value=value, meta=meta, duplicate=duplicate):
                extra = [(key, item) for key, item in [("MCP-Method",method), ("MCP-Name",name), ("MCP-Param-Region",value)] if item]
                if duplicate:
                    extra.append(("MCP-Method",method))
                before = len(calls)
                status, body = self.post({"jsonrpc":"2.0", "id":1, "method":"tools/call", "params":{"name":"header_probe", "arguments":{"region":"us-east"}, "_meta":{"io.modelcontextprotocol/protocolVersion":meta}}}, extra=extra)
                self.assertEqual(status, wanted, body)
                self.assertEqual(len(calls), before + (wanted==200))
                if wanted==400:
                    self.assertEqual(json.loads(body)["error"]["code"], -32020)
        status, _ = self.post({"jsonrpc":"2.0", "method":"notifications/initialized"})
        self.assertEqual(status, 202)


if __name__ == "__main__":
    unittest.main()
