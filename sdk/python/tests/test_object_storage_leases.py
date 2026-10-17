import copy
import http.client
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from faas_sdk.object_storage_runtime import ObjectStorageBinding, ObjectStorageBindingError
from test_object_storage_runtime import values


class Client:
    def __init__(self, key="private-key"):
        self.key = key
        self.closed = 0
        self.body = None
        self.lock = threading.Lock()

    def close(self):
        with self.lock:
            self.closed += 1
            if self.body is not None:
                self.body.close()


class ObjectStorageLeaseTest(unittest.TestCase):
    def binding(self, retire=None):
        return ObjectStorageBinding(
            "ASSETS", lambda config: Client(config.secret_access_key), retire=retire or (lambda client: client.close())
        )

    def worker(self, operation, name=None):
        errors = []

        def run():
            try:
                operation()
            except BaseException as error:
                errors.append(error)

        thread = threading.Thread(target=run, name=name)
        thread.start()
        self.addCleanup(lambda: thread.join(3))
        return thread, errors

    def wait(self, event):
        self.assertTrue(event.wait(3), "owned fixture did not progress")

    def test_rotation_multiple_borrowers_and_concurrent_copied_release(self):
        binding = self.binding()
        with self.assertRaises(ObjectStorageBindingError):
            binding.acquire()
        binding.reload(values())
        with self.assertRaisesRegex(ObjectStorageBindingError, "lease acquisition"):
            binding.current()
        first, second = binding.acquire(), binding.acquire()
        old, bucket = first.current()
        self.assertEqual(bucket, "assets")
        self.assertIs(second.current()[0], old)
        self.assertNotIn("private-", repr(first))
        binding.reload({**values(), "ASSETS_SECRET_ACCESS_KEY": "private-new"})
        current = binding.acquire()
        next_client, bucket = current.current()
        self.assertEqual(next_client.key, "private-new")
        first.release()
        self.assertEqual(old.closed, 0)
        copied = copy.copy(second)
        workers = [self.worker(lambda: (copied.release(), second.release())) for _ in range(20)]
        for thread, errors in workers:
            thread.join(3)
            self.assertFalse(thread.is_alive())
            self.assertEqual(errors, [])
        binding.wait_for_retirement(3)
        self.assertEqual(old.closed, 1)
        self.assertEqual(next_client.closed, 0)
        with self.assertRaisesRegex(ObjectStorageBindingError, "released"):
            second.current()
        binding.invalidate()
        with self.assertRaises(ObjectStorageBindingError):
            binding.acquire()
        self.assertEqual(next_client.closed, 0)
        current.release()
        binding.wait_for_retirement(3)
        self.assertEqual(next_client.closed, 1)

    def test_context_manager_releases_on_exception_and_rejects_reentry(self):
        binding = self.binding()
        binding.reload(values())
        lease = binding.acquire()
        with self.assertRaisesRegex(ValueError, "application"):
            with lease as acquired:
                self.assertIs(acquired, lease)
                client, _ = acquired.current()
                binding.invalidate()
                raise ValueError("application")
        binding.wait_for_retirement(3)
        self.assertEqual(client.closed, 1)
        with self.assertRaises(ObjectStorageBindingError):
            with lease:
                self.fail("released context reentered")

    def test_drain_cancellation_and_slow_callback_do_not_block_new_requests(self):
        entered, finish = threading.Event(), threading.Event()
        self.addCleanup(finish.set)

        def retire(client):
            if client.key == "private-secret":
                entered.set()
                if not finish.wait(3):
                    raise TimeoutError("owned callback timed out")
            client.close()

        binding = self.binding(retire)
        binding.reload(values())
        lease = binding.acquire()
        old, _ = lease.current()
        binding.invalidate()
        cancel = threading.Event()
        cancel.set()
        with self.assertRaisesRegex(ObjectStorageBindingError, "cancelled"):
            binding.wait_for_retirement(3, cancel)
        self.assertEqual(old.closed, 0)
        lease.release()
        self.wait(entered)
        binding.reload({**values(), "ASSETS_SECRET_ACCESS_KEY": "private-new"})
        with binding.acquire():
            binding.invalidate()
        with self.assertRaisesRegex(ObjectStorageBindingError, "timed out"):
            binding.wait_for_retirement(0.02)
        finish.set()
        binding.wait_for_retirement(3)
        self.assertEqual(old.closed, 1)
        for invalid in (-1, float("nan"), float("inf")):
            with self.assertRaises(ObjectStorageBindingError):
                binding.wait_for_retirement(invalid)

    def test_late_candidate_cleanup_and_queued_factory_fence(self):
        for mode in ("invalidate", "cancel"):
            with self.subTest(mode=mode):
                entered, finish = threading.Event(), threading.Event()
                self.addCleanup(finish.set)
                candidate = Client()
                calls = []

                def factory(_config):
                    calls.append(1)
                    entered.set()
                    if not finish.wait(3):
                        raise TimeoutError("owned factory timed out")
                    return candidate

                binding = ObjectStorageBinding("ASSETS", factory, retire=lambda client: client.close())
                queued_admission = threading.Event()
                inner = binding._reloads

                class ObservedAdmission:
                    def acquire(self, **options):
                        if threading.current_thread().name == "queued-lease-reload":
                            queued_admission.set()
                        return inner.acquire(**options)

                    def release(self):
                        inner.release()

                binding._reloads = ObservedAdmission()
                cancel = threading.Event()
                thread, errors = self.worker(lambda: binding.reload(values(), cancel))
                self.wait(entered)
                queued = (
                    self.worker(lambda: binding.reload(values()), name="queued-lease-reload")
                    if mode == "invalidate"
                    else None
                )
                if queued is not None:
                    self.wait(queued_admission)  # Captured epoch precedes admission.
                    binding.invalidate()
                else:
                    cancel.set()
                with self.assertRaisesRegex(ObjectStorageBindingError, "timed out"):
                    binding.wait_for_retirement(0.02)
                finish.set()
                thread.join(3)
                self.assertFalse(thread.is_alive())
                self.assertEqual(len(errors), 1)
                self.assertIsInstance(errors[0], ObjectStorageBindingError)
                if queued is not None:
                    queued[0].join(3)
                    self.assertFalse(queued[0].is_alive())
                    self.assertEqual(len(queued[1]), 1)
                binding.wait_for_retirement(3)
                self.assertEqual(candidate.closed, 1)
                self.assertEqual(len(calls), 1)
                with self.assertRaises(ObjectStorageBindingError):
                    binding.acquire()

    def test_retirement_failure_and_observer_are_sanitized_sticky_and_not_retried(self):
        called, observed = [], []
        notification = threading.Event()

        def retire(_client):
            called.append(1)
            raise RuntimeError("private-driver")

        def observer(error):
            observed.append(error)
            notification.set()
            raise RuntimeError("private-observer")

        binding = ObjectStorageBinding("ASSETS", lambda _config: Client(), retire=retire, on_retire_error=observer)
        binding.reload(values())
        binding.invalidate()
        for _ in range(2):
            with self.assertRaises(ObjectStorageBindingError) as error:
                binding.wait_for_retirement(3)
            self.assertNotIn("private-", str(error.exception))
        self.wait(notification)
        self.assertNotIn("private-", str(observed[0]))
        self.assertEqual(called, [1])

    def test_reused_tracked_client_is_rejected_without_closing_its_borrower(self):
        client = Client()
        binding = ObjectStorageBinding("ASSETS", lambda _config: client, retire=lambda c: c.close())
        binding.reload(values())
        lease = binding.acquire()
        with self.assertRaisesRegex(ObjectStorageBindingError, "reused an owned client"):
            binding.reload(values())
        self.assertEqual(client.closed, 0)
        self.assertIs(lease.current()[0], client)
        lease.release()
        binding.wait_for_retirement(3)
        self.assertEqual(client.closed, 1)

    def test_http_body_completes_across_rotation_and_revocation(self):
        finish = threading.Event()
        self.addCleanup(finish.set)

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(200)
                self.send_header("Content-Length", "10")
                self.end_headers()
                self.wfile.write(b"first-")
                self.wfile.flush()
                if finish.wait(3):
                    self.wfile.write(b"last")

            def log_message(self, *_args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        worker = threading.Thread(target=server.serve_forever)
        worker.start()
        connection = http.client.HTTPConnection("127.0.0.1", server.server_port, timeout=3)
        binding = self.binding()
        try:
            binding.reload(values())
            with binding.acquire() as lease:
                client, _ = lease.current()
                connection.request("GET", "/")
                response = connection.getresponse()
                client.body = response
                try:
                    self.assertEqual(response.read(6), b"first-")
                    binding.reload(values())
                    binding.invalidate()
                    self.assertEqual(client.closed, 0)
                    finish.set()
                    self.assertEqual(response.read(), b"last")
                finally:
                    response.close()
            binding.wait_for_retirement(3)
            self.assertEqual(client.closed, 1)
        finally:
            finish.set()
            connection.close()
            binding.invalidate()
            server.shutdown()
            worker.join(3)
            server.server_close()
