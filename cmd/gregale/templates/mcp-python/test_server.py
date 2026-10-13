"""Public HTTP regression contracts for the modern stateless starter."""
import json
import tempfile
import threading
import unittest
from pathlib import Path
from urllib.request import Request,urlopen
from urllib.error import HTTPError
from server import DrainingHTTPServer,MCPHandler,load_config
from auth import Authenticator,AuthConfigurationError

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
    def test_draining_rejects_new_work(self):
        self.http.draining.set()
        with self.assertRaises(HTTPError) as failure:urlopen(self.base+"/healthz",timeout=3)
        self.assertEqual(failure.exception.code,503)
    def test_config_fails_closed(self):
        for change in [{"endpoint":"/healthz"},{"allowed_origins":["https://trusted.example:bad"]},{"allowed_origins":["https://trusted.example:"]},{"allowed_origins":["https://trusted.example/path"]}]:
            with tempfile.TemporaryDirectory() as directory:
                path=Path(directory)/"gregale-mcp.json";path.write_text(json.dumps({**self.config,**change}))
                with self.assertRaises(AuthConfigurationError):load_config(path)

if __name__=="__main__":unittest.main()
