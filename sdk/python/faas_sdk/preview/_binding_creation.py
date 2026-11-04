"""Create reviewed receipts with bounded recovery, or inspect an identity explicitly."""

import asyncio
import copy
import time
import re
from urllib.parse import quote
from uuid import UUID

from ._binding_runner import (
    _date,
    _response,
    _uuid,
    _validate,
    _inspect,
    _members,
    _status_retryable,
    _status_retry_count,
    _status_retry_delay,
    BindingVerificationProgress,
)
from attrs import fields, has
from .models.binding_verification_run import BindingVerificationRun
from .models.project_binding_verification_run import ProjectBindingVerificationRun


def _clear_unknown(value):
    if has(type(value)):
        attributes = fields(type(value))
        extra = getattr(value, "additional_properties", None)
        if extra is not None:
            if len(attributes) == 1:
                for item in extra.values():
                    _clear_unknown(item)
                return
            extra.clear()
        for field in attributes:
            if field.name != "additional_properties":
                _clear_unknown(getattr(value, field.name))
    elif isinstance(value, dict):
        for item in value.values():
            _clear_unknown(item)
    elif isinstance(value, list):
        for item in value:
            _clear_unknown(item)


class BindingVerificationCreationError(Exception):
    def __init__(self, run_id, cause, attempted=False):
        super().__init__(f"verification creation {run_id} failed; inspect this run ID: {cause}")
        self.run_id, self.cause, self.attempted = run_id, cause, attempted


def _review(r, app, o):
    import time

    if (
        r["app"] != app
        or not app
        or not r["scope"]
        or not _uuid(r["deployment_id"], True)
        or not r["ready"]
        or r["blockers"]
        or not re.fullmatch(r"[a-fA-F0-9]{64}", r["review_digest"])
        or _date(r["reviewed_at"]) > time.time()
        or len(r["plan"]) > 128
        or r["object_storage_write_required"]
        and not o["object_storage_write"]
        or r["queue_publish_required"]
        and not o["queue_publish"]
        or r["policy"]["scope"] != r["scope"]
        or r["inventory"].get("verification_deployment_id") != r["deployment_id"]
        or r["inventory"].get("service_targets", {}) != r["service_targets"]
    ):
        raise ValueError("review selection, readiness or explicit consent was not confirmed")


def _prepare(review, identity, run_id, ttl_seconds, max_concurrency, object_storage_write, queue_publish):
    import time

    if (
        not _uuid(run_id)
        or type(ttl_seconds) is not int
        or not 1 <= ttl_seconds <= 600
        or type(max_concurrency) is not int
        or not 1 <= max_concurrency <= 8
        or type(object_storage_write) is not bool
        or type(queue_publish) is not bool
    ):
        raise ValueError("invalid run UUID, lifetime, concurrency or consent")
    model = type(review).from_dict(review.to_dict())
    _clear_unknown(model)
    r = model.to_dict()
    o = dict(
        id=run_id,
        ttl_seconds=ttl_seconds,
        max_concurrency=max_concurrency,
        object_storage_write=object_storage_write,
        queue_publish=queue_publish,
    )
    if len(identity) == 1:
        _review(r, identity[0], o)
        o.update(
            deployment_id=r["deployment_id"], service_targets=r["service_targets"], review_digest=r["review_digest"]
        )
        path = f"/v1/apps/{quote(identity[0], safe='')}/bindings/verification-runs"
    else:
        p, env = identity
        names = set(r["members"])
        if (
            r["project"] != p
            or r["environment"] != env
            or not p
            or not env
            or r["manifest"]["project"] != p
            or r["manifest"]["environment"] != env
            or not r["ready"]
            or r["blockers"]
            or not re.fullmatch(r"[a-fA-F0-9]{64}", r["review_digest"])
            or _date(r["reviewed_at"]) > time.time()
            or not 1 <= len(names) <= 100
            or names != set(r["manifest"]["deployments"])
            or names != set(r["review_digests"])
            or r["object_storage_write"] != object_storage_write
            or r["queue_publish"] != queue_publish
            or r["object_storage_write_required"]
            and not object_storage_write
            or r["queue_publish_required"]
            and not queue_publish
        ):
            raise ValueError("graph review selection, readiness or explicit consent was not confirmed")
        for name, m in r["members"].items():
            _review(m, name, o)
            if (
                m["scope"] != env
                or UUID(m["deployment_id"]) != UUID(r["manifest"]["deployments"][name])
                or m["review_digest"] != r["review_digests"][name]
                or m["policy"]["revision"] != r["manifest"]["expected_policy_revisions"][name]
                or m["service_targets"] != r["manifest"]["service_targets"].get(name, {})
            ):
                raise ValueError("graph member selection was not confirmed")
        o.update(manifest=r["manifest"], review_digests=r["review_digests"], review_digest=r["review_digest"])
        path = f"/v1/projects/{quote(p, safe='')}/environments/{quote(env, safe='')}/release-sets/verification-runs"
    return r, o, path


def _receipt(r, v, o):
    import time

    return (
        r["id"] == o["id"]
        and r["app"] == v["app"]
        and UUID(r["deployment_id"]) == UUID(v["deployment_id"])
        and r["scope"] == v["scope"]
        and r["review_digest"] == v["review_digest"]
        and r["object_storage_write"] == o["object_storage_write"]
        and r["queue_publish"] == o["queue_publish"]
        and (r.get("max_concurrency") or 2) == o["max_concurrency"]
        and r["policy"]["revision"] == v["policy"]["revision"]
        and r["inventory"].get("service_targets", {}) == v["service_targets"]
        and _date(r["created_at"]) <= time.time()
        and abs(_date(r["expires_at"]) - _date(r["created_at"]) - o["ttl_seconds"]) < 0.000001
        and [s["plan"] for s in r["steps"]] == v["plan"]
    )


def _decode(data, v, o, graph):
    model = (ProjectBindingVerificationRun if graph else BindingVerificationRun).from_dict(data)
    _clear_unknown(model)
    r = model.to_dict()
    if graph:
        valid = (
            r["id"] == o["id"]
            and r["project"] == v["project"]
            and r["environment"] == v["environment"]
            and r["review_digest"] == v["review_digest"]
            and r["manifest"] == v["manifest"]
            and r["object_storage_write"] == o["object_storage_write"]
            and r["queue_publish"] == o["queue_publish"]
            and (r.get("max_concurrency") or 2) == o["max_concurrency"]
            and set(r["members"]) == set(v["members"])
        )
        if valid:
            valid = all(
                _uuid(m["id"])
                and _receipt(m, v["members"][name], {**o, "id": m["id"]})
                and m.get("graph_run_id") == r["id"]
                and _date(m["created_at"]) == _date(r["created_at"])
                and _date(m["expires_at"]) == _date(r["expires_at"])
                for name, m in r["members"].items()
            )
    else:
        valid = _receipt(r, v, o) and not r.get("graph_run_id")
    if not valid:
        raise ValueError("server returned another receipt or reviewed selection")
    return model


# Keep this recovery-only budget aligned with pkg/api/limits.go.
_CREATION_RECOVERY_SECONDS = 5


def _creation_check(until, stop):
    if stop is not None and stop.is_set():
        raise RuntimeError("local creation observation stopped")
    remaining = until - time.monotonic()
    if remaining <= 0:
        raise TimeoutError("creation reconciliation deadline expired")
    return remaining


def _usable_creation(model, identity):
    data = model.to_dict()
    selector = dict(
        id=data["id"],
        **(dict(app=identity[0]) if len(identity) == 1 else dict(project=identity[0], environment=identity[1])),
    )
    _validate(data, selector)
    pending, active = _inspect(data, {})
    if data["status"] == "passed" and (
        pending
        or active
        or data.get("qualification")
        or any(m["status"] != "passed" or m.get("qualification") for m in _members(data))
    ):
        raise ValueError("passed creation has unfinished resources")
    if "members" in data:
        ids = [data["id"], *[m["id"] for m in _members(data)]]
        if len(set(ids)) != len(ids):
            raise ValueError("duplicate graph receipt identity")


def _recover_sync(http, path, v, o, identity, cause, began, retries, callback, stop):
    until = min(began + o["ttl_seconds"], time.monotonic() + _CREATION_RECOVERY_SECONDS)

    def emit(event):
        _creation_check(until, stop)
        if callback:
            callback(event)
        _creation_check(until, stop)

    try:
        emit(BindingVerificationProgress(o["id"], -1, "reconciling", operation="creation"))
        for attempt in range(retries + 1):
            remaining = _creation_check(until, stop)
            timeout = copy.copy(http.timeout)
            for name in ("connect", "read", "write", "pool"):
                configured = getattr(timeout, name)
                setattr(timeout, name, min(configured, remaining) if configured is not None else remaining)
            try:
                payload = _response(http.get(f"{path}/{o['id']}", timeout=timeout))
            except Exception as error:
                _creation_check(until, stop)
                if attempt >= retries or not _status_retryable(error):
                    raise
                duration = min(_status_retry_delay(attempt + 1), _creation_check(until, stop))
                emit(
                    BindingVerificationProgress(
                        o["id"], -1, "retrying", retry_attempt=attempt + 1, retry_delay_ms=int(duration * 1000)
                    )
                )
                duration = min(duration, _creation_check(until, stop))
                stop.wait(duration) if stop is not None else time.sleep(duration)
                continue
            model = _decode(payload, v, o, len(identity) == 2)
            _usable_creation(model, identity)
            _creation_check(until, stop)
            return model
    except Exception as error:
        if error is cause:
            raise
        raise cause from error


async def _recover_async(http, path, v, o, identity, cause, began, retries, callback, stop):
    until = min(began + o["ttl_seconds"], time.monotonic() + _CREATION_RECOVERY_SECONDS)

    async def guarded(awaitable):
        task = asyncio.ensure_future(awaitable)
        stopper = None
        try:
            remaining = _creation_check(until, stop)
            stopper = asyncio.create_task(stop.wait()) if stop is not None else None
            done, _ = await asyncio.wait(
                [task, *([stopper] if stopper else [])], timeout=remaining, return_when=asyncio.FIRST_COMPLETED
            )
            if not done:
                raise TimeoutError("creation reconciliation deadline expired")
            if stopper is not None and stopper in done:
                raise RuntimeError("local creation observation stopped")
            return await task
        finally:
            task.cancel()
            if stopper:
                stopper.cancel()
            await asyncio.gather(task, *([stopper] if stopper else []), return_exceptions=True)

    async def emit(event):
        _creation_check(until, stop)
        if callback:
            result = callback(event)
            if hasattr(result, "__await__"):
                await guarded(result)
        _creation_check(until, stop)

    try:
        await emit(BindingVerificationProgress(o["id"], -1, "reconciling", operation="creation"))
        for attempt in range(retries + 1):
            _creation_check(until, stop)
            try:
                payload = _response(await guarded(http.get(f"{path}/{o['id']}")))
            except Exception as error:
                _creation_check(until, stop)
                if attempt >= retries or not _status_retryable(error):
                    raise
                duration = min(_status_retry_delay(attempt + 1), _creation_check(until, stop))
                await emit(
                    BindingVerificationProgress(
                        o["id"], -1, "retrying", retry_attempt=attempt + 1, retry_delay_ms=int(duration * 1000)
                    )
                )
                await guarded(asyncio.sleep(min(duration, _creation_check(until, stop))))
                continue
            model = _decode(payload, v, o, len(identity) == 2)
            _usable_creation(model, identity)
            _creation_check(until, stop)
            return model
    except asyncio.CancelledError as error:
        error.__cause__ = cause
        raise
    except Exception as error:
        if error is cause:
            raise
        raise cause from error


def _sync(
    client,
    identity,
    review,
    *,
    run_id,
    ttl_seconds,
    max_concurrency=2,
    object_storage_write=False,
    queue_publish=False,
    status_read_retries=3,
    on_progress=None,
    stop_event=None,
    read=False,
):
    attempted = False
    try:
        _status_retry_count(status_read_retries)
        if stop_event is not None and stop_event.is_set():
            raise RuntimeError("local creation observation stopped")
        v, o, path = _prepare(
            review, identity, run_id, ttl_seconds, max_concurrency, object_storage_write, queue_publish
        )
        http = getattr(client, "inner", client).get_httpx_client()
        attempted = not read
        began = time.monotonic()
        try:
            payload = _response(http.get(f"{path}/{run_id}") if read else http.post(path, json=o))
        except Exception as cause:
            if read or not _status_retryable(cause):
                raise
            return _recover_sync(http, path, v, o, identity, cause, began, status_read_retries, on_progress, stop_event)
        return _decode(payload, v, o, len(identity) == 2)
    except Exception as cause:
        raise BindingVerificationCreationError(run_id, cause, attempted) from cause


async def _async(
    client,
    identity,
    review,
    *,
    run_id,
    ttl_seconds,
    max_concurrency=2,
    object_storage_write=False,
    queue_publish=False,
    status_read_retries=3,
    on_progress=None,
    stop_event=None,
    read=False,
):
    attempted = False
    try:
        _status_retry_count(status_read_retries)
        if stop_event is not None and stop_event.is_set():
            raise RuntimeError("local creation observation stopped")
        v, o, path = _prepare(
            review, identity, run_id, ttl_seconds, max_concurrency, object_storage_write, queue_publish
        )
        http = getattr(client, "inner", client).get_async_httpx_client()
        attempted = not read
        began = time.monotonic()
        try:
            payload = _response(await http.get(f"{path}/{run_id}") if read else await http.post(path, json=o))
        except Exception as cause:
            if read or not _status_retryable(cause):
                raise
            return await _recover_async(
                http, path, v, o, identity, cause, began, status_read_retries, on_progress, stop_event
            )
        return _decode(payload, v, o, len(identity) == 2)
    except asyncio.CancelledError as cause:
        cause.run_id, cause.attempted = run_id, attempted
        raise
    except Exception as cause:
        raise BindingVerificationCreationError(run_id, cause, attempted) from cause


def create_binding_verification_from_review(client, app, review, **options):
    return _sync(client, (app,), review, **options)


def reconcile_binding_verification_creation(client, app, review, **options):
    return _sync(client, (app,), review, read=True, **options)


def create_project_binding_verification_from_review(client, project, environment, review, **options):
    return _sync(client, (project, environment), review, **options)


def reconcile_project_binding_verification_creation(client, project, environment, review, **options):
    return _sync(client, (project, environment), review, read=True, **options)


async def acreate_binding_verification_from_review(client, app, review, **options):
    return await _async(client, (app,), review, **options)


async def areconcile_binding_verification_creation(client, app, review, **options):
    return await _async(client, (app,), review, read=True, **options)


async def acreate_project_binding_verification_from_review(client, project, environment, review, **options):
    return await _async(client, (project, environment), review, **options)


async def areconcile_project_binding_verification_creation(client, project, environment, review, **options):
    return await _async(client, (project, environment), review, read=True, **options)
