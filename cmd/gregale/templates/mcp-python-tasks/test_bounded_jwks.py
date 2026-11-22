"""JWKS byte budgets, total deadlines, and independent cached-key verification."""
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import ssl
import tempfile
import threading
import time
import unittest
from unittest.mock import Mock, patch

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import NameOID

from auth import Authenticator
from bounded_jwks import MAX_JWKS_BYTES
import test_auth_verification as fixtures


class BoundedJWKTests(unittest.TestCase):
    setUpClass = classmethod(fixtures.JWKVerificationTests.setUpClass.__func__)
    token = fixtures.JWKVerificationTests.token
    status = fixtures.JWKVerificationTests.status

    def test_size_limit_is_inclusive_and_oversize_fetches_cool_down(self):
        body = json.dumps({"keys": [self.key]}).encode()
        for size, expected in ((MAX_JWKS_BYTES, 200), (MAX_JWKS_BYTES + 1, 401)):
            auth = Authenticator(fixtures.CONFIG)
            response = fixtures.http_response(body.ljust(size, b" "))
            opener = Mock()
            opener.open.return_value = response
            with patch("urllib.request.build_opener", return_value=opener):
                self.assertEqual(self.status(auth, self.token()), expected)
                self.assertEqual(self.status(auth, self.token()), expected)
            self.assertEqual(opener.open.call_count, 1)
            self.assertTrue(response.closed)

    def test_concurrent_refresh_has_one_deadline_and_discards_late_success(self):
        auth = Authenticator(fixtures.CONFIG)
        auth.jwks_client.timeout = 0.15
        started, release = threading.Event(), threading.Event()

        def blocked_fetch():
            started.set()
            release.wait(2)
            return {"keys": [self.key]}

        token = self.token()
        try:
            with patch.object(auth.jwks_client, "fetch_data", side_effect=blocked_fetch) as fetch:
                with ThreadPoolExecutor(max_workers=12) as workers:
                    futures = [workers.submit(self.status, auth, token) for _ in range(12)]
                    self.assertTrue(started.wait(1))
                    job = auth.jwks_client._refresh_job
                    self.assertEqual([future.result(timeout=1) for future in futures], [401] * 12)
                self.assertEqual(self.status(auth, token), 401)
                self.assertEqual(fetch.call_count, 1)
                release.set()
                self.assertTrue(job["done"].wait(1))
                self.assertIsNone(auth.jwks_client.jwk_set_cache.get())
                self.assertEqual(self.status(auth, token), 401)
                self.assertEqual(fetch.call_count, 1)
        finally:
            release.set()

    def test_tls_body_trickle_is_interrupted_and_cached_keys_remain_available(self):
        body = json.dumps({"keys": [self.key]}).encode()
        started, disconnected = threading.Event(), threading.Event()

        class Provider(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def do_GET(self):
                self.send_response(200)
                self.end_headers()
                try:
                    if self.server.slow:
                        started.set()
                        for _ in range(100):
                            self.wfile.write(b" ")
                            self.wfile.flush()
                            time.sleep(0.02)
                    self.wfile.write(body)
                except (OSError, ssl.SSLError):
                    disconnected.set()

        with tempfile.TemporaryDirectory() as directory:
            private = rsa.generate_private_key(public_exponent=65537, key_size=2048)
            name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "localhost")])
            now = datetime.now(timezone.utc)
            cert = (x509.CertificateBuilder().subject_name(name).issuer_name(name)
                    .public_key(private.public_key()).serial_number(x509.random_serial_number())
                    .not_valid_before(now - timedelta(minutes=1)).not_valid_after(now + timedelta(hours=1))
                    .add_extension(x509.SubjectAlternativeName([x509.DNSName("localhost")]), critical=False)
                    .sign(private, hashes.SHA256()))
            cert_path, key_path = Path(directory) / "cert.pem", Path(directory) / "key.pem"
            cert_path.write_bytes(cert.public_bytes(serialization.Encoding.PEM))
            key_path.write_bytes(private.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
            tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            tls.load_cert_chain(cert_path, key_path)
            provider = ThreadingHTTPServer(("127.0.0.1", 0), Provider)
            provider.socket = tls.wrap_socket(provider.socket, server_side=True)
            provider.slow = False
            thread = threading.Thread(target=provider.serve_forever, daemon=True)
            thread.start()
            try:
                config = {**fixtures.CONFIG, "auth": {**fixtures.CONFIG["auth"],
                          "jwks_url": f"https://localhost:{provider.server_port}/jwks"}}
                auth = Authenticator(config)
                auth.jwks_client.ssl_context = ssl.create_default_context(cafile=str(cert_path))
                auth.jwks_client.timeout = 0.25
                known, unknown = self.token(), self.token("rotated-key")
                self.assertEqual(self.status(auth, known), 200)
                auth.jwks_client._last_successful_fetch -= 31
                provider.slow = True
                before = time.monotonic()
                with ThreadPoolExecutor(max_workers=1) as workers:
                    refresh = workers.submit(self.status, auth, unknown)
                    self.assertTrue(started.wait(1))
                    self.assertEqual(self.status(auth, known), 200)
                    self.assertFalse(refresh.done(), "cached token waited for the slow refresh")
                    self.assertEqual(refresh.result(timeout=1), 401)
                self.assertLess(time.monotonic() - before, 1)
                self.assertTrue(disconnected.wait(1), "deadline did not interrupt the TLS response body")
                self.assertEqual(self.status(auth, known), 200)
            finally:
                provider.shutdown()
                provider.server_close()
                thread.join(2)
