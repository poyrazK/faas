import hashlib
import io
import json
from types import SimpleNamespace

import httpx
import pytest
from faas_sdk.preview import Task, TaskDeliveryError, TaskDestination, TaskExecutionError, TaskSubmissionError

RUN_ID = "11111111-1111-4111-8111-111111111111"
TASK_ID = "22222222-2222-4222-8222-222222222222"
FILE_ID = "33333333-3333-4333-8333-333333333333"
DELIVERY_ID = "44444444-4444-4444-8444-444444444444"


def receipt(status="succeeded", **extra):
    return {
        "id": RUN_ID,
        "task": {"id": TASK_ID, "name": "pdf-report", "version": 1, "digest": "recipe"},
        "status": status,
        "runtime": "python313",
        "result": {"words": 3},
        "limits": {
            "timeout_ms": 1000,
            "memory_mb": 128,
            "cpu_millicores": 250,
            "ephemeral_disk_mb": 64,
            "max_output_bytes": 1024,
            "pids_max": 32,
        },
        "output_truncated": False,
        "created_at": "2026-11-17T00:00:00Z",
        "artifacts": [{"name": "report.html", "size_bytes": 10, "sha256": "sha256:" + "a" * 64, "content": None}],
        **extra,
    }


def delivery(status):
    return {
        "id": DELIVERY_ID,
        "status": status,
        "size_bytes": 10,
        "content_type": "text/html",
        "receipt_url": "/receipt",
        "created_at": "2026-11-17T00:00:00Z",
    }


class Backend:
    def __init__(self):
        self.uploads = self.admissions = self.delivery_reads = 0
        self.keys, self.delivery_keys = [], []
        self.prepared = False

    def handle(self, request):
        path = request.url.path
        if path == "/v1/execution-files":
            body = json.loads(request.content)
            return httpx.Response(
                200,
                json={
                    "id": FILE_ID,
                    "status": "missing",
                    "size_bytes": body["size_bytes"],
                    "sha256": body["sha256"],
                    "content_url": "/content",
                    "upload_required": True,
                },
            )
        if path == f"/v1/execution-files/{FILE_ID}/content":
            self.uploads += 1
            assert request.content == b"PDF input"
            assert request.headers["X-Content-SHA256"] == "sha256:" + hashlib.sha256(b"PDF input").hexdigest()
            return httpx.Response(
                200, json={"id": FILE_ID, "status": "ready", "size_bytes": 9, "content_url": "/content"}
            )
        if path == "/v1/run-tasks/invoke":
            assert self.prepared
            self.keys.append(request.headers["Idempotency-Key"])
            if not self.uploads:
                return httpx.Response(409, json={"code": "execution_files_not_ready"})
            self.admissions += 1
            return httpx.Response(202, json=receipt("queued" if self.admissions == 1 else "succeeded"))
        if path == f"/v1/executions/{RUN_ID}":
            return httpx.Response(200, json=receipt())
        if path == "/v1/apps/reports/run-artifact-deliveries/reports":
            self.delivery_keys.append(json.loads(request.content)["idempotency_key"])
            return httpx.Response(202, json=delivery("completed" if self.delivery_reads else "pending"))
        if path.endswith("/" + DELIVERY_ID):
            self.delivery_reads += 1
            return httpx.Response(200, json=delivery("completed"))
        raise AssertionError(path)


def fake_client(handler):
    transport = httpx.MockTransport(handler)
    sync = httpx.Client(base_url="https://api.example.test", transport=transport)
    asynchronous = httpx.AsyncClient(base_url="https://api.example.test", transport=transport)
    return SimpleNamespace(
        httpx_client=sync, async_httpx_client=asynchronous, inner=SimpleNamespace(get_httpx_client=lambda: sync)
    )


@pytest.mark.parametrize("asynchronous", [False, True])
@pytest.mark.asyncio
async def test_task_composes_inputs_results_and_delivery_without_uploading_on_replay(asynchronous):
    backend = Backend()
    client = fake_client(backend.handle)
    task = Task(client, "pdf-report", 1, lambda value: int(value["words"]))
    intents = []

    def prepared(intent):
        backend.prepared = True
        intents.append(intent)

    options = {
        "idempotency_key": "customer-42",
        "files": {"input.pdf": io.BytesIO(b"PDF input")},
        "on_prepared": prepared,
        "poll_interval": 0.001,
        "deliver": TaskDestination("reports", "reports", {"report.html": "text/html"}),
    }
    try:
        if asynchronous:
            first = await task.arun({"title": "Report"}, **options)
            replay = await task.arun({"title": "Report"}, **options)
            recovered = await task.asubmit(intents[0])
        else:
            first = task.run({"title": "Report"}, **options)
            replay = task.run({"title": "Report"}, **options)
            recovered = task.submit(intents[0])
        assert first.result == replay.result == 3
        assert first.receipt.id == replay.receipt.id == recovered.id
        assert backend.uploads == backend.delivery_reads == 1
        assert backend.keys == ["customer-42"] * 4
        assert backend.delivery_keys[0] == backend.delivery_keys[1]
        assert options["files"]["input.pdf"].tell() == 0
    finally:
        client.httpx_client.close()
        await client.async_httpx_client.aclose()


@pytest.mark.parametrize("status", ["failed", "succeeded"])
def test_contract_and_execution_failure_never_reach_result_decoder_or_storage(status):
    paths = []

    def handle(request):
        paths.append(request.url.path)
        return httpx.Response(202, json=receipt(status, result_validation={"status": "invalid"}))

    client = fake_client(handle)

    def decode(_):
        raise AssertionError("invalid result decoded")

    try:
        with pytest.raises(TaskExecutionError):
            Task(client, "pdf-report", 1, decode).run(
                {}, idempotency_key="call", deliver=TaskDestination("reports", "reports", {"report.html": "text/html"})
            )
        assert paths == ["/v1/run-tasks/invoke"]
    finally:
        client.httpx_client.close()


def test_uncertain_admission_preserves_an_unmodified_persistable_intent():
    calls = []

    def handle(request):
        calls.append(request)
        return httpx.Response(202, content=b"not-json")

    client = fake_client(handle)

    def observer(intent):
        intent.request["input"] = {"title": "Mutated"}

    try:
        with pytest.raises(TaskSubmissionError) as caught:
            Task(client, "pdf-report", 1, lambda value: value).start(
                {"title": "Original"}, idempotency_key="stable", on_prepared=observer
            )
        assert caught.value.intent.request["input"] == {"title": "Original"}
        assert caught.value.intent.idempotency_key == "stable"
        assert len(calls) == 1
    finally:
        client.httpx_client.close()


@pytest.mark.parametrize("asynchronous", [False, True])
@pytest.mark.asyncio
async def test_observation_timeout_preserves_running_task_and_pending_delivery(asynchronous):
    methods = []

    def handle(request):
        methods.append(request.method)
        if request.url.path.startswith("/v1/apps/"):
            return httpx.Response(202, json=delivery("pending"))
        return httpx.Response(202, json=receipt("running"))

    client = fake_client(handle)
    task = Task(client, "pdf-report", 1, lambda value: value)
    try:
        run = (
            await task.astart({}, idempotency_key="stable")
            if asynchronous
            else task.start({}, idempotency_key="stable")
        )
        with pytest.raises(TimeoutError):
            if asynchronous:
                await run.aresult(timeout=0.01, poll_interval=0.001)
            else:
                run.result(timeout=0.01, poll_interval=0.001)
        assert str(run.id) == RUN_ID
        assert run.receipt.status == "running"
        completed = task.attach(type(run.receipt).from_dict(receipt()))
        with pytest.raises(TaskDeliveryError) as caught:
            destination = TaskDestination("reports", "reports", {"report.html": "text/html"})
            if asynchronous:
                await completed.adeliver(destination, timeout=0.01, poll_interval=0.001)
            else:
                completed.deliver(destination, timeout=0.01, poll_interval=0.001)
        assert str(caught.value.receipt.id) == RUN_ID
        assert str(caught.value.delivery.id) == DELIVERY_ID
        assert caught.value.delivery.status == "pending"
        assert methods.count("POST") == 2
        assert "DELETE" not in methods
    finally:
        client.httpx_client.close()
        await client.async_httpx_client.aclose()
