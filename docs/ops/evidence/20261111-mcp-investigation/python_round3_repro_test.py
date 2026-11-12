"""Expected-failing HTTP probe for a legal boolean JSON Schema property."""
import http.client
import json
from pathlib import Path
import sys
import threading
import unittest

REPO = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(REPO / 'cmd/gregale/templates/mcp-python'))
from auth import Authenticator
from server import (CURRENT_PROTOCOL, DrainingHTTPServer, MCPHandler,
                    register_tool, remove_tool)


class PythonRoundThreeInvestigation(unittest.TestCase):
    def test_boolean_property_schema_is_callable_over_modern_http(self):
        name = 'investigation_boolean_schema'
        calls = []
        register_tool({'name': name, 'inputSchema': {
            'type': 'object', 'properties': {'extension': True},
        }}, lambda arguments, identity: calls.append(arguments) or {'content': []})
        config = {'endpoint': '/mcp', 'allowed_origins': [], 'auth': {'mode': 'open'}}
        server = DrainingHTTPServer(('127.0.0.1', 0), MCPHandler)
        server.app_config = config
        server.authenticator = Authenticator(config)
        server.task_runtime = None
        server.expected_catalog_sha256 = ''
        server.daemon_threads = True
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        connection = http.client.HTTPConnection(*server.server_address, timeout=2)
        try:
            body = json.dumps({'jsonrpc': '2.0', 'id': 1, 'method': 'tools/call',
                               'params': {'name': name, 'arguments': {}}})
            connection.request('POST', '/mcp', body=body, headers={
                'Content-Type': 'application/json', 'Accept': 'application/json',
                'MCP-Protocol-Version': CURRENT_PROTOCOL,
                'Mcp-Method': 'tools/call', 'Mcp-Name': name,
            })
            try:
                response = connection.getresponse()
            except http.client.RemoteDisconnected:
                self.fail('legal boolean property schema caused the server to close HTTP without a response')
            self.assertEqual(response.status, 200, response.read())
            self.assertEqual(calls, [{}])
        finally:
            connection.close()
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)
            remove_tool(name)


if __name__ == '__main__':
    unittest.main()
