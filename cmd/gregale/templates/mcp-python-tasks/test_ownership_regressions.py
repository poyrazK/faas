"""Ownership regressions for resource discovery and subscriptions."""
import json
import threading
from urllib.request import Request, urlopen
import unittest

from auth import AuthContext, Authenticator
from server import (CURRENT_PROTOCOL, DrainingHTTPServer, MCPHandler, dispatch,
                    notify_resource_updated, register_resource, replace_resource,
                    remove_resource, resource_access_allowed)

class PythonMCPInvestigation(unittest.TestCase):
    def test_application_subscription_requires_authorizer_and_honors_revocation(self):
        definition = {'name': 'guarded_regression', 'uri': 'fixture://guarded-regression'}
        auth = Authenticator({'endpoint': '/mcp', 'auth': {'mode': 'open'}})
        identity = AuthContext(set())
        def read_handler(*_):
            self.fail('authorization executed a resource read handler')
        register_resource(definition, read_handler)
        try:
            self.assertFalse(resource_access_allowed(definition['uri'], auth, identity, subscription=True))
            allowed = True
            replace_resource(definition['name'], definition, read_handler,
                             authorize=lambda uri, caller: allowed)
            self.assertTrue(resource_access_allowed(definition['uri'], auth, identity, subscription=True))
            allowed = False
            for subscription in (False, True):
                self.assertFalse(resource_access_allowed(definition['uri'], auth, identity, subscription=subscription))
        finally:
            remove_resource(definition['name'])

    def test_completion_does_not_enumerate_another_callers_records(self):
        auth = Authenticator({'endpoint': '/mcp', 'auth': {
            'mode': 'external-oauth', 'issuer': 'https://issuer.example',
            'jwks_url': 'https://issuer.example/jwks', 'resource': 'https://mcp.example/mcp',
            'scopes': ['mcp:catalog'], 'resource_scopes': {
                'customer://records/{recordId}': ['records:read'],
            },
        }})
        identity = AuthContext({'mcp:catalog', 'records:read'}, subject='demo-caller-a')
        status, read = dispatch({'id': 1, 'method': 'resources/read', 'params': {
            'uri': 'customer://records/example-2',
        }}, CURRENT_PROTOCOL, auth, identity)
        self.assertEqual(status, 404)
        status, completion = dispatch({'id': 2, 'method': 'completion/complete', 'params': {
            'ref': {'type': 'ref/resource', 'uri': 'customer://records/{recordId}'},
            'argument': {'name': 'recordId', 'value': 'example-'},
        }}, CURRENT_PROTOCOL, auth, identity)
        self.assertEqual(status, 200)
        self.assertNotIn('example-2', completion['result']['completion']['values'],
                         'completion leaks a record ID that resource ownership refuses')

    def test_subscriptions_do_not_reveal_unreadable_record_updates(self):
        config = {'version': 1, 'transport': 'streamable-http', 'mode': 'stateless',
                  'endpoint': '/mcp', 'allowed_origins': [], 'auth': {
            'mode': 'external-oauth', 'issuer': 'https://issuer.example',
            'jwks_url': 'https://issuer.example/jwks', 'resource': 'https://mcp.example/mcp',
            'scopes': ['mcp:catalog'], 'resource_scopes': {
                'customer://records/{recordId}': ['records:read'],
            },
        }}
        auth = Authenticator(config)
        identity = AuthContext({'mcp:catalog', 'records:read'}, subject='demo-caller-a')
        # Authentication is supplied as an already verified identity; this
        # probe isolates the resource-ownership guard, not JWT verification.
        auth.authenticate = lambda authorization: identity
        forbidden_uri = 'customer://records/example-2'
        status, _ = dispatch({'id': 1, 'method': 'resources/read',
                              'params': {'uri': forbidden_uri}}, CURRENT_PROTOCOL, auth, identity)
        self.assertEqual(status, 404)
        server = DrainingHTTPServer(('127.0.0.1', 0), MCPHandler)
        server.app_config = config
        server.authenticator = auth
        server.task_runtime = None
        server.expected_catalog_sha256 = ''
        server.daemon_threads = True
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        response = None
        def next_message():
            while True:
                line = response.readline()
                if not line:
                    self.fail('subscription ended before the probe completed')
                if line.startswith(b'data: '):
                    return json.loads(line[6:])
        try:
            message = {'jsonrpc': '2.0', 'id': 3, 'method': 'subscriptions/listen',
                       'params': {'notifications': {'resourceSubscriptions': [forbidden_uri]},
                                  '_meta': {'io.modelcontextprotocol/protocolVersion': CURRENT_PROTOCOL}}}
            headers = {'Content-Type': 'application/json', 'Accept': 'text/event-stream',
                       'MCP-Protocol-Version': CURRENT_PROTOCOL, 'Mcp-Method': 'subscriptions/listen'}
            response = urlopen(Request(f'http://127.0.0.1:{server.server_address[1]}/mcp',
                                      data=json.dumps(message).encode(), headers=headers), timeout=2)
            ack = next_message()
            notify_resource_updated(forbidden_uri)
            update = next_message()
            self.assertNotEqual(update.get('params', {}).get('uri'), forbidden_uri,
                                f'unreadable record update leaked; acknowledged filter: {ack}')
        finally:
            if response is not None:
                response.close()
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

if __name__ == '__main__':
    unittest.main()
