"""Async storage ownership, stream leases and coordinated adoption contracts."""

import asyncio
import copy
import json
import signal
import subprocess
import sys
import traceback
from pathlib import Path

import pytest
from faas_sdk.preview.async_object_storage_runtime import AsyncObjectStorageBinding
from faas_sdk.preview.async_postgres_pool_runtime import AsyncPostgresPoolBinding
from faas_sdk.async_secret_reload_runtime import AsyncSecretReloadCoordinator, AsyncSecretReloadIO
from faas_sdk.preview.object_storage_runtime import ObjectStorageBindingError
from faas_sdk.preview.postgres_pool_runtime import PostgresPoolBindingError
from faas_sdk.secret_reload_runtime import SecretReloadError


def values(secret="private-first", bucket="assets"):
    return {
        "FILES_ENDPOINT": "https://s3.example",
        "FILES_REGION": "eu-test-1",
        "FILES_BUCKET": bucket,
        "FILES_ACCESS_KEY_ID": "private-access",
        "FILES_SECRET_ACCESS_KEY": secret,
        "FILES_ADDRESSING_STYLE": "path",
    }


class Harness:
    def __init__(self):
        self.loop = asyncio.get_running_loop()
        self.clients, self.configs, self.acks = [], [], []
        self.snapshot = {"revision": "a" * 64, "secrets": values()}

    async def factory(self, config, cancel):
        assert asyncio.get_running_loop() is self.loop
        cancel.check()
        self.configs.append(config)
        client = {"secret": config.secret_access_key, "closed": 0, "validated": False, "open_bodies": 0}
        self.clients.append(client)
        return client

    async def validate(self, client, bucket, cancel):
        assert asyncio.get_running_loop() is self.loop
        cancel.check()
        assert bucket
        client["validated"] = True

    async def retire(self, client):
        assert asyncio.get_running_loop() is self.loop
        assert client["open_bodies"] == 0
        await asyncio.sleep(0)
        client["closed"] += 1

    def binding(self, **overrides):
        return AsyncObjectStorageBinding(
            "FILES",
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

    async def post(self, _endpoint, body, cancel):
        cancel.check()
        self.acks.append(json.loads(body))
        return 202

    def coordinator(self, consumers):
        return AsyncSecretReloadCoordinator(
            consumers,
            env={
                "FAAS_SECRETS_SNAPSHOT_FILE": "/run/secrets.json",
                "FAAS_SECRETS_RELOAD_READY_FILE": "/run/secrets.ready",
                "FAAS_SECRETS_RELOAD_ACK_ENDPOINT": "http://127.0.0.1:4100/ack",
                "FAAS_SECRETS_RELOAD_GENERATION": "a" * 32,
            },
            io=AsyncSecretReloadIO(read=self.read, ready=self.ready, post=self.post),
        )


@pytest.fixture(autouse=True)
def signal_owner():
    previous = signal.signal(signal.SIGHUP, signal.SIG_IGN)
    yield
    signal.signal(signal.SIGHUP, previous)


def test_streaming_leases_capture_bucket_and_keep_client_until_body_closure():
    async def scenario():
        h = Harness()
        b = h.binding()
        await b.reload(values())
        async with b.acquire() as lease:
            old, bucket = lease.current()
            old["open_bodies"] += 1
            assert old["validated"] and bucket == "assets"
            await b.reload(values("private-second", "archive"))
            assert old["closed"] == 0
            async with b.acquire() as replacement:
                assert replacement.current()[1] == "archive"
                b.invalidate()
            with pytest.raises(ObjectStorageBindingError, match="timed out"):
                await b.wait_for_retirement(0)
            assert "private-" not in repr(lease) and "private-" not in repr(h.configs)
            assert h.configs[0].addressing_style == "path"
            assert h.configs[0].request_checksum_calculation == "when_required"
            assert h.configs[0].response_checksum_validation == "when_required"
            old["open_bodies"] -= 1  # Close the actual response body before lease exit.
            copied = copy.copy(lease)
        copied.release()
        with pytest.raises(ObjectStorageBindingError, match="released"):
            copied.current()
        await b.wait_for_retirement(1)
        assert all(client["closed"] == 1 for client in h.clients)

    asyncio.run(scenario())


@pytest.mark.parametrize("phase", ["factory", "validate"])
def test_cancelled_late_client_is_retired_without_publication_or_ack(phase):
    async def scenario():
        h = Harness()
        entered, finish = asyncio.Event(), asyncio.Event()

        async def pause():
            entered.set()
            try:
                await finish.wait()
            except asyncio.CancelledError:
                await finish.wait()

        async def factory(config, cancel):
            # Ignore cancellation to exercise the SDK's late-result fence.
            if phase == "factory":
                await pause()
            client = {"closed": 0, "open_bodies": 0}
            h.clients.append(client)
            return client

        async def validate(client, bucket, cancel):
            if phase == "validate":
                await pause()

        b = h.binding(factory=factory, validate=validate)
        c = h.coordinator([b.secret_consumer()])
        finish.set()
        await c.start()
        finish.clear()
        entered.clear()
        task = asyncio.create_task(c.reload(timeout=0.05))
        await entered.wait()
        with pytest.raises(SecretReloadError):
            await task
        assert len(h.acks) == 1
        with pytest.raises(ObjectStorageBindingError):
            b.acquire()
        finish.set()
        await b.wait_for_retirement(1)
        assert all(client["closed"] == 1 for client in h.clients)
        c.stop()
        await c.wait_stopped(1)

    asyncio.run(scenario())


@pytest.mark.parametrize("order", ["postgres_first", "storage_first"])
def test_mixed_consumers_only_ack_after_both_validate_and_disable_both_on_failure(order):
    async def scenario():
        h = Harness()
        h.snapshot["secrets"]["DATABASE_URL"] = "postgresql://worker:private-password@db.example:5432/app"
        b = h.binding()
        pools = []
        entered, finish = asyncio.Event(), asyncio.Event()
        fail = False

        async def factory(_config, _cancel):
            pool = {"closed": 0}
            pools.append(pool)
            return pool

        async def validate(_pool, _cancel):
            entered.set()
            await finish.wait()
            if fail:
                raise RuntimeError("private-driver-payload")

        async def retire(pool):
            pool["closed"] += 1

        database = AsyncPostgresPoolBinding("DATABASE_URL", factory, validate=validate, retire=retire)
        consumers = [database.secret_consumer(), b.secret_consumer()]
        c = h.coordinator(consumers if order == "postgres_first" else consumers[::-1])
        start = asyncio.create_task(c.start())
        await entered.wait()
        assert not h.acks
        finish.set()
        await start
        assert h.acks[-1]["status"] == "applied"
        fail = True
        with pytest.raises(SecretReloadError):
            await c.reload()
        assert h.acks[-1]["status"] == "failed"
        for binding, error in [(b, ObjectStorageBindingError), (database, PostgresPoolBindingError)]:
            with pytest.raises(error):
                binding.acquire()
        fail = False
        await c.reload()
        c.stop()
        await c.wait_stopped(1)
        await asyncio.gather(b.wait_for_retirement(1), database.wait_for_retirement(1))
        assert all(client["closed"] == 1 for client in h.clients + pools)

    asyncio.run(scenario())


@pytest.mark.parametrize("mode", ["validation", "revocation", "invalid_config"])
def test_failed_adoption_is_redacted_and_recoverable(mode):
    async def scenario():
        h = Harness()

        async def validate(client, bucket, cancel):
            await h.validate(client, bucket, cancel)
            if client["secret"] == "private-bad":
                raise RuntimeError(client["secret"])

        b = h.binding(validate=validate)
        await b.reload(values())
        changed = values("private-bad")
        if mode == "revocation":
            changed = {}
        elif mode == "invalid_config":
            changed["FILES_ADDRESSING_STYLE"] = "virtual"
        with pytest.raises(ObjectStorageBindingError) as error:
            await b.reload(changed)
        assert "private-" not in "".join(traceback.format_exception(error.value))
        with pytest.raises(ObjectStorageBindingError):
            b.acquire()
        await b.wait_for_retirement(1)
        await b.reload(values("private-recovered"))
        b.invalidate()
        await b.wait_for_retirement(1)
        assert all(client["closed"] == 1 for client in h.clients)

    asyncio.run(scenario())


def test_reused_client_is_not_closed_beneath_existing_stream_and_cleanup_failures_stay_sticky():
    async def scenario():
        h = Harness()
        client = {"closed": 0, "open_bodies": 0}

        async def factory(_config, _cancel):
            return client

        async def retire(value):
            value["closed"] += 1
            raise RuntimeError("private-retirement-payload")

        def observer(_error):
            raise RuntimeError("private-observer-payload")

        b = h.binding(factory=factory, retire=retire, on_retire_error=observer)
        await b.reload(values())
        async with b.acquire() as lease:
            with pytest.raises(ObjectStorageBindingError):
                await b.reload(values("second"))
            assert lease.current()[0] is client and client["closed"] == 0
        for _ in range(2):
            with pytest.raises(ObjectStorageBindingError, match="retirement failed") as error:
                await b.wait_for_retirement(1)
            assert "private-" not in "".join(traceback.format_exception(error.value))
        assert client["closed"] == 1

    asyncio.run(scenario())


def test_runtime_imports_do_not_load_application_drivers_or_frameworks():
    source = Path(__file__).resolve().parents[1]
    subprocess.run(
        [
            sys.executable,
            "-I",
            "-c",
            """
import importlib.abc
import sys
class NoDrivers(importlib.abc.MetaPathFinder):
    def find_spec(self, fullname, path=None, target=None):
        if fullname.split('.')[0] in {'aiobotocore', 'botocore', 'aiohttp', 'psycopg', 'psycopg_pool', 'fastapi'}:
            raise AssertionError('SDK imported an application dependency')
sys.meta_path.insert(0, NoDrivers())
sys.path.insert(0, sys.argv[1])
from faas_sdk.preview.async_object_storage_runtime import AsyncObjectStorageBinding
from faas_sdk.preview.async_postgres_pool_runtime import AsyncPostgresPoolBinding
""",
            str(source),
        ],
        check=True,
        timeout=15,
    )
