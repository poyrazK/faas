import json
import httpx
from faas_sdk.preview.client import AuthenticatedClient
from faas_sdk.preview.api.runs import invoke_execution_task
from faas_sdk.preview.models.invoke_execution_task_request import InvokeExecutionTaskRequest
from faas_sdk.preview.models.execution_response import ExecutionResponse


def test_task_invocation_keeps_retry_identity_and_typed_lineage():
    requests = []
    task_id = "01234567-89ab-cdef-0123-456789abcdef"
    def handler(request):
        requests.append(request)
        return httpx.Response(202, json={
            "id": task_id, "status": "queued", "runtime": "node24",
            "limits": {"timeout_ms": 1000, "memory_mb": 128, "cpu_millicores": 250,
                       "ephemeral_disk_mb": 64, "max_output_bytes": 1024, "pids_max": 32},
            "output_truncated": False, "created_at": "2026-11-15T00:00:00Z",
            "task": {"id": task_id, "name": "report", "version": 1, "digest": "digest"},
        })
    client = AuthenticatedClient(base_url="https://api.example.test", token="test-key").set_httpx_client(
        httpx.Client(base_url="https://api.example.test", transport=httpx.MockTransport(handler)))
    body = InvokeExecutionTaskRequest(name="report", version=1, input_={"rows": 3})
    first = invoke_execution_task.sync(client=client, body=body, idempotency_key="report-call-123")
    replay = invoke_execution_task.sync(client=client, body=body, idempotency_key="report-call-123")
    assert isinstance(first, ExecutionResponse)
    assert first.task.name == "report"
    assert replay.id == first.id
    assert len(requests) == 2
    for request in requests:
        assert request.url.path == "/v1/run-tasks/invoke"
        assert request.headers["Idempotency-Key"] == "report-call-123"
        assert json.loads(request.content) == {"name": "report", "version": 1, "input": {"rows": 3}}
