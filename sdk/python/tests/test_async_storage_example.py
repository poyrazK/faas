"""Real aiobotocore signing/streaming against a controlled local S3 fixture."""

import asyncio
import hashlib
import signal

import httpx
import pytest

pytest.importorskip("aiobotocore")
pytest.importorskip("fastapi")

from async_storage_fixture import SignedS3, finish_stream, load_example, open_stream, projection
from faas_sdk.async_object_storage_runtime import AsyncObjectStorageBinding
from faas_sdk.object_storage_runtime import ObjectStorageBindingError
from faas_sdk.secret_reload_runtime import SecretReloadError


@pytest.fixture(autouse=True)
def signal_owner():
    previous = signal.signal(signal.SIGHUP, signal.SIG_IGN)
    yield
    signal.signal(signal.SIGHUP, previous)


def tracked_binding(module):
    binding = module.make_binding()
    created, retired = [], []
    factory, retire = binding._factory, binding._retire

    async def make(config, cancel):
        owned = await factory(config, cancel)
        created.append(owned)
        return owned

    async def close(owned):
        await retire(owned)
        retired.append(owned)

    binding._factory, binding._retire = make, close
    return binding, created, retired


def test_signed_stream_survives_rotation_and_clients_drain_after_body_closure(tmp_path, monkeypatch):
    storage, example = load_example(monkeypatch)
    monkeypatch.setenv("AWS_ACCESS_KEY_ID", "ambient-access-must-not-be-used")
    monkeypatch.setenv("AWS_SECRET_ACCESS_KEY", "ambient-secret-must-not-be-used")
    monkeypatch.setenv("AWS_SESSION_TOKEN", "ambient-token-must-not-be-used")

    async def scenario():
        async with SignedS3() as server:
            binding, created, retired = tracked_binding(storage)
            env, publish = projection(tmp_path, server, server.values())
            app = example.create_app(env=env, binding_factory=lambda: binding)
            async with app.router.lifespan_context(app):
                task, _incoming, outgoing, initial = await open_stream(app)
                old = created[0]
                server.keys = dict([server.second])
                publish("b", server.values(server.second))
                await app.state.coordinator.reload()
                assert old not in retired
                assert [ack["status"] for ack in server.acks] == ["applied", "applied"]
                async with httpx.AsyncClient(transport=httpx.ASGITransport(app), base_url="http://example") as client:
                    assert (await client.get("/objects/hello.txt")).content == b"hello"
                    assert (await client.get("/healthz")).status_code == 200
                server.release.set()
                assert await finish_stream(task, outgoing, initial) == hashlib.sha256(server.data).digest()
                await binding.wait_for_retirement(1)
                assert old in retired
                assert all(access in {server.first[0], server.second[0]} for _method, _path, access in server.requests)
                assert ("GET", "/assets/slow.bin", server.first[0]) in server.requests
            assert len(created) == len(retired) == 2

    asyncio.run(scenario())


@pytest.mark.parametrize("method", ["disconnect", "task"])
def test_disconnect_closes_upstream_body_before_retiring_client(tmp_path, monkeypatch, method):
    storage, example = load_example(monkeypatch)

    async def scenario():
        async with SignedS3() as server:
            binding, created, retired = tracked_binding(storage)
            env, _publish = projection(tmp_path, server, server.values())
            app = example.create_app(env=env, binding_factory=lambda: binding)
            async with app.router.lifespan_context(app):
                task, incoming, _outgoing, _initial = await open_stream(app)
                if method == "disconnect":
                    await incoming.put({"type": "http.disconnect"})
                    await asyncio.wait_for(task, 1)
                else:
                    task.cancel()
                    with pytest.raises(asyncio.CancelledError):
                        await task
                binding.invalidate()
                await binding.wait_for_retirement(1)
                await asyncio.wait_for(server.stream_closed.wait(), 1)
                assert created == retired

    asyncio.run(scenario())


def test_graceful_shutdown_drains_an_admitted_stream_before_closing_client(tmp_path, monkeypatch):
    storage, example = load_example(monkeypatch)

    async def scenario():
        async with SignedS3() as server:
            binding, created, retired = tracked_binding(storage)
            env, _publish = projection(tmp_path, server, server.values())
            app = example.create_app(env=env, binding_factory=lambda: binding)
            context = app.router.lifespan_context(app)
            await context.__aenter__()
            task, _incoming, outgoing, initial = await open_stream(app)
            close = asyncio.create_task(context.__aexit__(None, None, None))
            await asyncio.sleep(0)
            assert not close.done() and not retired
            server.release.set()
            assert await finish_stream(task, outgoing, initial) == hashlib.sha256(server.data).digest()
            await close
            assert created == retired

    asyncio.run(scenario())


def test_provider_errors_and_truncated_streams_are_sanitized_without_replay(tmp_path, monkeypatch):
    storage, example = load_example(monkeypatch)

    async def scenario():
        async with SignedS3() as server:
            binding, created, retired = tracked_binding(storage)
            env, _publish = projection(tmp_path, server, server.values())
            app = example.create_app(env=env, binding_factory=lambda: binding)
            async with app.router.lifespan_context(app):
                async with httpx.AsyncClient(transport=httpx.ASGITransport(app), base_url="http://example") as client:
                    with pytest.raises(storage.StorageRequestError, match="Storage stream failed"):
                        await client.get("/objects/broken.bin")
                    assert sum(path == "/assets/broken.bin" for _method, path, _access in server.requests) == 1
                    server.keys.clear()
                    response = await client.get("/objects/hello.txt")
                    assert response.status_code == 503
                    assert response.text == "Storage temporarily unavailable"
                    binding.invalidate()
                    await binding.wait_for_retirement(1)
                    assert created == retired

    asyncio.run(scenario())


def test_failed_and_cancelled_storage_validation_cannot_ack_applied(tmp_path, monkeypatch):
    storage, example = load_example(monkeypatch)

    async def scenario():
        async with SignedS3() as server:
            binding, created, retired = tracked_binding(storage)
            env, publish = projection(tmp_path, server, server.values())
            app = example.create_app(env=env, binding_factory=lambda: binding)
            async with app.router.lifespan_context(app):
                publish("b", server.values((server.first[0], "private-wrong-secret")))
                with pytest.raises(SecretReloadError):
                    await app.state.coordinator.reload()
                assert server.acks[-1]["status"] == "failed"
                with pytest.raises(ObjectStorageBindingError):
                    binding.acquire()
                publish("c", server.values())
                server.hold_head = True
                server.head_entered.clear()
                cancel = asyncio.Event()
                task = asyncio.create_task(app.state.coordinator.reload(cancel=cancel))
                await asyncio.wait_for(server.head_entered.wait(), 3)
                cancel.set()
                with pytest.raises(SecretReloadError):
                    await task
                await binding.wait_for_retirement(1)
                assert [ack["status"] for ack in server.acks] == ["applied", "failed"]
                server.hold_head = False
                server.head_release.set()
                await app.state.coordinator.reload()
            assert len(created) == len(retired)

    asyncio.run(scenario())


def test_late_response_after_disconnect_is_closed_before_lease_release(monkeypatch):
    storage, _example = load_example(monkeypatch)

    async def scenario():
        entered, finish = asyncio.Event(), asyncio.Event()
        body_closed, retired = [], []

        class Body:
            def close(self):
                body_closed.append(True)

        class Client:
            async def get_object(self, **_kwargs):
                entered.set()
                try:
                    await finish.wait()
                except asyncio.CancelledError:
                    await finish.wait()
                return {"Body": Body(), "ContentLength": 1}

        async def factory(_config, _cancel):
            return storage.OwnedS3(Client(), None)

        async def validate(*_args):
            pass

        async def retire(owned):
            assert body_closed
            retired.append(owned)

        binding = AsyncObjectStorageBinding("FILES", factory, validate=validate, retire=retire)
        values = {
            "FILES_ENDPOINT": "http://127.0.0.1:9000",
            "FILES_REGION": "eu-test-1",
            "FILES_BUCKET": "assets",
            "FILES_ACCESS_KEY_ID": "access",
            "FILES_SECRET_ACCESS_KEY": "secret",
            "FILES_ADDRESSING_STYLE": "path",
        }
        await binding.reload(values)
        service = storage.StorageService(binding)
        messages = asyncio.Queue()
        response = service.download("late.bin")

        async def send(_message):
            pytest.fail("Late cancelled response must not send headers/body")

        task = asyncio.create_task(response({}, messages.get, send))
        await entered.wait()
        await messages.put({"type": "http.disconnect"})
        await asyncio.wait_for(task, 1)
        binding.invalidate()
        with pytest.raises(ObjectStorageBindingError, match="timed out"):
            await binding.wait_for_retirement(0.01)
        assert not body_closed and not retired
        finish.set()
        await binding.wait_for_retirement(1)
        await service.wait_closed()
        assert body_closed == [True] and len(retired) == 1

    asyncio.run(scenario())
