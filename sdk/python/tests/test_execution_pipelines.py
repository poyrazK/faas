import json

import httpx
from faas_sdk.preview.api.runs import cancel_execution_pipeline, create_managed_execution_workflow_v2
from faas_sdk.preview.client import AuthenticatedClient
from faas_sdk.preview.models.create_managed_execution_workflow_request import CreateManagedExecutionWorkflowRequest
from faas_sdk.preview.models.managed_execution_workflow_response import ManagedExecutionWorkflowResponse


def test_pipeline_v2_batch_and_attempt_lineage():
    requests = []

    def handler(request):
        requests.append(request)
        if request.url.path.endswith("/cancel"):
            return httpx.Response(
                200,
                json={
                    "workflow_id": "batch:one",
                    "run_count": 0,
                    "status_counts": {
                        k: 0
                        for k in (
                            "queued",
                            "restoring",
                            "running",
                            "succeeded",
                            "failed",
                            "timed_out",
                            "out_of_memory",
                            "cancelled",
                        )
                    },
                    "usage": {"wall_time_ms": 0, "cpu_time_ms": 0, "peak_memory_mb": 0, "output_bytes": 0},
                },
            )
        return httpx.Response(
            202,
            json={
                "workflow_id": "batch:one",
                "plan_id": "a" * 24,
                "status": "queued",
                "step_count": 1,
                "next_step": 1,
                "created_at": "2026-11-16T00:00:00Z",
                "pipeline": {
                    "deadline_at": "2026-11-16T01:00:00Z",
                    "steps": [
                        {
                            "label": "one",
                            "max_attempts": 2,
                            "attempts": [
                                {
                                    "run_id": "01234567-89ab-cdef-0123-456789abcdef",
                                    "attempt": 2,
                                    "previous_run_id": "11234567-89ab-cdef-0123-456789abcdef",
                                    "status": "queued",
                                }
                            ],
                        }
                    ],
                },
            },
        )

    client = AuthenticatedClient(base_url="https://api.example.test", token="key").set_httpx_client(
        httpx.Client(base_url="https://api.example.test", transport=httpx.MockTransport(handler))
    )
    body = CreateManagedExecutionWorkflowRequest.from_dict(
        {
            "workflow_id": "batch:one",
            "version": "v1",
            "batch": {"name": "report", "version": 2, "items": [{"label": "one", "input": {"rows": 3}}]},
        }
    )
    out = create_managed_execution_workflow_v2.sync(client=client, body=body, idempotency_key="pipeline-call-123")
    assert isinstance(out, ManagedExecutionWorkflowResponse)
    assert str(out.pipeline.steps[0].attempts[0].previous_run_id).startswith("11234567")
    assert requests[0].url.path == "/v2/execution-workflows"
    assert json.loads(requests[0].content)["batch"]["items"][0]["label"] == "one"
    cancel_execution_pipeline.sync("batch:one", client=client)
    assert requests[1].url.path == "/v1/execution-workflows/batch:one/cancel"


def test_pipeline_completion_round_trip_and_legacy_absence():
    from faas_sdk.preview.models.execution_pipeline_response import ExecutionPipelineResponse
    from faas_sdk.preview.types import UNSET

    legacy = ExecutionPipelineResponse.from_dict({"deadline_at": "2026-11-16T01:00:00Z", "steps": []})
    assert legacy.completion is UNSET
    manifest = {
        "version": "v1",
        "completion_id": "01234567-89ab-cdef-0123-456789abcdef",
        "workflow_id": "report",
        "outcome": "cancelled",
        "finished_at": "2026-11-16T00:00:00Z",
        "receipt": "/v1/execution-workflows/report",
        "usage": {"wall_time_ms": 7, "cpu_time_ms": 3, "peak_memory_mb": 8},
        "steps": [
            {
                "label": "one",
                "outcome": "cancelled",
                "attempts": [
                    {
                        "run_id": "11234567-89ab-cdef-0123-456789abcdef",
                        "attempt": 1,
                        "status": "cancelled",
                        "receipt": "/v1/executions/run",
                        "usage": {"wall_time_ms": 7, "cpu_time_ms": 3, "peak_memory_mb": 8},
                        "artifacts": [
                            {
                                "name": "report.csv",
                                "size_bytes": 5,
                                "sha256": "sha256:abc",
                                "url": "/v1/executions/run/artifacts/report.csv",
                            }
                        ],
                    }
                ],
            }
        ],
    }
    parsed = ExecutionPipelineResponse.from_dict(
        {"deadline_at": "2026-11-16T01:00:00Z", "steps": [], "completion": manifest}
    )
    assert str(parsed.completion.completion_id) == manifest["completion_id"]
    assert parsed.completion.usage.wall_time_ms == 7
    assert parsed.completion.steps[0].attempts[0].artifacts[0].name == "report.csv"
    assert parsed.to_dict()["completion"]["outcome"] == "cancelled"


def test_pipeline_watch_gap_cursor_and_terminal():
    from types import SimpleNamespace

    from faas_sdk.preview.execution_pipeline_progress import watch_execution_pipeline

    seen = []

    def handler(request):
        after = int(request.url.params["after"])
        seen.append(after)
        return httpx.Response(
            200,
            json={
                "pipeline_id": "01234567-89ab-cdef-0123-456789abcdef",
                "events": [
                    {
                        "sequence": 5 if after == 1 else 6,
                        "type": "pipeline",
                        "status": "running",
                        "created_at": "2026-11-17T00:00:00Z",
                    }
                ],
                "next_sequence": 5 if after == 1 else 6,
                "gap": after == 1,
                "terminal": after != 1,
            },
        )

    gen = AuthenticatedClient(base_url="https://api.example.test", token="key").set_httpx_client(
        httpx.Client(base_url="https://api.example.test", transport=httpx.MockTransport(handler))
    )
    events = list(
        watch_execution_pipeline(
            SimpleNamespace(_gen=gen), "01234567-89ab-cdef-0123-456789abcdef", after=1, poll_interval=0.001
        )
    )
    assert [e["type"] for e in events] == ["gap", "progress", "progress", "terminal"]
    assert seen == [1, 5]
