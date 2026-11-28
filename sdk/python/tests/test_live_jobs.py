import asyncio
import json
from pathlib import Path

import pytest
from faas_sdk.preview import LiveJob, LiveJobError, LiveJobSnapshot, realtime_live_job_source
from faas_sdk.preview.realtime_recovery import RealtimeRecoveryEvent, RealtimeRecoveryTarget
from faas_sdk.preview.realtime_websocket import RealtimeWebSocketMessage

ROOT = Path(__file__).resolve().parents[1] / "request-contracts"
if not ROOT.exists():
    ROOT = Path(__file__).resolve().parents[2] / "request-contracts"
FIXTURE = json.loads((ROOT / "live-jobs.json").read_text())
BASE = FIXTURE["cases"][0]["snapshot"]
COMPLETE = dict(BASE, status="succeeded", succeeded=3, running=0, report="ready")
EVENT = dict(version=1, resource_id="import-1", kind="progress")


@pytest.mark.parametrize("case", FIXTURE["cases"], ids=lambda case: case["name"])
def test_shared_snapshot(case):
    if not case["valid"]:
        with pytest.raises(LiveJobError):
            LiveJobSnapshot.from_dict(case["snapshot"], "import-1")
        return
    snapshot = LiveJobSnapshot.from_dict(case["snapshot"], "import-1")
    wire = snapshot.to_dict()
    assert "secret-discard-me" not in json.dumps(wire)
    assert "discard" not in json.dumps(wire["failures"])
    assert LiveJobSnapshot.from_dict(wire, "import-1") == snapshot


class Source:
    def __init__(self):
        self.sink = None
        self.starts = self.stops = 0

    async def __call__(self, sink):
        self.sink = sink
        self.starts += 1
        sink.ready()
        try:
            await asyncio.Event().wait()
        finally:
            self.stops += 1


@pytest.mark.asyncio
async def test_in_flight_intent_can_become_accepted_without_resubmitting():
    source, value = Source(), dict(BASE, run_id=None, status="submission_unresolved", total=0, succeeded=0, running=0)

    async def load():
        return value

    iterator = LiveJob(resource_id="import-1", load_snapshot=load, source=source, timeout=2).updates()
    assert (await anext(iterator)).status == "submission_unresolved"
    value = COMPLETE
    source.sink.changed(EVENT)
    assert (await anext(iterator)).report == "ready"
    with pytest.raises(StopAsyncIteration):
        await anext(iterator)
    assert source.stops == 1


@pytest.mark.asyncio
async def test_duplicate_out_of_order_hints_do_not_complete_before_authoritative_report():
    source, reads, value = Source(), 0, BASE

    async def load():
        nonlocal reads
        reads += 1
        return value

    live = LiveJob(resource_id="import-1", load_snapshot=load, source=source, timeout=2)
    iterator = live.updates()
    assert source.starts == 0
    assert (await anext(iterator)).status == "running"
    for _ in range(500):
        source.sink.changed(dict(EVENT, kind="report_ready", status="succeeded", report="ready"))
    assert (await anext(iterator)).status == "running"
    assert reads == 2
    source.sink.changed(dict(EVENT, resource_id="other"))
    assert reads == 2
    value = dict(COMPLETE, report="pending")
    source.sink.changed(EVENT)
    assert not (await anext(iterator)).complete
    source.sink.disconnected()
    value = COMPLETE
    source.sink.ready()
    assert (await anext(iterator)).complete
    with pytest.raises(StopAsyncIteration):
        await anext(iterator)
    assert source.stops == 1


@pytest.mark.asyncio
async def test_deadline_fences_cancellation_ignoring_loader():
    source, release = Source(), asyncio.Event()

    async def load():
        try:
            await release.wait()
        except asyncio.CancelledError:
            await release.wait()
        return COMPLETE

    iterator = LiveJob(resource_id="import-1", load_snapshot=load, source=source, timeout=0.02).updates()
    try:
        with pytest.raises(LiveJobError, match="deadline_exceeded"):
            await anext(iterator)
    finally:
        release.set()
        await asyncio.sleep(0)
    assert source.stops == 1


@pytest.mark.asyncio
async def test_revocation_interrupts_a_pending_snapshot_without_waiting_for_deadline():
    source, started, release = Source(), asyncio.Event(), asyncio.Event()

    async def load():
        started.set()
        try:
            await release.wait()
        except asyncio.CancelledError:
            await release.wait()
        return COMPLETE

    iterator = LiveJob(resource_id="import-1", load_snapshot=load, source=source, timeout=10).updates()
    pending = asyncio.create_task(anext(iterator))
    await started.wait()
    source.sink.failed()
    try:
        with pytest.raises(LiveJobError, match="subscription_failed"):
            await asyncio.wait_for(pending, 0.5)
    finally:
        release.set()
        await asyncio.sleep(0)
    assert source.stops == 1


@pytest.mark.asyncio
@pytest.mark.parametrize("mode", ["cancel", "limit", "close", "denied", "wrong_run"])
async def test_limits_cancellation_authorization_and_cleanup(mode):
    source, value = Source(), BASE

    async def load():
        return value

    iterator = LiveJob(
        resource_id="import-1",
        load_snapshot=load,
        source=source,
        timeout=2,
        max_snapshots=1 if mode == "limit" else 100,
    ).updates()
    assert (await anext(iterator)).status == "running"
    if mode == "close":
        await iterator.aclose()
    elif mode == "cancel":
        pending = asyncio.create_task(anext(iterator))
        await asyncio.sleep(0)
        pending.cancel()
        with pytest.raises(asyncio.CancelledError):
            await pending
    else:
        if mode == "denied":
            source.sink.failed()
        else:
            if mode == "wrong_run":
                value = dict(BASE, run_id="another-run")
            source.sink.changed(EVENT)
        with pytest.raises(LiveJobError):
            await anext(iterator)
    assert source.stops == 1


@pytest.mark.asyncio
async def test_native_recovery_resnapshots_history_gap_and_reopening_completed_import():
    sessions, reads = [], 0

    class Session:
        async def subscribe(self, **_):
            return RealtimeRecoveryTarget(oldest_sequence=10, latest_sequence=12, resync_required=True)

        async def replay(self, **_):
            if False:
                yield None

        async def events(self):
            yield RealtimeRecoveryEvent(
                sequence=13,
                data=RealtimeWebSocketMessage(
                    message_id="m13",
                    data=json.dumps(EVENT).encode(),
                    binary=False,
                    version=1,
                    event="created",
                    deleted=False,
                ),
            )
            await asyncio.Event().wait()

        async def aclose(self):
            self.closed = True

    async def connect():
        session = Session()
        sessions.append(session)
        return session

    async def load():
        nonlocal reads
        reads += 1
        return COMPLETE

    iterator = LiveJob(
        resource_id="import-1",
        load_snapshot=load,
        source=realtime_live_job_source(connect=connect, channel="import:import-1"),
        timeout=2,
    ).updates()
    assert (await anext(iterator)).report == "ready"
    with pytest.raises(StopAsyncIteration):
        await anext(iterator)
    await asyncio.sleep(0)
    assert reads == 1
    assert sessions[0].closed
