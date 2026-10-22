import asyncio
import json
import time

import httpx
import pytest

from faas_sdk.queue_consumer import QueueConsumer


class FakeQueue:
    """Serves the lease API from memory; an empty lease waits briefly like the server's long-poll."""

    def __init__(self, payloads, extend_fails=False):
        self.calls = []
        self.pending = [
            {
                "id": f"m{i}",
                "receipt": f"m{i}.r",
                "payload": payload,
                "headers": {"k": "v"},
                "attempt": 1,
                "enqueued_at": "1970-01-01T00:00:00Z",
            }
            for i, payload in enumerate(payloads)
        ]
        self.extend_fails = extend_fails

    async def handle(self, request: httpx.Request) -> httpx.Response:
        if request.url.host == "127.0.0.1":
            self.calls.append((f"identity:{request.url.params.get('audience')}", {}, None))
            return httpx.Response(200, json={"access_token": "eyJ.workload.token", "expires_in": 300})
        body = json.loads(request.content or b"{}")
        verb = request.url.path.split(":")[1] if ":" in request.url.path else "lease"
        self.calls.append((verb, body, request.headers.get("authorization")))
        if verb == "lease":
            taken = self.pending[: body["max_messages"]]
            del self.pending[: len(taken)]
            if not taken:
                await asyncio.sleep(0.03)
            return httpx.Response(200, json={"messages": taken})
        held = not (verb == "extend" and self.extend_fails)
        receipts = body["receipts"]
        failed = [] if held else [{"receipt": r, "reason": "lease_expired"} for r in receipts]
        return httpx.Response(200, json={"succeeded": receipts if held else [], "failed": failed})

    def verbs(self, verb):
        return [call for call in self.calls if call[0] == verb]

    def consumer(self, handler, **options):
        settings = {
            "app": "worker",
            "queue": "jobs",
            "api_url": "https://api.test",
            "api_key": "key",
            "async_client": httpx.AsyncClient(transport=httpx.MockTransport(self.handle)),
        }
        settings.update(options)
        return QueueConsumer(handler=handler, **settings)


async def until(check, seconds=3.0):
    deadline = time.monotonic() + seconds
    while not check():
        if time.monotonic() > deadline:
            raise AssertionError("condition not met in time")
        await asyncio.sleep(0.005)


@pytest.mark.asyncio
async def test_acks_handled_messages_and_nacks_failures():
    fake = FakeQueue([{"ok": True}, {"ok": False}])
    errors = []

    async def handle(message):
        assert message.headers["k"] == "v"
        if not message.payload["ok"]:
            raise RuntimeError("boom")

    consumer = fake.consumer(handle, concurrency=2, retry_delay_seconds=5, on_error=errors.append)
    run = asyncio.create_task(consumer.run())
    await until(lambda: fake.verbs("ack") and fake.verbs("nack"))
    consumer.stop()
    await run
    lease = fake.verbs("lease")[0]
    assert lease[1] == {"max_messages": 2, "visibility_timeout_seconds": 30, "wait_seconds": 20}
    assert lease[2] == "Bearer key"
    assert fake.verbs("ack")[0][1] == {"receipts": ["m0.r"]}
    nack = fake.verbs("nack")[0][1]
    assert nack["receipts"] == ["m1.r"] and nack["delay_seconds"] == 5 and "boom" in nack["error"]
    assert any(isinstance(error, RuntimeError) and str(error) == "boom" for error in errors)


@pytest.mark.asyncio
async def test_uses_a_cached_workload_token():
    fake = FakeQueue([1, 2, 3])

    async def handle(message):
        return None

    consumer = fake.consumer(handle, api_key=None, identity_endpoint="http://127.0.0.1:9/token")
    run = asyncio.create_task(consumer.run())
    await until(lambda: len(fake.verbs("ack")) == 3)
    consumer.stop()
    await run
    assert [call[0] for call in fake.calls if call[0].startswith("identity")] == ["identity:gregale:queues"]
    assert all(call[2] == "Bearer eyJ.workload.token" for call in fake.calls if not call[0].startswith("identity"))


@pytest.mark.asyncio
async def test_never_runs_more_handlers_than_concurrency():
    fake = FakeQueue(list(range(7)))
    active = 0
    peak = 0

    async def handle(message):
        nonlocal active, peak
        active += 1
        peak = max(peak, active)
        await asyncio.sleep(0.015)
        active -= 1

    consumer = fake.consumer(handle, concurrency=2)
    run = asyncio.create_task(consumer.run())
    await until(lambda: len(fake.verbs("ack")) == 7)
    consumer.stop()
    await run
    assert peak == 2
    assert all(call[1]["max_messages"] <= 2 for call in fake.verbs("lease"))


@pytest.mark.asyncio
async def test_extends_and_reports_a_lost_lease():
    fake = FakeQueue(["slow"], extend_fails=True)
    lost = asyncio.Event()

    async def handle(message):
        await message.lease_lost.wait()
        lost.set()

    consumer = fake.consumer(handle, visibility_timeout_seconds=2)
    run = asyncio.create_task(consumer.run())
    await asyncio.wait_for(lost.wait(), 4)
    consumer.stop()
    await run
    assert fake.verbs("extend")[0][1] == {"receipts": ["m0.r"], "visibility_timeout_seconds": 2}
    assert not fake.verbs("ack") and not fake.verbs("nack")


@pytest.mark.asyncio
async def test_stop_finishes_in_flight_work_without_leasing_more():
    fake = FakeQueue(["first", "second"])
    started = asyncio.Event()
    release = asyncio.Event()

    async def handle(message):
        started.set()
        await release.wait()

    consumer = fake.consumer(handle)
    run = asyncio.create_task(consumer.run())
    await started.wait()
    consumer.stop()
    leases_at_stop = len(fake.verbs("lease"))
    release.set()
    await run
    assert [call[1]["receipts"] for call in fake.verbs("ack")] == [["m0.r"]]
    assert len(fake.verbs("lease")) == leases_at_stop


@pytest.mark.asyncio
async def test_sync_handlers_run_off_the_event_loop():
    fake = FakeQueue(["blocking"])

    def handle(message):
        time.sleep(0.05)

    consumer = fake.consumer(handle)
    run = asyncio.create_task(consumer.run())
    await until(lambda: fake.verbs("ack"))
    consumer.stop()
    await run


@pytest.mark.parametrize(
    "bad",
    [
        {"api_url": "http://api.test"},
        {"concurrency": 0},
        {"concurrency": 101},
        {"wait_seconds": 21},
        {"visibility_timeout_seconds": 1},
        {"concurrency": True},
        {"api_key": None, "identity_endpoint": "http://metadata.example/token"},
        {"app": ""},
    ],
)
def test_rejects_unsafe_options(bad):
    settings = {"app": "worker", "queue": "jobs", "api_url": "https://api.test", "api_key": "key"}
    settings.update(bad)
    with pytest.raises(ValueError):
        QueueConsumer(handler=lambda message: None, **settings)


@pytest.mark.asyncio
async def test_qualification_candidate_leases_through_platform_url(monkeypatch):
    fake = FakeQueue([{"smoke": True}])
    urls = []

    async def recording(request):
        urls.append(str(request.url))
        return await fake.handle(request)

    monkeypatch.setenv("GREGALE_QUEUE_LEASE_URL", "http://10.100.0.1:10081/v1/internal/environment-qualification-queues")
    monkeypatch.delenv("FAAS_WORKLOAD_IDENTITY_ENDPOINT", raising=False)
    consumer = QueueConsumer(
        app="worker",
        queue="jobs",
        handler=lambda message: None,
        api_url="https://api.test",
        async_client=httpx.AsyncClient(transport=httpx.MockTransport(recording)),
    )
    task = asyncio.create_task(consumer.run())
    await until(lambda: fake.verbs("ack"))
    consumer.stop()
    await task
    assert all(call[2] is None and not call[0].startswith("identity") for call in fake.calls)
    leases = "http://10.100.0.1:10081/v1/internal/environment-qualification-queues/v1/apps/worker/queues/jobs/leases"
    assert all(url.startswith(leases) for url in urls), urls
    assert any(url.endswith("/leases:ack") for url in urls)


@pytest.mark.parametrize(
    "bad", ["http://api.gregale.dev", "http://8.8.8.8:10081", "http://127.0.0.1:10081", "http://u@10.0.0.1", "ftp://10.0.0.1"]
)
def test_rejects_a_public_platform_lease_url(monkeypatch, bad):
    monkeypatch.setenv("GREGALE_QUEUE_LEASE_URL", bad)
    with pytest.raises(ValueError):
        QueueConsumer(app="worker", queue="jobs", handler=lambda message: None)
