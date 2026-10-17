"""Real HTTPS/SigV4 acceptance of the exact starter and its trust boundary."""

import asyncio
import os
import ssl
from unittest.mock import patch

import httpx
import test_s3
from app import create_app
from s3_fixture import TOKEN, SignedS3, projection
from storage import Storage, StorageError
from tls_fixture import Certificates


class HTTPSStorageTests(test_s3.SignedStorageTests):
    # Run the complete signed transfer, rotation, cancellation, restore and real
    # handler-process suite over HTTPS, including its no-replay/cleanup checks.
    async def fixture(self):
        self.certificates = await asyncio.to_thread(Certificates, self.root / "tls")
        return SignedS3(ssl_context=self.certificates.context()), {"AWS_CA_BUNDLE": str(self.certificates.ca)}

    async def rejected_start(self, environment=None, server=None):
        environment = dict(self.env if environment is None else environment)
        server = self.server if server is None else server
        service = Storage(env=environment, spool_root=str(self.spool))
        self.addAsyncCleanup(service.close)
        loop = asyncio.get_running_loop()
        previous = loop.get_exception_handler()
        address = server.server.sockets[0].getsockname()

        def expected_rejection(current_loop, context):
            transport = context.get("transport")
            # A client rejecting this owned certificate can reset the server's
            # handshake. Preserve diagnostics for every unrelated loop failure.
            if (context.get("message") == "Error on transport creation for incoming connection"
                    and isinstance(context.get("exception"), (ConnectionError, ssl.SSLError))
                    and transport is not None):
                try:
                    peer = transport.get_extra_info("sockname")
                except AttributeError:
                    peer = None  # asyncio can clear the SSL protocol before reporting a peer reset.
                if peer in (None, address):
                    return
            if previous is None:
                current_loop.default_exception_handler(context)
            else:
                previous(current_loop, context)

        loop.set_exception_handler(expected_rejection)
        try:
            with self.assertRaises(StorageError) as failure:
                await service.start()
        finally:
            loop.set_exception_handler(previous)
        self.assertEqual(str(failure.exception), "storage_unavailable")
        self.assertFalse(service.ready())
        self.assertEqual(server.acks, [{"revision": "a" * 64, "generation": "a" * 32, "status": "failed"}])
        self.assertEqual(server.requests, [])  # Rejected before sending SigV4 or object bytes.
        app = create_app(lambda: service)
        app.state.storage = service  # Failed lifespan cannot start a serving process.
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app), base_url="http://app",
                                     headers={"Authorization": "Bearer " + TOKEN}) as client:
            self.assertEqual((await client.get("/readyz")).status_code, 503)
            self.assertEqual((await client.get("/objects/hello.bin")).status_code, 503)
        await service.close()

    async def test_explicit_bundle_is_used_with_certificate_and_hostname_checks(self):
        # Spaces in public bundle paths are supported. The injected environment
        # selects trust even when an ambient process setting points elsewhere.
        bundle = self.root / "company trust.pem"
        bundle.write_bytes(self.certificates.ca.read_bytes())
        self.env["AWS_CA_BUNDLE"] = str(bundle)
        with patch.dict(os.environ, {"AWS_CA_BUNDLE": str(self.root / "ambient-does-not-exist")}):
            _app, service, client = await self.application()
            self.assertTrue(service.ready())
            self.assertEqual((await client.get("/objects/hello.bin")).content, b"hello\x00world")
        self.assertTrue(self.server.tls_requests)
        self.assertTrue(all(self.server.tls_requests))

    async def test_default_trust_rejects_private_ca_without_using_ambient_profile_trust(self):
        del self.env["AWS_CA_BUNDLE"]
        config = self.root / "ambient-config"
        config.write_text(f"[default]\nca_bundle = {self.certificates.ca}\n")
        with patch.dict(os.environ, {"AWS_CA_BUNDLE": str(self.certificates.ca), "AWS_CONFIG_FILE": str(config)}):
            await self.rejected_start()

    async def test_unrelated_ca_fails_before_sending_credentials(self):
        other = await asyncio.to_thread(Certificates, self.root / "other-ca")
        self.env["AWS_CA_BUNDLE"] = str(other.ca)
        await self.rejected_start()

    async def test_wrong_hostname_and_expired_certificate_fail_closed(self):
        for name, options in (("wrong-host", {"hostname": "DNS:other.invalid"}), ("expired", {"expired": True})):
            with self.subTest(name=name):
                certificates = await asyncio.to_thread(Certificates, self.root / name, **options)
                server = await self.enterAsyncContext(SignedS3(ssl_context=certificates.context()))
                env, _publish = projection(self.root, server)
                env["AWS_CA_BUNDLE"] = str(certificates.ca)
                await self.rejected_start(env, server)

    async def test_missing_empty_and_invalid_bundle_paths_fail_closed(self):
        # Each failure needs its own coordinator: a failed startup is terminal.
        for name in ("missing", "empty.pem", "invalid.pem", "directory"):
            path = self.root / name
            if name == "empty.pem":
                path.write_text("")
            elif name == "invalid.pem":
                path.write_text("not a certificate")
            elif name == "directory":
                path.mkdir()
            with self.subTest(name=name):
                self.server.acks.clear()
                self.env["AWS_CA_BUNDLE"] = str(path)
                await self.rejected_start()

    async def test_empty_or_boolean_trust_settings_cannot_disable_verification(self):
        for setting in ("", " ", False, True, None, "bad\npath", "bad\0path"):
            with self.subTest(setting=repr(setting)):
                self.server.acks.clear()
                self.env["AWS_CA_BUNDLE"] = setting
                await self.rejected_start()

    async def test_bundle_is_reread_on_reload_and_same_revision_restore(self):
        bundle = self.root / "active-trust.pem"
        good = self.certificates.ca.read_bytes()
        bundle.write_bytes(good)
        self.env["AWS_CA_BUNDLE"] = str(bundle)
        _app, service, client = await self.application()
        lease = service.binding.acquire()
        original = lease.current()[0]
        lease.release()
        bundle.write_text("invalid public trust")
        with self.assertRaises(Exception):
            await service.coordinator.reload()
        self.assertFalse(service.ready())
        self.assertEqual(self.server.acks[-1]["status"], "failed")
        with self.assertRaises(StorageError):
            await service.restore()
        self.assertFalse(service.ready())
        self.assertTrue(all(ack["status"] == "failed" for ack in self.server.acks[1:]))
        replacement = self.root / "next-trust.pem"
        replacement.write_bytes(good)
        os.replace(replacement, bundle)
        await service.coordinator.reload()
        self.assertEqual(self.server.acks[-1]["status"], "applied")
        self.assertFalse(service.ready())  # Ordinary reload cannot clear a failed restore gate.
        await service.restore()
        self.assertTrue(service.ready())
        self.assertEqual(service.generation, 1)
        lease = service.binding.acquire()
        self.assertIsNot(lease.current()[0], original)
        lease.release()
        self.assertEqual((await client.get("/objects/hello.bin")).content, b"hello\x00world")
        self.assertTrue(all(self.server.tls_requests))
        self.assertTrue(all(ack["revision"] == "a" * 64 for ack in self.server.acks))
        self.assertEqual(list(self.spool.iterdir()), [])

    async def test_actual_handler_bad_bundle_stops_without_successful_adoption_or_private_logs(self):
        invalid = self.root / "private-invalid-trust.pem"
        process, _address = await self.handler(AWS_CA_BUNDLE=str(invalid))
        output = (await asyncio.wait_for(process.communicate(), 10))[0].decode()
        self.assertIsNotNone(process.returncode)
        self.assertIn("Application startup failed", output)
        self.assertEqual(self.server.acks, [{"revision": "a" * 64, "generation": "a" * 32, "status": "failed"}])
        self.assertEqual(self.server.requests, [])
        for private in (str(invalid), str(self.certificates.ca), self.server.endpoint, TOKEN, self.server.first[1]):
            self.assertNotIn(private, output)
        self.assertEqual(list(self.spool.iterdir()), [])
