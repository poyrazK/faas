import json
from datetime import UTC, datetime
from urllib.error import URLError

import pytest

from faas_sdk.issues import IssueReporter


def test_context_is_bounded_and_copied_before_retry():
    requests = []

    class Response:
        status = 202

        def __enter__(self):
            return self

        def __exit__(self, *args):
            return False

    class Opener:
        def open(self, request, **kwargs):
            requests.append(json.loads(request.data))
            if len(requests) == 1:
                raise URLError("transient")
            return Response()

    reporter = IssueReporter(
        "http://example.test",
        "payments",
        "g_issue_test",
        grouping_strategy="failure-site-v1",
        symbolication_strategy="deployment-source-maps-v1",
    )
    reporter._opener = Opener()
    entries = [
        {
            "occurred_at": datetime.now(UTC),
            "category": "workflow",
            "message": "€" * 100,
            "data": {"private": "must not be copied"},
        }
        for _ in range(20)
    ]
    tags = {"component": "payments", "email": "private@example.com"}
    try:
        assert reporter.capture_exception(ValueError("failed"), breadcrumbs=entries, breadcrumbs_dropped=2, tags=tags)
        entries[-1]["message"] = "later"
        tags["component"] = "later"
        assert reporter.flush(timeout=10)
        assert len(requests) == 2 and requests[0] == requests[1]
        event = requests[0]
        assert len(event["breadcrumbs"]) == 16 and event["breadcrumbs_dropped"] == 6
        assert len(event["breadcrumbs"][-1]["message"].encode()) <= 256
        assert "data" not in event["breadcrumbs"][-1]
        assert event["tags"] == {"component": "payments"}
        assert event["symbolication_strategy"] == "deployment-source-maps-v1"
    finally:
        reporter.close()


def test_server_symbolication_requires_explicit_grouping_before_startup():
    with pytest.raises(ValueError):
        IssueReporter(
            "http://example.test", "payments", "g_issue_test", symbolication_strategy="deployment-source-maps-v1"
        )


def test_generated_context_and_artifact_contracts():
    from faas_sdk.api.apps.list_issues import _get_kwargs
    from faas_sdk.models.issue_event import IssueEvent
    from faas_sdk.models.issue_occurrence import IssueOccurrence
    from faas_sdk.models.publish_issue_source_maps_request import PublishIssueSourceMapsRequest

    event = {
        "event_id": "11111111-1111-4111-8111-111111111111",
        "occurred_at": "2026-10-13T12:00:00Z",
        "exception_type": "Error",
        "message": "failed",
        "tags": {"component": "payments"},
        "breadcrumbs": [
            {"occurred_at": "2026-10-13T11:59:59Z", "category": "workflow", "level": "info", "message": "charge"}
        ],
        "breadcrumbs_dropped": 2,
        "symbolication_strategy": "deployment-source-maps-v1",
        "symbolication": {"manifest_sha256": "a" * 64, "mapped_frames": 1, "unmapped_frames": 0},
    }
    model = IssueEvent.from_dict(event)
    assert model.to_dict()["tags"] == event["tags"]
    assert model.breadcrumbs_dropped == 2 and model.symbolication.mapped_frames == 1
    occurrence = IssueOccurrence.from_dict(
        {
            **event,
            "deployment_id": "22222222-2222-4222-8222-222222222222",
            "received_at": event["occurred_at"],
            "attribution": "unattributed",
        }
    )
    assert occurrence.to_dict()["breadcrumbs_dropped"] == 2
    assert occurrence.symbolication.manifest_sha256 == "a" * 64
    assert _get_kwargs("payments", tag="component=payments")["params"]["tag"] == "component=payments"
    request = {
        "directory": "/app/dist",
        "manifest": {
            "version": 1,
            "artifacts": [
                {
                    "generatedFile": "bundle.js",
                    "sha256": "a" * 64,
                    "payload": '{"version":3,"sources":["src.ts"],"names":["run"],"mappings":"AAAAA"}',
                }
            ],
        },
    }
    assert PublishIssueSourceMapsRequest.from_dict(request).to_dict() == request
