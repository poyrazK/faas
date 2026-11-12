"""Request framing contracts for raw HTTP clients and proxy-forwarded bodies."""

import http.client
import io
import json
import socket
import unittest
from email.message import Message

from http_body import RequestBodyError, read_request_body
import test_server


def headers(*pairs):
    result = Message()
    for name, value in pairs:
        result[name] = value
    return result


class RequestFramingTests(unittest.TestCase):
    def test_exact_body_limit_remains_available_with_either_framing(self):
        body = b"x" * 1024
        self.assertEqual(read_request_body(io.BytesIO(body), headers(("Content-Length", "1024")), 1024), body)
        wire = b"400\r\n" + body + b"\r\n0\r\n\r\n"
        self.assertEqual(read_request_body(io.BytesIO(wire), headers(("Transfer-Encoding", "chunked")), 1024), body)

    def test_chunk_extensions_trailers_and_pipeline_boundary(self):
        stream = io.BytesIO(b'2;ext="value"\r\n{}\r\n0\r\nX-Trace: ignored\r\n\r\nNEXT')
        supplied = headers(("Transfer-Encoding", "Chunked"))
        self.assertEqual(read_request_body(stream, supplied, 1024), b"{}")
        self.assertEqual(stream.read(), b"NEXT")
        self.assertNotIn("X-Trace", supplied)

    def test_invalid_and_ambiguous_framing(self):
        for supplied, body, limit, status in [
            (headers(), b"", 1024, 400),
            (headers(("Content-Length", "2"), ("Content-Length", "2")), b"{}", 1024, 400),
            (headers(("Transfer-Encoding", "chunked"), ("Content-Length", "2")), b"", 1024, 400),
            (headers(("Transfer-Encoding", "chunked"), ("Transfer-Encoding", "chunked")), b"", 1024, 400),
            (headers(("Transfer-Encoding", "gzip, chunked")), b"", 1024, 400),
            (headers(("Content-Length", "+2")), b"{}", 1024, 400),
            (headers(("Content-Length", "9" * 5000)), b"", 1024, 413),
            (headers(("Content-Length", "0")), b"", 1024, 413),
            (headers(("Content-Length", "2")), b"{", 1024, 400),
            (headers(("Transfer-Encoding", "chunked")), b"g\r\n", 1024, 400),
            (headers(("Transfer-Encoding", "chunked")), b"2\r\n{", 1024, 400),
            (headers(("Transfer-Encoding", "chunked")), b"2\r\n{}X\n0\r\n\r\n", 1024, 400),
            (headers(("Transfer-Encoding", "chunked")), b"2\n{}\n0\n\n", 1024, 400),
            (headers(("Transfer-Encoding", "chunked")), b"0\r\nContent-Length: 2\r\n\r\n", 1024, 400),
            (headers(("Transfer-Encoding", "chunked")), b"0\r\nMissing-Colon\r\n\r\n", 1024, 400),
            (headers(("Transfer-Encoding", "chunked")), b"0\r\n", 1024, 400),
            (headers(("Transfer-Encoding", "chunked")), b"0\r\n\r\n", 1024, 413),
            (headers(("Transfer-Encoding", "chunked")), b"400\r\n", 10, 413),
            (headers(("Transfer-Encoding", "chunked")), b"6\r\n123456\r\n6\r\n", 10, 413),
            (headers(("Transfer-Encoding", "chunked")), b"1;" + b"x" * 65536 + b"\r\n", 1 << 20, 413),
            (headers(("Transfer-Encoding", "chunked")), b"0\r\nX: metadata\r\n\r\n", 10, 413),
        ]:
            with self.subTest(headers=list(supplied.items()), body=body[:20], limit=limit):
                with self.assertRaises(RequestBodyError) as caught:
                    read_request_body(io.BytesIO(body), supplied, limit)
                self.assertEqual(caught.exception.status, status)


class ChunkedHTTPTests(unittest.TestCase):
    setUp = test_server.MCPStarterTests.setUp
    tearDown = test_server.MCPStarterTests.tearDown

    def test_chunked_mcp_request_then_another_request_on_the_same_connection(self):
        for version in ("2026-07-28", "2025-11-25"):
            with self.subTest(version=version):
                connection = http.client.HTTPConnection("127.0.0.1", self.http.server_address[1], timeout=3)
                try:
                    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/list"}).encode()
                    for chunked in (True, False):
                        connection.request("POST", "/mcp", body=iter([body[:20], body[20:]]) if chunked else body,
                                           headers={"Content-Type": "application/json", "MCP-Protocol-Version": version, "Mcp-Method": "tools/list"}, encode_chunked=chunked)
                        response = connection.getresponse()
                        self.assertEqual(response.status, 200)
                        self.assertIn("tools", json.loads(response.read())["result"])
                finally:
                    connection.close()

    def test_malformed_framing_closes_the_connection(self):
        for framing, body, status in [
            (b"Transfer-Encoding: chunked\r\nContent-Length: 2\r\n", b"0\r\n\r\n", 400),
            (b"Transfer-Encoding: chunked\r\n", b"invalid\r\n", 400),
            (b"Transfer-Encoding: chunked\r\n", b"100001\r\n", 413),
            (b"Content-Length: 2\r\nContent-Length: 2\r\n", b"{}", 400),
        ]:
            with self.subTest(framing=framing, body=body):
                with socket.create_connection(self.http.server_address, timeout=3) as client:
                    client.sendall(b"POST /mcp HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\n" + framing + b"\r\n" + body)
                    response = http.client.HTTPResponse(client)
                    response.begin()
                    self.assertEqual(response.status, status)
                    self.assertEqual(response.getheader("Connection"), "close")
                    response.read()
                    self.assertEqual(client.recv(1), b"")
