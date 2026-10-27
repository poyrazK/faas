"""Event-loop ownership, cancellation fences and atomic adoption contracts."""

import asyncio
import copy
import json
import os
import signal
import threading
import traceback

import pytest
from faas_sdk.preview.async_postgres_pool_runtime import AsyncPostgresPoolBinding
from faas_sdk.async_secret_reload_runtime import (
    AsyncSecretConsumer,
    AsyncSecretReloadCancellation,
    AsyncSecretReloadCoordinator,
    AsyncSecretReloadIO,
)
from faas_sdk.preview.postgres_pool_runtime import PostgresPoolBindingError
from faas_sdk.secret_reload_runtime import SecretReloadError


def values(password="private-first"):
    return {"DATABASE_URL": f"postgresql://worker:{password}@db.example:6432/app?sslmode=require"}


def environment():
    return {
        "FAAS_SECRETS_SNAPSHOT_FILE": "/run/secrets.json",
        "FAAS_SECRETS_RELOAD_READY_FILE": "/run/secrets.ready",
        "FAAS_SECRETS_RELOAD_ACK_ENDPOINT": "http://127.0.0.1:4100/internal/secrets/ack",
        "FAAS_SECRETS_RELOAD_GENERATION": "a" * 32,
    }


class Harness:
    def __init__(self):
        self.loop = asyncio.get_running_loop()
        self.created, self.events, self.acks = [], [], []
        self.snapshot = {"revision": "a" * 64, "secrets": values()}

    async def factory(self, config, cancel):
        assert asyncio.get_running_loop() is self.loop
        cancel.check()
        pool = {"dsn": config.connection_string, "closed": 0}
        self.created.append(pool)
        self.events.append("factory")
        await asyncio.sleep(0)
        return pool

    async def validate(self, pool, cancel):
        assert asyncio.get_running_loop() is self.loop
        cancel.check()
        self.events.append("validate")

    async def retire(self, pool):
        assert asyncio.get_running_loop() is self.loop
        await asyncio.sleep(0)
        pool["closed"] += 1
        self.events.append("retire")

    def binding(self, **overrides):
        return AsyncPostgresPoolBinding(
            "DATABASE_URL",
            overrides.pop("factory", self.factory),
            validate=overrides.pop("validate", self.validate),
            retire=overrides.pop("retire", self.retire),
            **overrides,
        )

    async def read(self, _path, cancel):
        cancel.check()
        return json.dumps(self.snapshot)

    async def ready(self, _path, cancel):
        cancel.check()
        self.events.append("ready")

    async def post(self, _endpoint, body, cancel):
        cancel.check()
        self.acks.append(json.loads(body))
        self.events.append("ack")
        return 202

    async def sleep(self, _seconds, cancel):
        cancel.check()
        await asyncio.sleep(0)

    def coordinator(self, consumers, **kwargs):
        return AsyncSecretReloadCoordinator(
            consumers, env=environment(), io=AsyncSecretReloadIO(self.read, self.ready, self.post, self.sleep), **kwargs
        )


@pytest.fixture(autouse=True)
def signal_owner():
    previous = signal.signal(signal.SIGHUP, signal.SIG_IGN)
    yield
    signal.signal(signal.SIGHUP, previous)


def test_leases_validation_gate_and_same_loop_retirement():
    async def scenario():
        h = Harness()
        entered, finish = asyncio.Event(), asyncio.Event()

        async def validate(pool, token):
            await h.validate(pool, token)
            if "private-second" in pool["dsn"]:
                entered.set()
                await finish.wait()

        b = h.binding(validate=validate)
        await b.reload(values())
        first, second = b.acquire(), b.acquire()
        old = first.current()
        reload = asyncio.create_task(b.reload(values("private-second")))
        await entered.wait()
        with pytest.raises(PostgresPoolBindingError, match="not ready"):
            b.acquire()
        assert first.current() is old and old["closed"] == 0
        # An unrelated loop task keeps running while validation is suspended.
        assert await asyncio.create_task(asyncio.sleep(0, result="responsive")) == "responsive"
        finish.set()
        await reload
        async with b.acquire() as lease:
            current = lease.current()
            assert current is not old
            copied = copy.copy(first)
            copied.release()
            first.release()
            assert old["closed"] == 0
            with pytest.raises(PostgresPoolBindingError, match="released"):
                copied.current()
            b.invalidate()
            with pytest.raises(PostgresPoolBindingError, match="timed out"):
                await b.wait_for_retirement(0)
            second.release()
        await b.wait_for_retirement(1)
        assert old["closed"] == current["closed"] == 1

    asyncio.run(scenario())


@pytest.mark.parametrize("phase", ["factory", "validate"])
@pytest.mark.parametrize("method", ["task", "token", "invalidate"])
def test_late_cancelled_candidate_never_publishes_and_is_closed_once(phase, method):
    async def scenario():
        h = Harness()
        entered, finish = asyncio.Event(), asyncio.Event()

        async def pause():
            entered.set()
            try:
                await finish.wait()
            except asyncio.CancelledError:
                await finish.wait()  # Simulate a driver suppressing cancellation.

        async def factory(config, token):
            if phase == "factory":
                await pause()
            # Returning a late resource transfers ownership for SDK cleanup.
            return await h.factory(config, AsyncSecretReloadCancellation())

        async def validate(pool, token):
            if phase == "validate":
                await pause()

        b = h.binding(factory=factory, validate=validate)
        stop = asyncio.Event()
        task = asyncio.create_task(b.reload(values(), AsyncSecretReloadCancellation([stop])))
        await entered.wait()
        if method == "task":
            task.cancel()
            with pytest.raises(asyncio.CancelledError):
                await asyncio.wait_for(task, 0.5)
        elif method == "token":
            stop.set()
            with pytest.raises(asyncio.CancelledError):
                await asyncio.wait_for(task, 0.5)
        else:
            b.invalidate()
        with pytest.raises(PostgresPoolBindingError):
            b.acquire()
        with pytest.raises(PostgresPoolBindingError, match="timed out"):
            await b.wait_for_retirement(0.01)
        finish.set()
        if method == "invalidate":
            with pytest.raises(asyncio.CancelledError):
                await task
        await b.wait_for_retirement(1)
        assert len(h.created) == 1 and h.created[0]["closed"] == 1
        with pytest.raises(PostgresPoolBindingError):
            b.acquire()
        await b.reload(values("recovered"))
        b.invalidate()
        await b.wait_for_retirement(1)

    asyncio.run(scenario())


def test_errors_are_redacted_and_retirement_failure_is_sticky():
    async def scenario():
        h = Harness()
        observed = []

        async def validate(pool, _cancel):
            raise RuntimeError(pool["dsn"])

        async def retire(pool):
            await h.retire(pool)
            raise RuntimeError(pool["dsn"])

        b = h.binding(validate=validate, retire=retire, on_retire_error=observed.append)
        with pytest.raises(PostgresPoolBindingError) as error:
            await b.reload(values())
        assert "private-first" not in "".join(traceback.format_exception(error.value))
        for _ in range(2):
            with pytest.raises(PostgresPoolBindingError, match="retirement failed"):
                await b.wait_for_retirement(1)
        assert len(observed) == 1 and h.created[0]["closed"] == 1
        assert "private-first" not in repr(observed)

    asyncio.run(scenario())


def test_reused_owned_pool_is_rejected_without_closing_a_live_lease():
    async def scenario():
        h = Harness()
        pool = {"dsn": "sensitive", "closed": 0}

        async def factory(_config, _cancel):
            return pool

        b = h.binding(factory=factory)
        await b.reload(values())
        lease = b.acquire()
        with pytest.raises(PostgresPoolBindingError):
            await b.reload(values("second"))
        assert lease.current() is pool and pool["closed"] == 0
        lease.release()
        await b.wait_for_retirement(1)
        assert pool["closed"] == 1

    asyncio.run(scenario())


def test_pool_cannot_be_used_from_a_different_event_loop():
    b = None

    async def first():
        nonlocal b
        b = Harness().binding()
        await b.reload(values())
        b.invalidate()
        await b.wait_for_retirement(1)

    asyncio.run(first())

    async def second():
        with pytest.raises(PostgresPoolBindingError, match="another event loop"):
            b.acquire()

    asyncio.run(second())


def test_startup_ack_order_sighup_and_failed_consumer_disable_every_pool():
    async def scenario():
        h = Harness()
        b = h.binding()
        fail = False

        async def other(_snapshot, _cancel):
            h.events.append("other")
            if fail:
                raise RuntimeError("private-first")

        coordinator = h.coordinator([b.secret_consumer(), AsyncSecretConsumer(other, lambda: None)])
        try:
            assert await coordinator.start() == "a" * 64
            assert (
                h.events.index("ready") < h.events.index("validate") < h.events.index("other") < h.events.index("ack")
            )
            assert h.acks == [{"revision": "a" * 64, "status": "applied", "generation": "a" * 32}]
            h.snapshot = {"revision": "b" * 64, "secrets": values("second")}
            os.kill(os.getpid(), signal.SIGHUP)
            async with asyncio.timeout(1):
                while len(h.acks) < 2:
                    await asyncio.sleep(0)
            fail = True
            with pytest.raises(SecretReloadError):
                await coordinator.reload()
            assert h.acks[-1]["status"] == "failed"
            with pytest.raises(PostgresPoolBindingError):
                b.acquire()
            fail = False
            await coordinator.reload()
        finally:
            coordinator.stop()
            await coordinator.wait_stopped(1)
            await b.wait_for_retirement(1)
        assert signal.getsignal(signal.SIGHUP) == signal.SIG_IGN
        assert all(pool["closed"] == 1 for pool in h.created)

    asyncio.run(scenario())


@pytest.mark.parametrize("method", ["task", "deadline", "shutdown", "external"])
def test_cancelled_adoption_does_not_ack_or_publish_and_shutdown_drains(method):
    async def scenario():
        h = Harness()
        entered, finish, external = asyncio.Event(), asyncio.Event(), asyncio.Event()

        async def validate(_pool, _cancel):
            entered.set()
            try:
                await finish.wait()
            except asyncio.CancelledError:
                await finish.wait()

        b = h.binding(validate=validate)
        c = h.coordinator([b.secret_consumer()], cancel=external)
        # Startup itself needs to pass before the deliberately slow rotation.
        finish.set()
        await c.start()
        finish.clear()
        entered.clear()
        h.acks.clear()
        task = asyncio.create_task(c.reload(timeout=0.03 if method == "deadline" else None))
        await entered.wait()
        if method == "task":
            task.cancel()
        elif method == "shutdown":
            c.stop()
        elif method == "external":
            external.set()
        with pytest.raises((asyncio.CancelledError, SecretReloadError)):
            await asyncio.wait_for(task, 0.5)
        with pytest.raises(PostgresPoolBindingError):
            b.acquire()
        assert not h.acks
        finish.set()
        await b.wait_for_retirement(1)
        if method in ("task", "deadline"):
            await c.reload()
            assert len(h.acks) == 1
        c.stop()
        await c.wait_stopped(1)
        await b.wait_for_retirement(1)
        assert all(pool["closed"] == 1 for pool in h.created)

    asyncio.run(scenario())


def test_cancelled_queued_reload_does_not_run_or_invalidate_successful_adoption():
    async def scenario():
        h = Harness()
        b = h.binding()
        c = h.coordinator([b.secret_consumer()])
        await c.start()
        entered, finish = asyncio.Event(), asyncio.Event()
        original_post = h.post

        async def post(*args):
            entered.set()
            await finish.wait()
            return await original_post(*args)

        c._io = AsyncSecretReloadIO(h.read, h.ready, post, h.sleep)
        first = asyncio.create_task(c.reload())
        await entered.wait()
        queued = asyncio.create_task(c.reload(timeout=0.02))
        with pytest.raises(SecretReloadError):
            await queued
        finish.set()
        await first
        async with b.acquire() as lease:
            assert lease.current()["closed"] == 0
        assert len(h.created) == len(h.acks) == 2
        c.stop()
        await c.wait_stopped(1)
        await b.wait_for_retirement(1)

    asyncio.run(scenario())


@pytest.mark.parametrize("code,expected", [(403, 1), (302, 1), (503, 10), (409, 5)])
def test_ack_errors_fail_closed_and_retry_boundedly(code, expected):
    async def scenario():
        h = Harness()
        b = h.binding()

        async def recording(_endpoint, body, _cancel):
            h.acks.append(json.loads(body))
            return code

        h.post = recording
        c = h.coordinator([b.secret_consumer()])
        with pytest.raises(SecretReloadError):
            await c.start()
        await c.wait_stopped(1)
        await b.wait_for_retirement(1)
        assert len(h.acks) == expected
        with pytest.raises(PostgresPoolBindingError):
            b.acquire()

    asyncio.run(scenario())


def test_projection_conflict_rereads_and_only_acknowledges_current_validated_revision():
    async def scenario():
        h = Harness()
        b = h.binding()

        async def post(_endpoint, body, _cancel):
            h.acks.append(json.loads(body))
            if len(h.acks) == 1:
                h.snapshot = {"revision": "b" * 64, "secrets": values("second")}
                return 409
            return 202

        h.post = post
        c = h.coordinator([b.secret_consumer()])
        assert await c.start() == "b" * 64
        assert [ack["revision"] for ack in h.acks] == ["a" * 64, "b" * 64]
        async with b.acquire() as lease:
            assert "second" in lease.current()["dsn"]
            assert "second" not in repr(lease)
        c.stop()
        await c.wait_stopped(1)
        await b.wait_for_retirement(1)
        assert all(pool["closed"] == 1 for pool in h.created)

    asyncio.run(scenario())


@pytest.mark.parametrize("mode", ["placeholder", "malformed", "missing", "async_invalidate", "boolean_validation"])
def test_invalid_startup_never_acknowledges_applied(mode):
    async def scenario():
        h = Harness()
        b = h.binding()
        consumer = b.secret_consumer()
        if mode == "placeholder":
            h.snapshot["revision"] = ""
        elif mode == "malformed":
            h.snapshot["revision"] = "private-invalid-revision"
        elif mode == "missing":

            async def read(*_args):
                raise FileNotFoundError("private-diagnostics")

            h.read = read
        elif mode == "async_invalidate":

            async def invalidate():
                pass

            consumer = AsyncSecretConsumer(consumer.apply, invalidate)
        elif mode == "boolean_validation":

            async def validate(*_args):
                return False

            b._validate = validate
        c = h.coordinator([consumer], startup_timeout=0.02)
        with pytest.raises(SecretReloadError) as error:
            await c.start()
        assert "private-" not in "".join(traceback.format_exception(error.value))
        assert all(ack["status"] == "failed" for ack in h.acks)
        with pytest.raises(PostgresPoolBindingError):
            b.acquire()
        await c.wait_stopped(1)
        await b.wait_for_retirement(1)

    asyncio.run(scenario())


def test_start_refuses_to_replace_framework_signal_handler():
    async def scenario():
        h = Harness()
        b = h.binding()

        def existing(*_args):
            pass

        signal.signal(signal.SIGHUP, existing)
        c = h.coordinator([b.secret_consumer()])
        with pytest.raises(SecretReloadError, match="application handler"):
            await c.start()
        assert signal.getsignal(signal.SIGHUP) is existing
        assert not h.created and not h.acks

    asyncio.run(scenario())


def test_cancelled_lease_context_releases_only_after_driver_context_returns():
    async def scenario():
        h = Harness()
        b = h.binding()
        await b.reload(values())
        entered, returned = asyncio.Event(), asyncio.Event()

        async def operation():
            async with b.acquire() as lease:
                try:
                    entered.set()
                    await asyncio.Future()
                finally:
                    # Models an async driver's rollback/connection return.
                    await asyncio.sleep(0)
                    assert lease.current()["closed"] == 0
                    returned.set()

        task = asyncio.create_task(operation())
        await entered.wait()
        b.invalidate()
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        await b.wait_for_retirement(1)
        assert returned.is_set() and h.created[0]["closed"] == 1

    asyncio.run(scenario())


def test_retirement_wait_cancellation_does_not_cancel_cleanup():
    async def scenario():
        h = Harness()
        entered, finish = asyncio.Event(), asyncio.Event()

        async def retire(pool):
            entered.set()
            await finish.wait()
            await h.retire(pool)

        b = h.binding(retire=retire)
        await b.reload(values())
        b.invalidate()
        await entered.wait()
        wait = asyncio.create_task(b.wait_for_retirement())
        await asyncio.sleep(0)
        wait.cancel()
        with pytest.raises(asyncio.CancelledError):
            await wait
        assert h.created[0]["closed"] == 0
        finish.set()
        await b.wait_for_retirement(1)
        assert h.created[0]["closed"] == 1

    asyncio.run(scenario())


def test_failed_startup_fences_queued_manual_adoption_before_releasing_serialization():
    async def scenario():
        h = Harness()
        entered, finish = asyncio.Event(), asyncio.Event()

        async def validate(_pool, _cancel):
            entered.set()
            await finish.wait()
            raise RuntimeError("private-startup-error")

        b = h.binding(validate=validate)
        c = h.coordinator([b.secret_consumer()])
        start = asyncio.create_task(c.start())
        await entered.wait()
        queued = asyncio.create_task(c.reload())
        await asyncio.sleep(0)
        finish.set()
        results = await asyncio.gather(start, queued, return_exceptions=True)
        assert all(isinstance(result, (SecretReloadError, asyncio.CancelledError)) for result in results)
        assert len(h.created) == 1 and all(ack["status"] == "failed" for ack in h.acks)
        await c.wait_stopped(1)
        await b.wait_for_retirement(1)

    asyncio.run(scenario())


@pytest.mark.parametrize("phase", ["factory", "validate"])
def test_callback_cancellation_diagnostics_are_redacted(phase):
    async def scenario():
        h = Harness()

        async def fail(*_args):
            raise asyncio.CancelledError("private-credential-diagnostic")

        b = h.binding(**{phase: fail})
        with pytest.raises(asyncio.CancelledError) as error:
            await b.reload(values())
        assert "private-" not in "".join(traceback.format_exception(error.value))
        await b.wait_for_retirement(1)
        assert all(pool["closed"] == 1 for pool in h.created)

    asyncio.run(scenario())


def test_default_ack_transport_cancels_slow_headers_and_closes_socket():
    async def scenario():
        h = Harness()
        b = h.binding()
        received, closed = asyncio.Event(), asyncio.Event()

        async def handle(reader, writer):
            try:
                headers = (await reader.readuntil(b"\r\n\r\n")).decode().split("\r\n")
                size = int(
                    next(line.split(":", 1)[1] for line in headers if line.lower().startswith("content-length:"))
                )
                await reader.readexactly(size)
                received.set()
                await reader.read()
            finally:
                writer.close()
                await writer.wait_closed()
                closed.set()

        server = await asyncio.start_server(handle, "127.0.0.1", 0)
        env = environment()
        env["FAAS_SECRETS_RELOAD_ACK_ENDPOINT"] = f"http://127.0.0.1:{server.sockets[0].getsockname()[1]}/ack"
        c = AsyncSecretReloadCoordinator(
            [b.secret_consumer()],
            env=env,
            startup_timeout=0.2,
            io=AsyncSecretReloadIO(read=h.read, ready=h.ready, sleep=h.sleep),
        )
        try:
            start = asyncio.create_task(c.start())
            async with asyncio.timeout(1):
                await received.wait()
                with pytest.raises(SecretReloadError):
                    await start
                await c.wait_stopped(0.5)
                await closed.wait()
            await b.wait_for_retirement(1)
        finally:
            c.stop()
            server.close()
            await server.wait_closed()

    asyncio.run(scenario())


def test_blocking_cancellation_events_are_rejected_before_they_can_block_the_loop():
    async def scenario():
        h = Harness()
        b = h.binding()
        with pytest.raises(SecretReloadError):
            AsyncSecretReloadCancellation([threading.Event()])
        with pytest.raises(SecretReloadError):
            h.coordinator([b.secret_consumer()], cancel=threading.Event())
        c = h.coordinator([b.secret_consumer()])
        await c.start()
        with pytest.raises(SecretReloadError):
            await c.reload(cancel=threading.Event())
        with pytest.raises(PostgresPoolBindingError):
            await b.reload(values(), threading.Event())
        async with b.acquire():
            pass
        c.stop()
        await c.wait_stopped(1)
        await b.wait_for_retirement(1)

    asyncio.run(scenario())
