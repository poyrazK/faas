"""Local guest protocol tests: never connect to a provider or production API."""

from __future__ import annotations

import json
import os
import signal
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from queue import Queue

import pytest

from faas_sdk.preview.object_storage_runtime import ObjectStorageBinding, ObjectStorageBindingError
from faas_sdk.secret_reload_runtime import (
    SecretConsumer,
    SecretReloadCancellation,
    SecretReloadCoordinator,
    SecretReloadError,
    SecretReloadIO,
)


def settings(directory):
    return {
        "FAAS_SECRETS_SNAPSHOT_FILE": str(directory / "snapshot.json"),
        "FAAS_SECRETS_RELOAD_READY_FILE": str(directory / "ready"),
        "FAAS_SECRETS_RELOAD_ACK_ENDPOINT": "http://127.0.0.1:1/ack",
        "FAAS_SECRETS_RELOAD_GENERATION": "1" * 32,
    }


def values(key="private-a"):
    return {
        "ASSETS_ENDPOINT": "https://s3.gregale.test",
        "ASSETS_REGION": "auto",
        "ASSETS_BUCKET": "assets",
        "ASSETS_ACCESS_KEY_ID": "private-access",
        "ASSETS_SECRET_ACCESS_KEY": key,
        "ASSETS_ADDRESSING_STYLE": "path",
    }


class Guest:
    def __init__(self, directory):
        self.env = settings(directory)
        self.raw = json.dumps({"revision": "a" * 64, "secrets": values()})
        self.ready = False
        self.reads = 0
        self.acks = []
        self.codes = []

    def read(self, path, _cancel):
        assert path == self.env["FAAS_SECRETS_SNAPSHOT_FILE"]
        assert self.ready
        self.reads += 1
        return self.raw

    def marker(self, path, _cancel):
        assert path == self.env["FAAS_SECRETS_RELOAD_READY_FILE"]
        assert callable(signal.getsignal(signal.SIGHUP))
        self.ready = True

    def post(self, endpoint, body, _cancel):
        assert endpoint == "http://127.0.0.1:1/ack"
        self.acks.append(body)
        code = self.codes.pop(0) if self.codes else 202
        if isinstance(code, Exception):
            raise code
        return code

    def io(self, **changes):
        return SecretReloadIO(
            **{
                "read": self.read,
                "ready": self.marker,
                "post": self.post,
                "sleep": lambda _seconds, cancel: cancel.check(),
                **changes,
            }
        )

    def snapshot(self, revision, secrets):
        self.raw = json.dumps({"revision": revision * 64, "secrets": secrets})


@pytest.fixture
def runtime(tmp_path):
    guest = Guest(tmp_path)
    owned = []

    def create(consumers, **options):
        coordinator = SecretReloadCoordinator(consumers, env=guest.env, io=options.pop("io", guest.io()), **options)
        owned.append(coordinator)
        return coordinator

    yield guest, create
    for coordinator in owned:
        coordinator.stop()


def test_managed_leases_survive_partial_adoption_and_coordinator_stop(runtime):
    guest, create = runtime
    retired = []
    completed = threading.Event()
    fail = threading.Event()

    def retire(client):
        retired.append(client["key"])
        if client["key"] == "private-failed":
            completed.set()

    def other(_snapshot, _cancel):
        if fail.is_set():
            raise RuntimeError("private-other-consumer")

    storage = ObjectStorageBinding("ASSETS", lambda config: {"key": config.secret_access_key}, retire=retire)
    coordinator = create([storage.secret_consumer(), SecretConsumer(other, lambda: None)])
    coordinator.start()
    with storage.acquire() as old:
        fail.set()
        guest.snapshot("b", values("private-failed"))
        with pytest.raises(SecretReloadError):
            coordinator.reload()
        with pytest.raises(ObjectStorageBindingError):
            storage.acquire()
        assert completed.wait(3)
        assert retired == ["private-failed"]
        assert old.current()[0]["key"] == "private-a"
        assert [json.loads(ack)["status"] for ack in guest.acks] == ["applied", "failed"]
        fail.clear()
        guest.snapshot("c", values("private-recovered"))
        coordinator.reload()
        with storage.acquire() as current:
            coordinator.stop()
            with pytest.raises(ObjectStorageBindingError):
                storage.acquire()
            assert retired == ["private-failed"]
            assert current.current()[0]["key"] == "private-recovered"
    storage.wait_for_retirement(3)
    assert sorted(retired) == ["private-a", "private-failed", "private-recovered"]


def test_start_waits_for_atomic_revision_and_all_consumers(runtime):
    guest, create = runtime
    snapshots = []
    applied = []

    def first(snapshot, _cancel):
        snapshots.append(snapshot)
        with pytest.raises(TypeError):
            snapshot.secrets["ASSETS_SECRET_ACCESS_KEY"] = "changed"
        assert "private-" not in repr(snapshot)
        applied.append("one")

    def second(snapshot, _cancel):
        assert snapshot.secrets["ASSETS_SECRET_ACCESS_KEY"] == "private-a"
        applied.append("two")

    read = guest.read

    def bootstrap(path, cancel):
        raw = read(path, cancel)
        return '{"revision":"","secrets":{}}' if guest.reads == 1 else raw

    def ack(endpoint, body, cancel):
        assert applied == ["one", "two"]
        return guest.post(endpoint, body, cancel)

    coordinator = create(
        [SecretConsumer(first, lambda: None), SecretConsumer(second, lambda: None)],
        io=guest.io(read=bootstrap, post=ack),
    )
    guest.env["FAAS_SECRETS_RELOAD_GENERATION"] = "2" * 32
    assert coordinator.start() == "a" * 64
    assert coordinator.start() == "a" * 64
    assert guest.reads == 2
    assert json.loads(guest.acks[0]) == {"revision": "a" * 64, "status": "applied", "generation": "1" * 32}
    assert len(snapshots) == 1


def test_rotation_revocation_recovery_and_second_consumer_failure(runtime):
    guest, create = runtime
    storage = ObjectStorageBinding("ASSETS", lambda config: config.secret_access_key)
    fail_second = threading.Event()

    def second(_snapshot, _cancel):
        if fail_second.is_set():
            raise ValueError("private-driver-detail")

    coordinator = create([storage.secret_consumer(), SecretConsumer(second, lambda: None)])
    coordinator.start()
    old = storage.current()
    guest.snapshot("b", values("private-b"))
    assert coordinator.reload() == "b" * 64
    assert storage.current() == ("private-b", "assets")
    assert old == ("private-a", "assets")
    guest.snapshot("c", {})
    with pytest.raises(SecretReloadError, match="could not be completed") as error:
        coordinator.reload()
    assert "private-" not in str(error.value)
    with pytest.raises(ObjectStorageBindingError):
        storage.current()
    guest.snapshot("d", values("private-d"))
    assert coordinator.reload() == "d" * 64
    fail_second.set()
    guest.snapshot("e", values("private-e"))
    with pytest.raises(SecretReloadError):
        coordinator.reload()
    with pytest.raises(ObjectStorageBindingError):
        storage.current()
    assert [json.loads(body)["status"] for body in guest.acks] == ["applied", "applied", "failed", "applied", "failed"]


@pytest.mark.parametrize("first_status", ["applied", "failed"])
def test_stale_ack_rereads_and_transient_retries_identical_body(runtime, first_status):
    guest, create = runtime
    storage = ObjectStorageBinding("ASSETS", lambda config: config.secret_access_key)
    guest.codes = [OSError("private-network-detail"), 503, 409, 202]
    if first_status == "failed":
        guest.snapshot("a", {})

    def ack(endpoint, body, cancel):
        result = guest.post(endpoint, body, cancel)
        if result == 409:
            guest.snapshot("b", values("private-b"))
        return result

    read = guest.read

    def reread(path, cancel):
        if guest.reads:
            with pytest.raises(ObjectStorageBindingError):
                storage.current()
        return read(path, cancel)

    coordinator = create([storage.secret_consumer()], io=guest.io(post=ack, read=reread))
    assert coordinator.start() == "b" * 64
    assert len(guest.acks) == 4
    assert len(set(guest.acks[:3])) == 1
    assert json.loads(guest.acks[0])["status"] == first_status
    assert json.loads(guest.acks[-1])["revision"] == "b" * 64


@pytest.mark.parametrize(
    "raw",
    [
        "not-json",
        "null",
        "[]",
        '{"Revision":"a","secrets":{}}',
        '{"revision":null,"secrets":{}}',
        '{"revision":"","secrets":null}',
        json.dumps({"revision": "a" * 64, "secrets": {"key": None}}),
        json.dumps({"revision": "A" * 64, "secrets": {}}),
    ],
)
def test_invalid_snapshot_never_acknowledged(runtime, raw):
    guest, create = runtime
    guest.raw = raw
    coordinator = create([SecretConsumer(lambda _s, _c: None, lambda: None)])
    with pytest.raises(SecretReloadError):
        coordinator.start()
    assert guest.acks == []
    assert not callable(signal.getsignal(signal.SIGHUP))


@pytest.mark.parametrize(("code", "attempts"), [(403, 1), (302, 1), (503, 10), (409, 5)])
def test_ack_bounds_fail_closed(runtime, code, attempts):
    guest, create = runtime
    guest.codes = [code] * attempts
    storage = ObjectStorageBinding("ASSETS", lambda config: config.secret_access_key)
    coordinator = create([storage.secret_consumer()])
    with pytest.raises(SecretReloadError):
        coordinator.start()
    assert len(guest.acks) == attempts
    with pytest.raises(ObjectStorageBindingError):
        storage.current()


def test_read_retry_and_startup_deadline(runtime):
    guest, create = runtime
    read = guest.read
    missing = 0

    def retry(path, cancel):
        nonlocal missing
        missing += 1
        if missing <= 4:
            raise FileNotFoundError("private-path")
        return read(path, cancel)

    coordinator = create([SecretConsumer(lambda _s, _c: None, lambda: None)], io=guest.io(read=retry))
    assert coordinator.start() == "a" * 64
    assert missing == 5
    coordinator.stop()
    guest.acks.clear()
    guest.raw = '{"revision":"","secrets":{}}'
    coordinator = create(
        [SecretConsumer(lambda _s, _c: None, lambda: None)],
        startup_timeout=0.03,
        io=guest.io(sleep=SecretReloadIO().sleep),
    )
    with pytest.raises(SecretReloadError):
        coordinator.start()
    assert guest.acks == []


@pytest.mark.parametrize("async_stage", ["apply", "invalidate"])
def test_async_consumers_cannot_establish_adoption(runtime, async_stage):
    guest, create = runtime

    async def apply(_snapshot, _cancel):
        raise AssertionError("must not run")

    async def invalidate():
        raise AssertionError("must not run")

    consumer = SecretConsumer(
        apply if async_stage == "apply" else lambda _s, _c: None,
        invalidate if async_stage == "invalidate" else lambda: None,
    )
    with pytest.raises(SecretReloadError):
        create([consumer]).start()
    assert [json.loads(body)["status"] for body in guest.acks] == (["failed"] if async_stage == "apply" else [])


def test_stop_and_late_factory_do_not_block_acquisition(runtime):
    guest, create = runtime
    entered, release, drained = threading.Event(), threading.Event(), threading.Event()

    def factory(config):
        if config.secret_access_key == "private-b":
            entered.set()
            assert release.wait(3)
            drained.set()
        return config.secret_access_key

    storage = ObjectStorageBinding("ASSETS", factory)
    coordinator = create([storage.secret_consumer()])
    coordinator.start()
    guest.snapshot("b", values("private-b"))
    errors = Queue()

    def reload():
        try:
            coordinator.reload()
        except SecretReloadError as error:
            errors.put(error)

    caller = threading.Thread(target=reload)
    caller.start()
    try:
        assert entered.wait(2)
        started = time.monotonic()
        with pytest.raises(ObjectStorageBindingError):
            storage.current()
        coordinator.stop()
        assert time.monotonic() - started < 0.5
        caller.join(1)
        assert not caller.is_alive()
        assert "private-" not in str(errors.get(timeout=1))
        assert len(guest.acks) == 1
    finally:
        release.set()
        caller.join(2)
    assert drained.wait(1)
    # Joining the factory gate proves the previous candidate finished publication
    # checks; an already cancelled queued reload cannot replace the disabled state.
    cancelled = threading.Event()
    cancelled.set()
    with pytest.raises(ObjectStorageBindingError):
        storage.reload(values(), cancelled)
    with storage._reloads:
        with pytest.raises(ObjectStorageBindingError):
            storage.current()
    assert len(guest.acks) == 1


def test_factory_serialization_and_invalidation_fence():
    entered, release = threading.Event(), threading.Event()
    calls = []
    errors = Queue()

    def factory(config):
        calls.append(config.secret_access_key)
        if config.secret_access_key == "private-b":
            entered.set()
            assert release.wait(3)
        return config.secret_access_key

    storage = ObjectStorageBinding("ASSETS", factory)
    storage.reload(values())
    queued = threading.Event()
    inner = storage._reloads

    class ObservedAdmission:
        def acquire(self, **options):
            if threading.current_thread().name == "queued-storage-reload":
                queued.set()  # Epoch is captured before admission.
            return inner.acquire(**options)

        def release(self):
            inner.release()

    storage._reloads = ObservedAdmission()

    def reload(key):
        try:
            storage.reload(values(key))
        except ObjectStorageBindingError as error:
            errors.put(error)

    first = threading.Thread(target=reload, args=("private-b",))
    first.start()
    assert entered.wait(1)
    second = threading.Thread(target=reload, args=("private-c",), name="queued-storage-reload")
    second.start()
    assert queued.wait(1)
    storage.invalidate()
    release.set()
    first.join(2)
    second.join(2)
    assert not first.is_alive() and not second.is_alive()
    assert calls == ["private-a", "private-b"]
    assert errors.qsize() == 2
    with pytest.raises(ObjectStorageBindingError):
        storage.current()
    storage.reload(values("private-d"))
    assert storage.current() == ("private-d", "assets")


def test_signal_ownership_and_main_thread_cleanup(runtime):
    guest, create = runtime
    original = signal.getsignal(signal.SIGHUP)
    application = lambda _number, _frame: None
    signal.signal(signal.SIGHUP, application)
    coordinator = create([SecretConsumer(lambda _s, _c: None, lambda: None)])
    try:
        with pytest.raises(SecretReloadError, match="application handler"):
            coordinator.start()
        assert signal.getsignal(signal.SIGHUP) is application
    finally:
        signal.signal(signal.SIGHUP, original)
    with pytest.raises(SecretReloadError):
        coordinator.start()  # Failed startup is terminal, even after the handler changes.
    coordinator = create([SecretConsumer(lambda _s, _c: None, lambda: None)])
    coordinator.start()
    thread = threading.Thread(target=coordinator.stop)
    thread.start()
    thread.join(1)
    assert not thread.is_alive()
    coordinator.stop()
    assert signal.getsignal(signal.SIGHUP) is original
    with pytest.raises(SecretReloadError):
        coordinator.start()


@pytest.mark.parametrize("phase", ["ready", "apply", "invalidate", "missing"])
def test_safe_failures_disable_remaining_consumers(runtime, phase):
    guest, create = runtime
    storage = ObjectStorageBinding("ASSETS", lambda config: config.secret_access_key)
    storage.reload(values())

    def fail(*_args):
        raise RuntimeError("private-driver-detail")

    def missing(*_args):
        raise FileNotFoundError("private-path")

    consumer = SecretConsumer(
        fail if phase == "apply" else lambda _s, _c: None, fail if phase == "invalidate" else lambda: None
    )
    options = {"ready": fail} if phase == "ready" else {"read": missing} if phase == "missing" else {}
    coordinator = create([consumer, storage.secret_consumer()], io=guest.io(**options))
    with pytest.raises(SecretReloadError) as error:
        coordinator.start()
    assert "private-" not in str(error.value)
    with pytest.raises(ObjectStorageBindingError):
        storage.current()
    assert [json.loads(body)["status"] for body in guest.acks] == (["failed"] if phase == "apply" else [])


def test_background_start_fails_closed_and_preserves_signals(runtime):
    guest, create = runtime
    storage = ObjectStorageBinding("ASSETS", lambda config: config.secret_access_key)
    storage.reload(values())
    original = signal.getsignal(signal.SIGHUP)
    coordinator = create([storage.secret_consumer()])
    errors = Queue()

    def start():
        try:
            coordinator.start()
        except SecretReloadError as error:
            errors.put(error)

    caller = threading.Thread(target=start)
    caller.start()
    caller.join(1)
    assert not caller.is_alive()
    assert "main thread" in str(errors.get(timeout=1))
    assert signal.getsignal(signal.SIGHUP) is original
    assert not guest.ready and not guest.acks
    with pytest.raises(ObjectStorageBindingError):
        storage.current()


def test_external_cancellation_disables_while_signal_consumer_is_blocked(runtime):
    guest, create = runtime
    cancel, entered, release = (threading.Event() for _ in range(3))
    disabled = threading.Event()
    active = False

    def apply(snapshot, context):
        nonlocal active
        if snapshot.revision == "b" * 64:
            entered.set()
            assert release.wait(3)
        context.check()
        active = True

    def invalidate():
        nonlocal active
        active = False
        if cancel.is_set():
            disabled.set()

    coordinator = create([SecretConsumer(apply, invalidate)], cancel=cancel)
    coordinator.start()
    guest.snapshot("b", values("private-b"))
    os.kill(os.getpid(), signal.SIGHUP)
    try:
        assert entered.wait(1)
        cancel.set()
        assert disabled.wait(0.5)  # No reload caller; shutdown owns invalidation.
        assert not active and len(guest.acks) == 1
    finally:
        release.set()
        coordinator.stop()


def test_late_connect_cannot_post_after_transport_deadline(monkeypatch):
    release, drained, posted = threading.Event(), threading.Event(), threading.Event()

    class DelayedConnection:
        sock = None

        def __init__(self, *_args, **_options):
            pass

        def connect(self):
            assert release.wait(2)

        def request(self, *_args):
            posted.set()

        def close(self):
            if threading.current_thread().name == "gregale-secret-ack":
                drained.set()

    monkeypatch.setattr("faas_sdk.secret_reload_runtime.http.client.HTTPConnection", DelayedConnection)
    cancel = SecretReloadCancellation([threading.Event()]).with_timeout(0.05)
    try:
        with pytest.raises(SecretReloadError):
            SecretReloadIO().post("http://127.0.0.1:1/ack", b"{}", cancel)
    finally:
        release.set()
    assert drained.wait(1)
    assert not posted.is_set()


def test_real_child_atomic_rotation_revocation_and_shutdown(tmp_path):
    env = settings(tmp_path)
    acknowledgements = Queue()

    class Metadata(BaseHTTPRequestHandler):
        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            acknowledgements.put(body)
            self.send_response(202)
            self.end_headers()

        def log_message(self, *_args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Metadata)
    serving = threading.Thread(target=server.serve_forever)
    serving.start()
    env["FAAS_SECRETS_RELOAD_ACK_ENDPOINT"] = f"http://127.0.0.1:{server.server_port}/ack"

    def atomic(revision, secrets):
        temporary = tmp_path / "next.json"
        temporary.write_text(json.dumps({"revision": revision * 64, "secrets": secrets}))
        temporary.replace(env["FAAS_SECRETS_SNAPSHOT_FILE"])

    atomic("a", values())
    child = r"""
import signal
import threading
from faas_sdk.preview.object_storage_runtime import ObjectStorageBinding, ObjectStorageBindingError
from faas_sdk.secret_reload_runtime import SecretReloadCoordinator
binding = ObjectStorageBinding("ASSETS", lambda c: c.secret_access_key)
done = threading.Event()
runtime = SecretReloadCoordinator([binding.secret_consumer()], cancel=done)
def terminate(*_args):
    done.set()
    runtime.stop()
signal.signal(signal.SIGTERM, terminate)
runtime.start()
done.wait()
runtime.stop()
try:
    binding.current()
    raise SystemExit(2)
except ObjectStorageBindingError:
    pass
"""
    process = None
    try:
        process = subprocess.Popen(
            [sys.executable, "-c", child],
            env={**os.environ, **env, "HTTP_PROXY": "http://127.0.0.1:1"},
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

        def ack(revision, status):
            assert acknowledgements.get(timeout=5) == {
                "revision": revision * 64,
                "status": status,
                "generation": "1" * 32,
            }

        ack("a", "applied")
        assert (tmp_path / "ready").stat().st_mode & 0o777 == 0o600
        for revision, secrets, status in [
            ("b", values("private-b"), "applied"),
            ("c", {}, "failed"),
            ("d", values("private-d"), "applied"),
        ]:
            atomic(revision, secrets)
            process.send_signal(signal.SIGHUP)
            ack(revision, status)
        process.terminate()
        output, errors = process.communicate(timeout=5)
        assert process.returncode == 0, errors.decode()
        assert output == b"" and errors == b""
    finally:
        if process is not None and process.poll() is None:
            process.kill()
            process.communicate(timeout=2)
        server.shutdown()
        server.server_close()
        serving.join(2)


@pytest.mark.parametrize(
    ("key", "value"),
    [
        ("FAAS_SECRETS_SNAPSHOT_FILE", "relative"),
        ("FAAS_SECRETS_RELOAD_READY_FILE", ""),
        ("FAAS_SECRETS_RELOAD_GENERATION", "private-invalid"),
        ("FAAS_SECRETS_RELOAD_ACK_ENDPOINT", "https://user:private-secret@example.test"),
        ("FAAS_SECRETS_RELOAD_ACK_ENDPOINT", "http://example.test:invalid"),
    ],
)
def test_contract_validation_redacts_inputs(tmp_path, key, value):
    env = {**settings(tmp_path), key: value}
    with pytest.raises(SecretReloadError) as error:
        SecretReloadCoordinator([SecretConsumer(lambda _s, _c: None, lambda: None)], env=env)
    assert "private-" not in str(error.value)


def test_default_transport_deadline_bounds_slow_headers(tmp_path):
    received, release = threading.Event(), threading.Event()

    class Metadata(BaseHTTPRequestHandler):
        def do_POST(self):
            self.rfile.read(int(self.headers["Content-Length"]))
            received.set()
            release.wait(2)

        def log_message(self, *_args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Metadata)
    serving = threading.Thread(target=server.serve_forever)
    serving.start()
    cancel = SecretReloadCancellation([threading.Event()]).with_timeout(0.1)
    try:
        started = time.monotonic()
        with pytest.raises(SecretReloadError):
            SecretReloadIO().post(f"http://127.0.0.1:{server.server_port}/ack", b"{}", cancel)
        assert received.is_set()
        assert time.monotonic() - started < 0.5
    finally:
        release.set()
        server.shutdown()
        server.server_close()
        serving.join(2)
