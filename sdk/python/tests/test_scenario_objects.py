from faas_sdk.preview.models.create_scenario_objects_request import CreateScenarioObjectsRequest
from faas_sdk.preview.models.scenario_objects_evidence import ScenarioObjectsEvidence


def test_object_fixture_bytes_and_duplicate_receipts_round_trip():
    spec = {"version": 1, "bucket": "imports", "fixture_sha256": "a" * 64}
    request = {
        "app_slug": "isolated-api",
        "spec": spec,
        "objects": [
            {
                "key": "exports/é.bin",
                "body": "AP8K",
                "content_type": "application/octet-stream",
                "metadata": {"owner": "one"},
            }
        ],
    }
    assert CreateScenarioObjectsRequest.from_dict(request).to_dict() == request
    obj = {
        "key": "exports/é.bin",
        "size": 3,
        "sha256": "e" * 64,
        "content_type": "application/octet-stream",
        "metadata": {"owner": "one"},
    }
    evidence = {
        "version": 1,
        "collector_id": "44444444-4444-4444-8444-444444444444",
        "collector_closed": True,
        "run_id": "b" * 32,
        "app_id": "11111111-1111-4111-8111-111111111111",
        "resource_id": "22222222-2222-4222-8222-222222222222",
        "deployment_id": "33333333-3333-4333-8333-333333333333",
        "fixture_sha256": "a" * 64,
        "contract_sha256": "c" * 64,
        "initial_sha256": "d" * 64,
        "status": "complete",
        "provenance": "platform_object_collector",
        "spec": spec,
        "before": [],
        "after": [obj],
        "mutations": [
            {"sequence": n, "method": "PUT", "key": obj["key"], "status": 200, "object": obj} for n in [1, 2]
        ],
        "cleanup_confirmed": True,
    }
    assert ScenarioObjectsEvidence.from_dict(evidence).to_dict() == evidence
