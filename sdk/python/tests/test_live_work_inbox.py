import asyncio
import json
from contextlib import aclosing
from copy import deepcopy
from pathlib import Path

import pytest
from faas_sdk.preview import (
    LiveWorkInboxError,
    parse_live_work_inbox_snapshot,
    read_work_inbox_snapshot,
    watch_work_inbox,
    work_inbox_invalidation,
)

CASES = json.loads((Path(__file__).resolve().parents[2] / "request-contracts/live-work-inbox-cases.json").read_text())


def hint(change_id, target=CASES["alice"]):
    return work_inbox_invalidation(target["resource_id"], change_id)


class Source:
    def __init__(self):
        self.started, self.stopped = asyncio.Event(), asyncio.Event()
        self.sink = None

    async def __call__(self, sink):
        self.sink = sink
        self.started.set()
        try:
            await asyncio.Future()
        finally:
            self.stopped.set()


def watch(source, load, **options):
    settings = {"target": CASES["alice"], "timeout": 2, "refresh_interval": 10} | options
    return watch_work_inbox(source=source, load_snapshot=load, **settings)


@pytest.mark.parametrize("key", ("initial", "pending_review", "refund_unknown", "refund_succeeded"))
def test_shared_snapshot(key):
    assert parse_live_work_inbox_snapshot(CASES[key], CASES["alice"]) == CASES[key]


def test_snapshot_rejects_wrong_scope_duplicates_and_unsafe_links():
    altered = deepcopy(CASES["pending_review"])
    altered["items"][0]["review"]["review_url"] += "&token=secret"
    duplicate = CASES["refund_unknown"] | {"items": CASES["refund_unknown"]["items"] * 2}
    for value, target in ((altered, CASES["alice"]), (duplicate, CASES["alice"]), (CASES["initial"], CASES["bob"])):
        with pytest.raises(LiveWorkInboxError, match="invalid_snapshot"):
            parse_live_work_inbox_snapshot(value, target)


async def test_lazy_authorized_reads_coalesce_repeated_hints_and_isolate_two_viewers():
    alice, bob = Source(), Source()
    alice_value, reads, bob_reads, authorized = CASES["initial"], 0, 0, True
    same_state_read = asyncio.Event()

    async def alice_load(_remaining):
        nonlocal reads
        reads += 1
        if reads == 3:
            same_state_read.set()
        if not authorized:
            raise LiveWorkInboxError("unauthorized")
        return alice_value

    async def bob_load(_remaining):
        nonlocal bob_reads
        bob_reads += 1
        return CASES["bob_empty"]

    async with (
        aclosing(watch(alice, alice_load)) as mine,
        aclosing(watch(bob, bob_load, target=CASES["bob"])) as other,
        asyncio.timeout(2),
    ):
        assert alice.sink is None
        initial = asyncio.create_task(anext(mine))
        foreign = asyncio.create_task(anext(other))
        await asyncio.gather(alice.started.wait(), bob.started.wait())
        assert reads == 0
        alice.sink.ready()
        bob.sink.ready()
        assert await initial == CASES["initial"]
        assert await foreign == CASES["bob_empty"]
        review = asyncio.create_task(anext(mine))
        alice_value = CASES["pending_review"]
        alice.sink.changed(hint("other", CASES["bob"]))
        alice.sink.changed(hint("malformed") | {"items": alice_value["items"]})
        bob.sink.changed(hint("review"))
        await asyncio.sleep(0)
        assert reads == bob_reads == 1
        alice.sink.changed(hint("review"))
        assert await review == CASES["pending_review"]
        revoked = asyncio.create_task(anext(mine))
        alice.sink.changed(hint("review"))
        await asyncio.sleep(0)
        assert reads == 2
        alice.sink.changed(hint("same-state"))
        await same_state_read.wait()
        authorized = False
        alice.sink.changed(hint("revoked"))
        with pytest.raises(LiveWorkInboxError, match="unauthorized"):
            await revoked
        await alice.stopped.wait()
        assert not bob.stopped.is_set()
        alice.sink.ready()
        with pytest.raises(StopAsyncIteration):
            await anext(mine)


async def test_reconnect_cancels_stale_reader_and_refreshes_without_a_notification():
    started, interrupted, release = asyncio.Event(), asyncio.Event(), asyncio.Event()
    source = Source()
    reads = 0

    async def load(_remaining):
        nonlocal reads
        reads += 1
        if reads == 1:
            return CASES["initial"]
        if reads == 2:
            started.set()
            try:
                await asyncio.Future()
            except asyncio.CancelledError:
                interrupted.set()
                await release.wait()
                return CASES["refund_unknown"]
        return CASES["refund_succeeded"]

    async with aclosing(watch(source, load)) as updates, asyncio.timeout(2):
        first = asyncio.create_task(anext(updates))
        await source.started.wait()
        source.sink.ready()
        assert await first == CASES["initial"]
        reconnect = asyncio.create_task(anext(updates))
        source.sink.changed(hint("first"))
        await started.wait()
        source.sink.disconnected()
        await interrupted.wait()
        source.sink.ready()
        try:
            assert await reconnect == CASES["refund_succeeded"]
        finally:
            release.set()
        pending = asyncio.create_task(anext(updates))
        await asyncio.sleep(0)
        pending.cancel()
        with pytest.raises(asyncio.CancelledError):
            await pending
        await source.stopped.wait()


async def test_fallback_refresh_snapshot_budget_and_deadline_are_bounded():
    source = Source()
    values = iter((CASES["initial"], CASES["refund_unknown"]))

    async def load(_remaining):
        return next(values)

    async with aclosing(watch(source, load, max_snapshots=2, refresh_interval=0.01)) as updates:
        initial = asyncio.create_task(anext(updates))
        await source.started.wait()
        source.sink.ready()
        assert await initial == CASES["initial"]
        assert await anext(updates) == CASES["refund_unknown"]
        with pytest.raises(LiveWorkInboxError, match="snapshot_limit"):
            await anext(updates)
        await source.stopped.wait()

    async def not_called(_remaining):
        pytest.fail("Read before ready")

    idle = Source()
    async with aclosing(watch(idle, not_called, timeout=0.02)) as updates:
        with pytest.raises(LiveWorkInboxError, match="deadline"):
            await anext(updates)
        await idle.stopped.wait()

    denied = Source()
    async with aclosing(watch(denied, not_called)) as updates:
        rejected = asyncio.create_task(anext(updates))
        await denied.started.wait()
        denied.sink.failed("unauthorized")
        with pytest.raises(LiveWorkInboxError, match="unauthorized"):
            await rejected


async def test_snapshot_reader_follows_sparse_pages_and_marks_truncation():
    class Inbox:
        async def pages(self, filters, access, *, max_pages, **_options):
            assert filters == {"business_ref": CASES["alice"]["business_ref"]}
            assert access == "alice"
            yield {"items": [], "next_cursor": "next"}
            if max_pages != 1:
                yield {"items": CASES["pending_review"]["items"], "next_cursor": None}

    inbox = Inbox()
    assert await read_work_inbox_snapshot(inbox, CASES["alice"], "alice", timeout=1) == CASES["pending_review"]
    assert await read_work_inbox_snapshot(inbox, CASES["alice"], "alice", timeout=1, max_pages=1) == (
        CASES["initial"] | {"truncated": True}
    )
