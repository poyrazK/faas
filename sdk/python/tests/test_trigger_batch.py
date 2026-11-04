import asyncio
import base64
import json

import pytest

from faas_sdk.preview.trigger_batch import (
    TriggerBatchError,
    handle_trigger_batch,
    handle_trigger_batch_async,
    is_trigger_batch,
    parse_trigger_batch,
)

BODY = {
    "records": [
        {
            "item_identifier": "a",
            "data": {"n": 1},
            "headers": {"content-type": "application/json"},
            "metadata": {"sequence": "41"},
        },
        {"item_identifier": "b", "data_base64": base64.b64encode(b"raw bytes").decode()},
        {"item_identifier": "c", "data": None},
    ]
}


def test_parses_json_and_binary_records_in_order():
    records = parse_trigger_batch(json.dumps(BODY).encode())
    assert [r.item_identifier for r in records] == ["a", "b", "c"]
    assert records[0].is_json and records[0].data == {"n": 1}
    assert records[0].text() == '{"n":1}'
    assert records[0].headers["content-type"] == "application/json"
    assert records[0].metadata["sequence"] == "41"
    assert not records[1].is_json and records[1].data is None
    assert records[1].payload == b"raw bytes"
    assert records[2].is_json and records[2].data is None, "a JSON null payload is still JSON"
    assert len(parse_trigger_batch(json.dumps(BODY))) == 3


@pytest.mark.parametrize(
    "bad",
    [
        "not json",
        {"records": "x"},
        {"records": [{}]},
        {"records": [{"item_identifier": "a", "data": 1, "data_base64": "MQ=="}]},
        {"records": [{"item_identifier": "a", "data_base64": "%%%"}]},
        {"records": [{"item_identifier": "a", "data": 1}, {"item_identifier": "a", "data": 2}]},
    ],
)
def test_rejects_malformed_envelopes(bad):
    with pytest.raises(TriggerBatchError):
        parse_trigger_batch(bad)


def test_detects_a_batch_delivery_from_its_header():
    assert is_trigger_batch({"X-Gregale-Batch-Size": "3"})
    assert is_trigger_batch({"x-gregale-batch-size": "3"})
    assert not is_trigger_batch({"content-type": "application/json"})


def test_reports_only_failed_records():
    seen, errors = [], []

    def handler(record):
        seen.append(record.item_identifier)
        if record.item_identifier == "b":
            raise RuntimeError("boom")

    response = handle_trigger_batch(BODY, handler, on_error=lambda _e, r: errors.append(r.item_identifier))
    assert seen == ["a", "b", "c"]
    assert errors == ["b"]
    assert response == {"batchItemFailures": [{"itemIdentifier": "b"}]}
    assert handle_trigger_batch(BODY, lambda _r: None) == {"batchItemFailures": []}


def test_stop_on_failure_retries_the_rest_in_order():
    seen = []

    def handler(record):
        seen.append(record.item_identifier)
        if record.item_identifier == "b":
            raise RuntimeError("boom")

    response = handle_trigger_batch(BODY, handler, stop_on_failure=True)
    assert seen == ["a", "b"]
    assert [f["itemIdentifier"] for f in response["batchItemFailures"]] == ["b", "c"]


def test_async_runs_records_concurrently():
    running = peak = 0

    async def handler(record):
        nonlocal running, peak
        running += 1
        peak = max(peak, running)
        await asyncio.sleep(0.01)
        running -= 1
        if record.item_identifier == "c":
            raise RuntimeError("boom")

    response = asyncio.run(handle_trigger_batch_async(BODY, handler, concurrency=3))
    assert peak == 3
    assert response == {"batchItemFailures": [{"itemIdentifier": "c"}]}
    sequential = asyncio.run(handle_trigger_batch_async(BODY, lambda _r: None))
    assert sequential == {"batchItemFailures": []}
    with pytest.raises(TypeError):
        asyncio.run(handle_trigger_batch_async(BODY, lambda _r: None, concurrency=2, stop_on_failure=True))
    with pytest.raises(TypeError):
        asyncio.run(handle_trigger_batch_async(BODY, lambda _r: None, concurrency=0))
