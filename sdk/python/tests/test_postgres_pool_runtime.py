"""Driver-independent pool adoption and transaction lease contracts."""

import copy
import json
import threading
import traceback
from concurrent.futures import ThreadPoolExecutor

import pytest
from faas_sdk.postgres_pool_runtime import PostgresPoolBinding, PostgresPoolBindingError
from faas_sdk.secret_reload_runtime import SecretConsumer, SecretReloadCoordinator, SecretReloadError, SecretReloadIO


def uri(password="private-first"):
    return f"postgresql://worker:{password}@db.example:6432/app?sslmode=require"


def values(password="private-first"):
    return {"DATABASE_URL": uri(password)}


class Pool:
    def __init__(self, dsn):
        self.dsn, self.closed = dsn, 0

    def close(self):
        self.closed += 1


def binding(validate=lambda _pool, _cancel: None):
    return PostgresPoolBinding(
        "DATABASE_URL",
        lambda config, _cancel: Pool(config.connection_string),
        validate=validate,
        retire=lambda pool: pool.close(),
    )


def test_validation_gates_publication_and_old_transactions_finish():
    entered, finish = threading.Event(), threading.Event()

    def validate(pool, _cancel):
        if "private-second" in pool.dsn:
            entered.set()
            assert finish.wait(3)

    b = binding(validate)
    b.reload(values())
    first, second = b.acquire(), b.acquire()
    old = first.current()
    with ThreadPoolExecutor() as worker:
        reload = worker.submit(b.reload, values("private-second"))
        try:
            assert entered.wait(3)
            with pytest.raises(PostgresPoolBindingError, match="not ready"):
                b.acquire()
            assert first.current() is old and old.closed == 0
        finally:
            finish.set()
        reload.result(3)
    with b.acquire() as next_lease:
        current = next_lease.current()
        assert current is not old
        copied = copy.copy(first)
        first.release()
        copied.release()
        with pytest.raises(PostgresPoolBindingError, match="released"):
            copied.current()
        assert old.closed == 0
        b.invalidate()
        with pytest.raises(PostgresPoolBindingError):
            b.acquire()
        with pytest.raises(PostgresPoolBindingError, match="timed out"):
            b.wait_for_retirement(0)
        assert current.closed == 0
        second.release()
    b.wait_for_retirement(3)
    assert old.closed == current.closed == 1


def test_validation_failure_reclaims_candidate_hides_errors_and_recovers():
    created, configs = [], []

    def factory(config, _cancel):
        configs.append(config)
        pool = Pool(config.connection_string)
        created.append(pool)
        return pool

    def validate(pool, _cancel):
        if "private-bad" in pool.dsn:
            raise RuntimeError(pool.dsn)

    b = PostgresPoolBinding("DATABASE_URL", factory, validate=validate, retire=lambda pool: pool.close())
    b.reload(values())
    with b.acquire() as lease:
        bad_snapshot = values("private-bad")
        with pytest.raises(PostgresPoolBindingError) as error:
            b.reload(bad_snapshot)
        assert "private-" not in "".join(traceback.format_exception(error.value))
        assert "private-" not in repr(configs) and "private-" not in repr(lease)
        with pytest.raises(PostgresPoolBindingError):
            b.acquire()
        assert created[0].closed == 0
    b.wait_for_retirement(3)
    assert created[0].closed == created[1].closed == 1
    b.reload(values("private-recovered"))
    with b.acquire() as lease:
        assert lease.current().dsn == uri("private-recovered")
        b.invalidate()
    b.wait_for_retirement(3)


@pytest.mark.parametrize("mode", ["invalidate", "cancel"])
def test_late_validation_never_publishes_and_cannot_block_invalidation(mode):
    entered, finish, cancel = threading.Event(), threading.Event(), threading.Event()
    pool = Pool(uri())

    def validate(_pool, _cancel):
        entered.set()
        assert finish.wait(3)

    b = PostgresPoolBinding(
        "DATABASE_URL", lambda _config, _cancel: pool, validate=validate, retire=lambda p: p.close()
    )
    with ThreadPoolExecutor() as worker:
        reload = worker.submit(b.reload, values(), cancel)
        try:
            assert entered.wait(3)
            if mode == "invalidate":
                b.invalidate()
            else:
                cancel.set()
            with pytest.raises(PostgresPoolBindingError):
                b.acquire()
            with pytest.raises(PostgresPoolBindingError, match="timed out"):
                b.wait_for_retirement(0)
        finally:
            finish.set()
        with pytest.raises(PostgresPoolBindingError, match="cancelled"):
            reload.result(3)
    b.wait_for_retirement(3)
    assert pool.closed == 1


def test_alias_rejected_before_validation_and_does_not_close_old_transaction():
    pool, checks = Pool(uri()), []
    b = PostgresPoolBinding(
        "DATABASE_URL",
        lambda _config, _cancel: pool,
        validate=lambda _pool, _cancel: checks.append(1),
        retire=lambda p: p.close(),
    )
    b.reload(values())
    with b.acquire() as lease:
        with pytest.raises(PostgresPoolBindingError, match="reused an owned pool"):
            b.reload(values())
        assert checks == [1] and pool.closed == 0 and lease.current() is pool
    b.wait_for_retirement(3)
    assert pool.closed == 1


def test_uri_validation_prevents_ambient_identity_and_never_invokes_factory():
    invalid = [
        "\v" + uri(),
        "postgres://worker:%1f@db:5432/app",
        "postgres://worker:secret@db/app",
        "postgres://worker:secret@%2Ftmp:5432/app",
        uri() + "&database=other",
        "",
        "host=db user=worker",
        "https://worker:secret@db/app",
        "postgres://db/app",
        "postgres://worker@db/app",
        "postgres://worker:secret@db",
        "postgres://worker:secret@db/",
        "postgres://worker:secret@db:0/app",
        "postgres://worker:secret@db:65536/app",
        uri() + "#fragment",
        uri() + "#",
        uri() + "&UsEr=other",
        uri() + "&host=other",
        "postgres://worker:%00@db/app",
        "postgres://worker:%ZZ@db/app",
        uri() + "\n",
    ]
    calls = []

    def factory(config, _cancel):
        calls.append(config)
        return Pool(config.connection_string)

    b = PostgresPoolBinding(
        "DATABASE_URL", factory, validate=lambda _pool, _cancel: None, retire=lambda pool: pool.close()
    )
    for value in invalid:
        with pytest.raises(PostgresPoolBindingError):
            b.reload({"DATABASE_URL": value})
        with pytest.raises(PostgresPoolBindingError):
            b.acquire()
    assert calls == []
    for value in [uri(), "postgres://work%20er:p%40ss@[::1]:5432/app%2Fdb?sslmode=disable"]:
        b.reload({"DATABASE_URL": value})
        with b.acquire() as lease:
            assert lease.current().dsn == value
        b.invalidate()
        b.wait_for_retirement(3)
    with pytest.raises(PostgresPoolBindingError):
        PostgresPoolBinding("invalid-key", factory, validate=lambda _p, _c: None, retire=lambda _p: None)


def test_async_hooks_and_false_validation_are_rejected_and_owned_candidate_reclaimed():
    async def async_factory(_config, _cancel):
        return Pool(uri())

    b = PostgresPoolBinding("DATABASE_URL", async_factory, validate=lambda _p, _c: None, retire=lambda p: p.close())
    with pytest.raises(PostgresPoolBindingError):
        b.reload(values())

    async def async_validate(_pool, _cancel):
        pass

    for validate in (async_validate, lambda _pool, _cancel: False):
        pool = Pool(uri())
        b = PostgresPoolBinding(
            "DATABASE_URL", lambda _config, _cancel: pool, validate=validate, retire=lambda p: p.close()
        )
        with pytest.raises(PostgresPoolBindingError, match="validation failed"):
            b.reload(values())
        b.wait_for_retirement(3)
        assert pool.closed == 1


def test_retirement_failures_are_sticky_sanitized_and_not_retried():
    calls, observed = [], threading.Event()

    def retire(_pool):
        calls.append(1)
        raise RuntimeError("private-cleanup")

    def observer(error):
        assert isinstance(error, PostgresPoolBindingError) and "private-" not in str(error)
        observed.set()
        raise RuntimeError("private-observer")

    b = PostgresPoolBinding(
        "DATABASE_URL",
        lambda config, _cancel: Pool(config.connection_string),
        validate=lambda _pool, _cancel: None,
        retire=retire,
        on_retire_error=observer,
    )
    b.reload(values())
    b.invalidate()
    for _ in range(2):
        with pytest.raises(PostgresPoolBindingError, match="pool retirement failed"):
            b.wait_for_retirement(3)
    assert observed.wait(3) and calls == [1]


def test_coordinator_ack_waits_for_validation_and_later_consumer_failure_invalidates(tmp_path):
    entered, finish = threading.Event(), threading.Event()
    receipts, checks = [], []
    fail = False

    def validate(_pool, _cancel):
        checks.append(1)
        if len(checks) == 1:
            entered.set()
            assert finish.wait(3)

    def later_consumer(_snapshot, _cancel):
        if fail:
            raise RuntimeError("private-other-consumer")

    b = binding(validate)
    raw = json.dumps({"revision": "a" * 64, "secrets": values()})

    def post(_endpoint, body, _cancel):
        receipts.append(json.loads(body)["status"])
        return 202

    coordinator = SecretReloadCoordinator(
        consumers=[b.secret_consumer(), SecretConsumer(later_consumer, lambda: None)],
        env={
            "FAAS_SECRETS_SNAPSHOT_FILE": str(tmp_path / "snapshot"),
            "FAAS_SECRETS_RELOAD_READY_FILE": str(tmp_path / "ready"),
            "FAAS_SECRETS_RELOAD_ACK_ENDPOINT": "http://127.0.0.1:1/ack",
            "FAAS_SECRETS_RELOAD_GENERATION": "e" * 32,
        },
        io=SecretReloadIO(read=lambda _path, _cancel: raw, ready=lambda _path, _cancel: None, post=post),
    )

    # start() installs the signal handler on the main thread. A helper inspects
    # publication while its validation callback is blocked in a worker.
    def inspect_pending():
        try:
            assert entered.wait(3) and receipts == []
            with pytest.raises(PostgresPoolBindingError):
                b.acquire()
        finally:
            finish.set()

    try:
        with ThreadPoolExecutor() as worker:
            pending = worker.submit(inspect_pending)
            coordinator.start()
            pending.result(3)
        assert receipts == ["applied"]
        with b.acquire() as old:
            pool = old.current()
            fail = True
            raw = json.dumps({"revision": "b" * 64, "secrets": values("private-second")})
            with pytest.raises(SecretReloadError):
                coordinator.reload()
            with pytest.raises(PostgresPoolBindingError):
                b.acquire()
            assert pool.closed == 0 and receipts == ["applied", "failed"]
        b.wait_for_retirement(3)
        assert pool.closed == 1
    finally:
        finish.set()
        coordinator.stop()
