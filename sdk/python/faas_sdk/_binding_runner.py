"""Observe and resume existing binding receipts without replacing or cancelling work."""

import asyncio
import copy
import json
import math
import random
import httpx
import time
from dataclasses import dataclass
from datetime import datetime
from uuid import UUID
from urllib.parse import quote

from ._binding_qualification import pending_qualification
from ._binding_runner_diagnostics import Diagnostics, diagnose_async, diagnose_sync
from .models.binding_verification_qualification import BindingVerificationQualification
from .models.binding_verification_run import BindingVerificationRun
from .models.project_binding_verification_run import ProjectBindingVerificationRun
from ._rfc7807 import FaasProblemError, parse_problem, raise_for_problem


@dataclass(frozen=True)
class BindingVerificationProgress:
    run_id: str
    step: int
    status: str
    app: str | None = None
    member_run_id: str | None = None
    resource_id: str | None = None
    resource_kind: str | None = None
    retry_attempt: int | None = None
    retry_delay_ms: int | None = None
    qualification: BindingVerificationQualification | None = None
    operation: str | None = None


class BindingVerificationRecoveryError(Exception):
    def __init__(self, run_id, cause, run=None, diagnostics: Diagnostics | None = None):
        super().__init__(f"verification run {run_id} stopped; resume this run: {cause}")
        self.run_id, self.cause, self.run = run_id, cause, copy.deepcopy(run)
        self.diagnostics = diagnostics


def _uuid(value, compact=False):
    try:
        parsed = UUID(value)
        return parsed.int != 0 and (str(parsed) == value or compact and parsed.hex == value)
    except (ValueError, TypeError, AttributeError):
        return False


def _date(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()


def _members(run):
    return [run["members"][name] for name in sorted(run["members"])] if "members" in run else [run]


def _frozen(run):
    run = copy.deepcopy(run)
    for member in [run, *_members(run)]:
        member["status"] = "pending"
        member.pop("reason", None)
        member.pop("qualification", None)
        for step in member.get("steps", []):
            step["status"], step["owned"] = "", False
            step.pop("resource_id", None)
            step.pop("resource_kind", None)
    return json.dumps(run, sort_keys=True)


def _validate(run, identity):
    if any(run.get(key) != value for key, value in identity.items()):
        raise ValueError("server returned another run")
    if not _uuid(run["id"]) or run["status"] not in ("pending", "passed"):
        raise ValueError("run is not resumable")
    now = time.time()
    for member in _members(run):
        created, expires = _date(member["created_at"]), _date(member["expires_at"])
        if not (
            0 <= member.get("max_concurrency", 0) <= 8
            and _uuid(member["id"])
            and member["app"]
            and _uuid(member["deployment_id"], True)
            and member["review_digest"]
            and created <= now < expires
            and 0 < expires - created <= 600
            and len(member["steps"]) <= 128
            and member["status"] in ("pending", "passed")
        ):
            raise ValueError("server did not confirm original member and deadline")
    if "members" in run:
        manifest = run["manifest"]
        if not (
            run["review_digest"]
            and 1 <= len(run["members"]) <= 100
            and set(run["members"]) == set(manifest["deployments"])
            and manifest["project"] == run["project"]
            and manifest["environment"] == run["environment"]
        ):
            raise ValueError("server did not confirm graph membership")
        for name, member in run["members"].items():
            if not (
                (member.get("max_concurrency") or 2) == (run.get("max_concurrency") or 2)
                and member["app"] == name
                and member.get("graph_run_id") == run["id"]
                and UUID(member["deployment_id"]) == UUID(manifest["deployments"][name])
                and member["scope"] == run["environment"]
                and member["created_at"] == run["created_at"]
                and member["expires_at"] == run["expires_at"]
                and member["policy"]["revision"] == manifest["expected_policy_revisions"][name]
                and member["inventory"].get("service_targets", {}) == manifest["service_targets"].get(name, {})
                and member["queue_publish"] == run["queue_publish"]
                and member["object_storage_write"] == run["object_storage_write"]
            ):
                raise ValueError("server changed graph member selection")


def _inspect(run, known):
    pending, active = [], 0
    for member in _members(run):
        for index, step in enumerate(member["steps"]):
            key = (member["app"], index)
            ref = (step.get("resource_id"), step.get("resource_kind"), step["owned"])
            if key in known and known[key] != ref:
                raise ValueError("server changed admitted resource identity or owner")
            if step.get("resource_id"):
                kind = "queue" if step["plan"]["type"] == "queue" else "task"
                if not (
                    _uuid(step["resource_id"])
                    and step["resource_kind"] == kind
                    and step["owned"] == (step["plan"]["action"] in ("probe", "queue-canary"))
                ):
                    raise ValueError("invalid resource identity or owner")
                known[key] = ref
                if step["status"] == "succeeded":
                    continue
                if step["status"] in (
                    ("published", "dispatching") if kind == "queue" else ("queued", "restoring", "running")
                ):
                    active += 1
                    continue
                raise ValueError("resource did not succeed")
            if step["owned"] or step.get("resource_kind"):
                raise ValueError("incomplete resource identity")
            action = step["plan"]["action"]
            if action in ("reuse", "observe", "skip"):
                continue
            if action in ("probe", "queue-canary") and member["status"] != "passed":
                pending.append((member, index, step))
            else:
                raise ValueError("reviewed step cannot be executed")
    return pending, active


class _UncertainWrite:
    def __init__(self, cause):
        self.cause = cause


def _reconcile_write(uncertain, identity, path, frozen, known, operation, target=None):
    fields = {}
    if target:
        member, index, _ = target
        fields = dict(app=member["app"], member_run_id=member["id"])
    event = BindingVerificationProgress(
        identity["id"], target[1] if target else -1, "reconciling", operation=operation, **fields
    )
    yield ("progress", event, None, uncertain.cause)
    fresh = yield ("request", "GET", path, uncertain.cause)
    try:
        _validate(fresh, identity)
        if _frozen(fresh) != frozen:
            raise ValueError("reconciliation changed original review or deadline")
        pending, active = _inspect(fresh, known)
        if target:
            member, index, expected = target
            observed = next(m for m in _members(fresh) if m["app"] == member["app"] and m["id"] == member["id"])
            step = observed["steps"][index]
            if not (
                step["owned"]
                and _uuid(step.get("resource_id"))
                and step.get("resource_kind") == ("queue" if expected["plan"]["type"] == "queue" else "task")
                and step["plan"] == expected["plan"]
            ):
                raise ValueError("admission was not confirmed")
        elif (
            fresh["status"] != "passed"
            or fresh.get("qualification")
            or pending
            or active
            or any(m["status"] != "passed" or m.get("qualification") for m in _members(fresh))
        ):
            raise ValueError("completion was not confirmed")
    except Exception as error:
        raise uncertain.cause from error
    return fresh


def _drive(identity, path, interval, concurrency, snapshot):
    if (
        not math.isfinite(interval)
        or interval <= 0
        or isinstance(concurrency, bool)
        or not isinstance(concurrency, int)
        or not 1 <= concurrency <= 8
    ):
        raise ValueError("poll interval must be positive and concurrency between 1 and 8")
    if not _uuid(identity["id"]):
        raise ValueError("run ID must be a nonzero canonical UUID")
    run = yield ("request", "POST", path + "/resume")
    snapshot[0] = run
    _validate(run, identity)
    snapshot[1] = copy.deepcopy(run)
    frozen, known, seen, confirmed = _frozen(run), {}, {}, False
    waiting = None
    while True:
        if snapshot[2]:
            seen.pop((None, -1), None)
            snapshot[2] = False
        _validate(run, identity)
        if _frozen(run) != frozen:
            raise ValueError("server changed original review or deadline")
        pending, active = _inspect(run, known)
        events = [
            BindingVerificationProgress(
                identity["id"],
                -1,
                "qualifying"
                if run["status"] == "passed" and not confirmed
                else "waiting_for_qualification"
                if waiting and run["status"] == "pending"
                else run["status"],
                qualification=BindingVerificationQualification.from_dict(waiting) if waiting else None,
            )
        ]
        for member in _members(run):
            for index, step in enumerate(member["steps"]):
                events.append(
                    BindingVerificationProgress(
                        identity["id"],
                        index,
                        step["status"],
                        member["app"],
                        member["id"],
                        step.get("resource_id"),
                        step.get("resource_kind"),
                    )
                )
        for event in events:
            key = (event.app, event.step)
            if seen.get(key) != event:
                yield ("progress", event)
                seen[key] = event
        if run["status"] == "passed":
            if pending or active or any(m["status"] != "passed" for m in _members(run)):
                raise ValueError("passed receipt has unfinished resources")
            if confirmed:
                return (ProjectBindingVerificationRun if "members" in run else BindingVerificationRun).from_dict(run)
        if not pending and not active:
            run = yield ("request", "POST", path + "/complete")
            if isinstance(run, _UncertainWrite):
                run = yield from _reconcile_write(run, identity, path, frozen, known, "completion")
            snapshot[0] = run
            _validate(run, identity)
            if _frozen(run) != frozen:
                raise ValueError("server changed original review during qualification")
            if run["status"] == "pending":
                completion_pending, completion_active = _inspect(run, known)
                if completion_pending or completion_active:
                    raise ValueError("pending qualification has unfinished work")
                waiting = pending_qualification(run)
                yield (
                    "progress",
                    BindingVerificationProgress(
                        identity["id"],
                        -1,
                        "waiting_for_qualification",
                        qualification=BindingVerificationQualification.from_dict(waiting) if waiting else None,
                    ),
                )
                yield ("sleep", interval)
                run = yield ("request", "GET", path)
                snapshot[0] = run
                continue
            if run["status"] != "passed" or run.get("qualification"):
                raise ValueError("server did not confirm passed run")
            waiting = None
            confirmed = True
            continue
        admitted, budget_full, reconciled = False, False, False
        for member, index, step in pending:
            if active >= concurrency:
                break
            admitted_step = yield (
                "request",
                "POST",
                f"/v1/apps/{quote(member['app'], safe='')}/bindings/verification-runs/{member['id']}/steps/{index}/admit",
            )
            if isinstance(admitted_step, _UncertainWrite):
                run = yield from _reconcile_write(
                    admitted_step, identity, path, frozen, known, "admission", (member, index, step)
                )
                snapshot[0] = run
                reconciled = True
                break
            if admitted_step is _BUDGET_FULL:
                budget_full = True
                break
            if not (
                admitted_step["plan"] == step["plan"]
                and admitted_step["owned"]
                and _uuid(admitted_step.get("resource_id"))
                and admitted_step.get("resource_kind") == ("queue" if step["plan"]["type"] == "queue" else "task")
            ):
                raise ValueError("server did not confirm reviewed admission")
            member["steps"][index] = admitted_step
            _inspect(run, known)
            event = BindingVerificationProgress(
                identity["id"],
                index,
                admitted_step["status"],
                member["app"],
                member["id"],
                admitted_step["resource_id"],
                admitted_step["resource_kind"],
            )
            yield ("progress", event)
            seen[(member["app"], index)] = event
            active += admitted_step["status"] != "succeeded"
            admitted = True
        if reconciled:
            continue
        if not admitted or budget_full:
            yield ("sleep", interval)
        run = yield ("request", "GET", path)
        snapshot[0] = run


def _check(stop, snapshot, started):
    if stop is not None and stop.is_set():
        raise RuntimeError("local observation stopped")
    deadline = _date(snapshot[0]["expires_at"]) if snapshot[0] else started + 600
    remaining = deadline - time.time()
    if remaining <= 0:
        raise TimeoutError("original run deadline expired")
    return remaining


_BUDGET_FULL = object()


def _response(response, admission=False):
    if response.status_code >= 400:
        problem = parse_problem(response.content, response.headers.get("content-type", ""), response.status_code)
        if problem is not None:
            if admission and response.status_code == 429 and problem.code == "binding_verification_budget_full":
                return _BUDGET_FULL
            raise_for_problem(problem)
        response.raise_for_status()
    return response.json()


def _status_retryable(error):
    if isinstance(error, FaasProblemError):
        return error.status in (502, 503, 504)
    if isinstance(error, httpx.HTTPStatusError):
        return error.response.status_code in (502, 503, 504)
    return isinstance(
        error,
        (httpx.ConnectError, httpx.ReadError, httpx.WriteError, httpx.RemoteProtocolError, httpx.TimeoutException),
    )


def _status_retry_count(value):
    if type(value) is not int or not 0 <= value <= 8:
        raise ValueError("status read retries must be between zero and eight")


def _status_retry_delay(attempt):
    cap = min(0.25 * 2 ** (attempt - 1), 2)
    return random.uniform(cap / 2, cap)


def _sync(
    client,
    identity,
    path,
    *,
    poll_interval=1,
    concurrency=2,
    on_progress=None,
    stop_event=None,
    skip_diagnostics=False,
    status_read_retries=3,
):
    client = getattr(client, "inner", client)
    snapshot, started = [None, None, False], time.time()
    local_failure = False
    control_started = False
    iterator = _drive(identity, path, poll_interval, concurrency, snapshot)
    value = None
    operation = None
    try:
        _status_retry_count(status_read_retries)
        while True:
            remaining = _check(stop_event, snapshot, started)
            try:
                operation = iterator.send(value)
            except StopIteration as done:
                return done.value
            if operation[0] == "request":
                control_started = True

                attempt = 0
                while True:
                    remaining = _check(stop_event, snapshot, started)
                    transport = client.get_httpx_client()
                    timeout = copy.copy(transport.timeout)
                    for name in ("connect", "read", "write", "pool"):
                        configured = getattr(timeout, name)
                        setattr(timeout, name, min(configured, remaining) if configured is not None else remaining)
                    admission = operation[2].endswith("/admit")
                    try:
                        value = _response(transport.request(operation[1], operation[2], timeout=timeout), admission)
                        break
                    except Exception as error:
                        if (
                            admission
                            and isinstance(error, FaasProblemError)
                            and error.status == 429
                            and error.problem.code == "binding_verification_budget_full"
                        ):
                            value = _BUDGET_FULL
                            break
                        if (
                            operation[1] == "POST"
                            and (admission or operation[2].endswith("/complete"))
                            and _status_retryable(error)
                        ):
                            _check(stop_event, snapshot, started)
                            value = _UncertainWrite(error)
                            break
                        if operation[1] != "GET" or attempt >= status_read_retries or not _status_retryable(error):
                            if len(operation) > 3:
                                raise operation[3] from error
                            raise
                        attempt += 1
                        snapshot[2] = True
                        duration = min(_status_retry_delay(attempt), _check(stop_event, snapshot, started))
                        if on_progress:
                            local_failure = True
                            on_progress(
                                BindingVerificationProgress(
                                    identity["id"],
                                    -1,
                                    "retrying",
                                    retry_attempt=attempt,
                                    retry_delay_ms=int(duration * 1000),
                                )
                            )
                            local_failure = False
                        duration = min(duration, _check(stop_event, snapshot, started))
                        if stop_event is not None:
                            stop_event.wait(duration)
                        else:
                            time.sleep(duration)
            elif operation[0] == "progress":
                if on_progress:
                    local_failure = True
                    on_progress(operation[1])
                    local_failure = False
                value = None
            else:
                duration = min(operation[1], remaining)
                stop_event.wait(duration) if stop_event is not None else time.sleep(duration)
                value = None
    except Exception as error:
        # A local deadline/stop may fire between a reconciliation GET and validation.
        # Keep the uncertain write reachable without replacing the stop exception.
        if operation is not None and len(operation) > 3 and error is not operation[3] and error.__cause__ is None:
            error.__cause__ = operation[3]
        details = None
        if control_started and not skip_diagnostics and not local_failure and not isinstance(error, TimeoutError):
            try:
                details = diagnose_sync(client, identity, path, snapshot[1], stop_event)
            except Exception:
                pass
        raise BindingVerificationRecoveryError(identity["id"], error, snapshot[0], details) from error
    finally:
        iterator.close()


async def _async(
    client,
    identity,
    path,
    *,
    poll_interval=1,
    concurrency=2,
    on_progress=None,
    stop_event=None,
    skip_diagnostics=False,
    status_read_retries=3,
):
    client = getattr(client, "inner", client)
    snapshot, started = [None, None, False], time.time()
    local_failure = False
    control_started = False
    iterator = _drive(identity, path, poll_interval, concurrency, snapshot)
    value = None
    operation = None

    async def guarded(awaitable, remaining):
        task = asyncio.ensure_future(awaitable)
        stopper = asyncio.create_task(stop_event.wait()) if stop_event is not None else None
        try:
            done, _ = await asyncio.wait(
                [task, *([stopper] if stopper else [])], timeout=remaining, return_when=asyncio.FIRST_COMPLETED
            )
            if not done:
                raise TimeoutError("original run deadline expired")
            if stopper is not None and stopper in done:
                raise RuntimeError("local observation stopped")
            return await task
        finally:
            task.cancel()
            if stopper:
                stopper.cancel()
            await asyncio.gather(task, *([stopper] if stopper else []), return_exceptions=True)

    try:
        _status_retry_count(status_read_retries)
        while True:
            remaining = _check(stop_event, snapshot, started)
            try:
                operation = iterator.send(value)
            except StopIteration as done:
                return done.value
            if operation[0] == "request":
                control_started = True

                admission = operation[2].endswith("/admit")
                attempt = 0
                while True:
                    remaining = _check(stop_event, snapshot, started)
                    try:
                        value = _response(
                            await guarded(
                                client.get_async_httpx_client().request(operation[1], operation[2]), remaining
                            ),
                            admission,
                        )
                        break
                    except Exception as error:
                        if (
                            admission
                            and isinstance(error, FaasProblemError)
                            and error.status == 429
                            and error.problem.code == "binding_verification_budget_full"
                        ):
                            value = _BUDGET_FULL
                            break
                        if (
                            operation[1] == "POST"
                            and (admission or operation[2].endswith("/complete"))
                            and _status_retryable(error)
                        ):
                            _check(stop_event, snapshot, started)
                            value = _UncertainWrite(error)
                            break
                        if operation[1] != "GET" or attempt >= status_read_retries or not _status_retryable(error):
                            if len(operation) > 3:
                                raise operation[3] from error
                            raise
                        attempt += 1
                        snapshot[2] = True
                        remaining = _check(stop_event, snapshot, started)
                        duration = min(_status_retry_delay(attempt), remaining)
                        if on_progress:
                            local_failure = True
                            result = on_progress(
                                BindingVerificationProgress(
                                    identity["id"],
                                    -1,
                                    "retrying",
                                    retry_attempt=attempt,
                                    retry_delay_ms=int(duration * 1000),
                                )
                            )
                            if hasattr(result, "__await__"):
                                await guarded(result, remaining)
                            local_failure = False
                        remaining = _check(stop_event, snapshot, started)
                        await guarded(asyncio.sleep(min(duration, remaining)), remaining)
            elif operation[0] == "progress":
                if on_progress:
                    local_failure = True
                    result = on_progress(operation[1])
                    if hasattr(result, "__await__"):
                        await guarded(result, remaining)
                    local_failure = False
                value = None
            else:
                await guarded(asyncio.sleep(min(operation[1], remaining)), remaining)
                value = None
    except asyncio.CancelledError as error:
        error.run_id = identity["id"]
        if operation is not None and len(operation) > 3 and error.__cause__ is None:
            error.__cause__ = operation[3]
        raise
    except Exception as error:
        # A local deadline/stop may fire between a reconciliation GET and validation.
        # Keep the uncertain write reachable without replacing the stop exception.
        if operation is not None and len(operation) > 3 and error is not operation[3] and error.__cause__ is None:
            error.__cause__ = operation[3]
        details = None
        if control_started and not skip_diagnostics and not local_failure and not isinstance(error, TimeoutError):
            try:
                details = await diagnose_async(client, identity, path, snapshot[1], stop_event)
            except asyncio.CancelledError as cancelled:
                cancelled.run_id = identity["id"]
                raise
            except Exception:
                pass
        raise BindingVerificationRecoveryError(identity["id"], error, snapshot[0], details) from error
    finally:
        iterator.close()


def _app(app, run_id):
    return {
        "id": run_id,
        "app": app,
    }, f"/v1/apps/{quote(app, safe='')}/bindings/verification-runs/{quote(run_id, safe='')}"


def _project(project, environment, run_id):
    return (
        {"id": run_id, "project": project, "environment": environment},
        f"/v1/projects/{quote(project, safe='')}/environments/{quote(environment, safe='')}/release-sets/verification-runs/{quote(run_id, safe='')}",
    )


def run_binding_verification(client, app, run_id, **options):
    return _sync(client, *_app(app, run_id), **options)


def run_project_binding_verification(client, project, environment, run_id, **options):
    return _sync(client, *_project(project, environment, run_id), **options)


async def arun_binding_verification(client, app, run_id, **options):
    return await _async(client, *_app(app, run_id), **options)


async def arun_project_binding_verification(client, project, environment, run_id, **options):
    return await _async(client, *_project(project, environment, run_id), **options)
