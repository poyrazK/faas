import asyncio
import json
from pathlib import Path

import httpx
import pytest
from faas_sdk.preview import (
    EventContract,
    EventOutbox,
    EventOutboxError,
    EventOutboxRecoveryOptions,
    EventPublicationOptions,
    EventPublisher,
    PipelineScope,
    with_event_outbox_transaction,
)
from faas_sdk.preview._event_outbox_protocol import EVENT_OUTBOX_SCHEMA, SQL
from faas_sdk.preview.event_outbox_state import (
    EventOutboxRetryPolicy,
    EventOutboxStatus,
    _hash,
    _instant,
    _parse_request,
    _policy_digest,
    _receipt,
    event_outbox_retry_delay,
    prepare_event_publication,
)

F = json.loads((Path(__file__).resolve().parents[2] / "request-contracts/event-outbox.json").read_text())
SCOPE = PipelineScope.from_dict(F["scope"])
CONTRACT = EventContract(F["contract"])
OPTIONS = EventPublicationOptions(**F["options"])


class Pool:
    def __init__(self):
        self.active = False
        self.commits = self.rollbacks = self.posts = 0
        self.lose_commit = self.fail_stage = self.paused = False
        self.next = self.lease = True
        self.digest = bytes.fromhex(F["digest"])
        self.settles = []

    async def connect(self):
        return self

    async def release(self, discard=False):
        pass

    async def execute(self, sql, values):
        return await self.query(sql, values)

    async def query(self, sql, values):
        if sql.startswith("BEGIN"):
            self.active = True
            return []
        if sql.startswith("SELECT set_config"):
            return []
        if sql == "COMMIT":
            self.active = False
            self.commits += 1
            if self.lose_commit:
                raise RuntimeError("private commit")
            return []
        if sql == "ROLLBACK":
            self.active = False
            self.rollbacks += 1
            return []
        if sql == SQL["stage"]:
            if self.fail_stage:
                raise RuntimeError("private payload")
            assert values[5] == F["body"]
            return []
        if sql == SQL["stage_check"]:
            return [dict(request_digest=self.digest, app_slug=SCOPE.app_slug)]
        if sql == SQL["status"]:
            return [F["status"]]
        if sql == SQL["publishing_lock"]:
            return []
        if sql == SQL["paused"]:
            return [dict(paused=self.paused)]
        if sql == SQL["rate"]:
            return []
        if sql == SQL["next"]:
            return (
                [
                    dict(
                        producer_key=OPTIONS.key,
                        app_slug=SCOPE.app_slug,
                        request_body=F["body"],
                        request_digest=self.digest,
                        expired=False,
                        retry_window=86400,
                        attempts="0",
                    )
                ]
                if self.next
                else []
            )
        if sql == SQL["policy"]:
            return []
        if sql == SQL["claim"]:
            self.next = False
            return [dict(attempts="1")]
        if sql == SQL["lease"]:
            return [dict(remaining=29)] if self.lease else []
        if sql == SQL["settle"]:
            assert not self.active
            self.settles.append(values)
            return [F["status"] | dict(state=values[4], accepted_at=values[6], attempts="1", last_code=values[5])]
        if sql == SQL["block"]:
            return [F["status"] | dict(state="blocked", last_code=values[3])]
        if sql == SQL["reconcile_read"]:
            return [F["status"] | dict(live=False, request_body=F["body"], request_digest=self.digest)]
        if sql == SQL["reconcile_settle"]:
            assert values[4] == F["status"]["updated_at"]
            return [F["status"] | dict(state="published", accepted_at=values[8], last_code="publication_confirmed")]
        raise AssertionError("unexpected SQL")


def worker(pool, handler=None, **kwargs):
    async def native(request):
        assert not pool.active and pool.commits == 1
        assert request.content.decode() == F["body"]
        assert request.headers["authorization"] == "Bearer secret"
        pool.posts += 1
        return await handler(request) if handler else httpx.Response(200, json=F["receipt"])

    return EventOutbox(
        pool, scope=SCOPE, token="secret", httpx_args={"transport": httpx.MockTransport(native)}, **kwargs
    )


def test_shared_bytes_policy_jitter_and_exact_timestamp():
    assert prepare_event_publication(CONTRACT, SCOPE, F["input"], OPTIONS) == F["body"]
    policy = EventOutboxRetryPolicy(initial_backoff=0.005, max_backoff=0.3, max_attempts=3)
    assert _policy_digest(policy).hex() == F["policy_digest"]
    assert [
        event_outbox_retry_delay(bytes.fromhex(F["token_hex"]), n, policy, 10) for n in [1, 2, 3, 9, 2147483647]
    ] == F["jitter"]
    assert _instant(OPTIONS.time) - _instant(F["status"]["created_at"]) == 789
    assert "request_body" not in EventOutboxStatus.from_dict(F["status"] | dict(request_body="private")).to_dict()
    assert _receipt(F["receipt"], SCOPE, OPTIONS.key).event_id == F["receipt"]["receipt"]["id"]
    assert _hash(EVENT_OUTBOX_SCHEMA).hex() == F["schema_digest"]
    assert SQL == json.loads(
        (Path(__file__).resolve().parents[2] / "request-contracts/event-outbox-sql.json").read_text()
    )
    assert "gregale_event_outbox_publishing_controls" in EVENT_OUTBOX_SCHEMA
    for bad in [
        json.loads(F["body"]) | dict(future=True),
        json.loads(F["body"]) | dict(expected_app_id="other"),
        json.loads(F["body"]) | dict(environment="invalid\n"),
    ]:
        with pytest.raises(EventOutboxError, match="invalid_request"):
            _parse_request(json.dumps(bad), SCOPE.app_id)


@pytest.mark.asyncio
async def test_stage_lifetime_and_deadline():
    pool = Pool()
    publisher = EventPublisher(CONTRACT, scope=SCOPE)
    saved = []

    async def handler(tx):
        saved.append(tx)
        return await publisher.stage(tx, F["input"], OPTIONS)

    result = await with_event_outbox_transaction(pool, handler, timeout=1)
    assert result.retry_until == F["status"]["retry_until"] and pool.commits == 1 and pool.posts == 0
    with pytest.raises(EventOutboxError, match="invalid_transaction"):
        await publisher.stage(saved[0], F["input"], OPTIONS)


@pytest.mark.asyncio
@pytest.mark.parametrize("mode", ["sql", "conflict", "invalid"])
async def test_caught_stage_error_rolls_back(mode):
    pool = Pool()
    pool.fail_stage = mode == "sql"
    if mode == "conflict":
        pool.digest = bytes(32)
    publisher = EventPublisher(CONTRACT, scope=SCOPE)

    async def handler(tx):
        try:
            await publisher.stage(tx, {"label": "x", "n": 0.5} if mode == "invalid" else F["input"], OPTIONS)
        except EventOutboxError:
            pass

    with pytest.raises(EventOutboxError):
        await with_event_outbox_transaction(pool, handler, timeout=1)
    assert pool.commits == 0 and pool.rollbacks == 1


@pytest.mark.asyncio
async def test_lost_commit_never_dispatches():
    pool = Pool()
    pool.lose_commit = True
    with pytest.raises(EventOutboxError, match="commit_unknown"):
        await worker(pool).recover_next(EventOutboxRecoveryOptions(timeout=1))
    assert pool.posts == 0


@pytest.mark.asyncio
@pytest.mark.parametrize("status", [200, 403, 408, 429, 500])
async def test_one_shot_publication(status):
    pool = Pool()

    async def native(request):
        return httpx.Response(status, json=F["receipt"] if status == 200 else {"detail": "private payload"})

    result = await worker(pool, native).recover_next(EventOutboxRecoveryOptions(timeout=1))
    assert result.outcome == ("published" if status == 200 else "blocked" if status == 403 else "unconfirmed")
    assert pool.posts == 1 and len(pool.settles) == 1 and F["input"]["label"] not in str(result)


@pytest.mark.asyncio
async def test_inner_deadline_settles_uncertainty():
    pool = Pool()

    async def native(request):
        await asyncio.sleep(60)

    result = await worker(pool, native).recover_next(EventOutboxRecoveryOptions(timeout=1, request_timeout=0.01))
    assert result.outcome == "unconfirmed" and len(pool.settles) == 1


@pytest.mark.asyncio
@pytest.mark.parametrize("mode", ["pause", "lease", "tamper", "retention"])
async def test_preflight_fences_dispatch(mode):
    pool = Pool()
    pool.paused = mode == "pause"
    pool.lease = mode != "lease"
    if mode == "tamper":
        pool.digest = bytes(32)
    outbox = worker(pool, receipt_retention=1 if mode == "retention" else 86400)
    if mode == "retention":
        with pytest.raises(EventOutboxError, match="retention_mismatch"):
            await outbox.recover_next(EventOutboxRecoveryOptions(timeout=1))
    else:
        assert (await outbox.recover_next(EventOutboxRecoveryOptions(timeout=1))).outcome == {
            "pause": "paused",
            "lease": "claim_lost",
            "tamper": "blocked",
        }[mode]
    assert pool.posts == 0


@pytest.mark.asyncio
async def test_deadline_and_cancel_fence_late_transactions():
    for cancel in [False, True]:
        pool = Pool()
        started = asyncio.Event()
        saved = []

        async def handler(tx):
            saved.append(tx)
            started.set()
            await asyncio.sleep(60)

        task = asyncio.create_task(with_event_outbox_transaction(pool, handler, timeout=1 if cancel else 0.01))
        await started.wait()
        if cancel:
            task.cancel()
        with pytest.raises(asyncio.CancelledError if cancel else EventOutboxError):
            await task
        assert pool.commits == 0
        with pytest.raises((asyncio.CancelledError, EventOutboxError)):
            await saved[0].execute("late")


@pytest.mark.asyncio
async def test_batch_limit_and_progress_failure():
    pool = Pool()
    progress = []

    async def notify(result):
        progress.append(result.outcome)

    result = await worker(pool).recover(EventOutboxRecoveryOptions(timeout=1), max_events=3, on_progress=notify)
    assert [r.outcome for r in result] == progress == ["published", "empty"]

    async def fail(result):
        raise RuntimeError("private progress")

    with pytest.raises(EventOutboxError, match="progress_failed"):
        await worker(Pool()).recover(EventOutboxRecoveryOptions(timeout=1), max_events=3, on_progress=fail)


@pytest.mark.asyncio
async def test_reconcile_exact_cas_and_read_only_evidence():
    pool = Pool()

    async def native(request):
        assert request.url.path.endswith("/events/verify-publication")
        assert request.content.decode() == F["body"]
        return httpx.Response(
            200,
            json=F["receipt"]
            | dict(event_id=F["receipt"]["receipt"]["id"], status="match", observed_at="2026-11-16T12:01:00Z"),
        )

    outbox = EventOutbox(pool, scope=SCOPE, token="secret", httpx_args={"transport": httpx.MockTransport(native)})
    assert (await outbox.reconcile(OPTIONS.key, timeout=1)).outcome == "published"


@pytest.mark.asyncio
async def test_cleanup_errors_never_expose_driver_diagnostics():
    class BrokenPool(Pool):
        async def release(self, discard=False):
            raise RuntimeError("private payload in driver diagnostics")

    with pytest.raises(EventOutboxError, match="store_failed") as caught:
        await worker(BrokenPool()).status(OPTIONS.key, timeout=1)
    assert "private" not in str(caught.value)
