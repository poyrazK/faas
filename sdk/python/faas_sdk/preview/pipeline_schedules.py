"""Typed native schedule inspection, bounded history, replay and run attachment."""

from __future__ import annotations

import asyncio
import json
from collections.abc import Awaitable, Callable
from typing import Any, Generic, Literal, TypeVar
from urllib.parse import quote, urlencode

import httpx

from ..job_runtime import JobError, _scope, job_value_hash
from .job_pipeline_client import JobPipelineClient, PipelineCheckpoint, PipelineInspection, TypedPipelineRun
from .pipeline_schedule_state import (
    PipelineSchedule,
    PipelineScheduleError,
    PipelineScheduleHistory,
    PipelineScheduleInfo,
    PipelineScheduleInspection,
    PipelineScheduleOccurrence,
    PipelineSchedulePreview,
    PipelineScheduleReplayOutcome,
    calendar,
    capture_schedule,
    instant,
    integer,
    metadata,
    occurrence,
    preview,
    replay_outcomes,
)
from .pipeline_state import _uuid

I = TypeVar("I")
O = TypeVar("O")
A = TypeVar("A")
T = TypeVar("T")
PipelineScheduleAction = Literal[
    "inspect", "preview", "history", "preview_replay", "replay", "attach", "read_run", "cancel_run"
]
Request = Callable[..., Awaitable[dict[str, Any]]]


class _RejectedRequest(PipelineScheduleError):
    def __init__(self) -> None:
        super().__init__("request_failed")


class ScheduledPipelineRun(Generic[O, A]):
    def __init__(self, client: PipelineScheduleClient[Any, O, A], run: TypedPipelineRun[O], access: A):
        self._client, self._run, self._access = client, run, access

    @property
    def checkpoint(self) -> PipelineCheckpoint:
        return self._run.checkpoint

    async def inspect(self, *, timeout: float) -> PipelineInspection:
        async def work(
            request: Request, remaining: Callable[[], float], dispatch: Callable[[], None]
        ) -> PipelineInspection:
            return await self._run.inspect(timeout=remaining())

        return await self._client._operation(self._access, "read_run", timeout, work)

    async def result(self, *, timeout: float) -> O:
        async def work(request: Request, remaining: Callable[[], float], dispatch: Callable[[], None]) -> O:
            return await self._run.result(timeout=remaining())

        return await self._client._operation(self._access, "read_run", timeout, work)

    async def cancel(self, *, timeout: float) -> PipelineInspection:
        async def work(
            request: Request, remaining: Callable[[], float], dispatch: Callable[[], None]
        ) -> PipelineInspection:
            return await self._run.cancel(timeout=remaining())

        return await self._client._operation(self._access, "cancel_run", timeout, work)


class PipelineScheduleClient(Generic[I, O, A]):
    def __init__(
        self,
        schedule: PipelineSchedule[I, O],
        *,
        deployment_id: str,
        token: str,
        authorize: Callable[[A, PipelineScheduleAction], Awaitable[bool]],
        httpx_args: dict[str, Any] | None = None,
    ):
        schedule = capture_schedule(schedule)
        httpx_args = dict(httpx_args or {})
        if (
            not _uuid(deployment_id)
            or not token
            or set(httpx_args)
            & {"base_url", "headers", "auth", "params", "cookies", "follow_redirects", "timeout", "verify"}
        ):
            raise PipelineScheduleError("invalid_input")
        self._schedule, self._deployment, self._token, self._authorize = schedule, deployment_id, token, authorize
        self._args = dict(httpx_args)
        self._args.setdefault("timeout", None)
        self._root = "/v1/apps/" + quote(schedule.scope.app_slug, safe="")
        self._pipeline = JobPipelineClient[I, O](
            base_url=schedule.scope.base_url,
            token=token,
            scope=schedule.scope,
            owner_id=schedule.owner_id,
            contract=schedule.contract,
            httpx_args=httpx_args,
        )

    async def __aenter__(self) -> PipelineScheduleClient[I, O, A]:
        return self

    async def __aexit__(self, *args: Any) -> None:
        await self._pipeline.__aexit__(*args)

    async def _operation(
        self,
        access: A,
        action: PipelineScheduleAction,
        timeout: float,
        work: Callable[[Request, Callable[[], float], Callable[[], None]], Awaitable[T]],
        ids: tuple[str, ...] = (),
    ) -> T:
        sent = False

        async def run(check: Callable[[], None]) -> T:
            nonlocal sent
            deadline = asyncio.get_running_loop().time() + timeout

            def remaining() -> float:
                check()
                left = deadline - asyncio.get_running_loop().time()
                if left <= 0:
                    raise PipelineScheduleError("deadline")
                return left

            remaining()
            if await self._authorize(access, action) is not True:
                raise PipelineScheduleError("unauthorized")
            remaining()
            async with httpx.AsyncClient(
                base_url=self._schedule.scope.base_url,
                headers={"authorization": "Bearer " + self._token, "accept": "application/json"},
                follow_redirects=False,
                **self._args,
            ) as client:

                async def request(path: str, body: Any = None) -> dict[str, Any]:
                    remaining()
                    async with client.stream(
                        "GET" if body is None else "POST", path, **({"json": body} if body is not None else {})
                    ) as response:
                        if not response.is_success:
                            if 400 <= response.status_code < 500 and response.status_code not in (408, 429):
                                raise _RejectedRequest()
                            raise PipelineScheduleError("request_failed")
                        data = bytearray()
                        async for chunk in response.aiter_bytes(chunk_size=8192):
                            remaining()
                            if len(data) + len(chunk) > 1048576:
                                raise PipelineScheduleError("invalid_response")
                            data.extend(chunk)
                    remaining()
                    value = json.loads(data)
                    if not isinstance(value, dict):
                        raise PipelineScheduleError("invalid_response")
                    return value

                account = await request("/v1/account")
                if account.get("id") != self._schedule.scope.account_id:
                    raise PipelineScheduleError("scope_mismatch")
                app = await request(self._root)
                if app.get("id") != self._schedule.scope.app_id:
                    raise PipelineScheduleError("scope_mismatch")

                def dispatch() -> None:
                    nonlocal sent
                    remaining()
                    sent = True

                value = await work(request, remaining, dispatch)
                remaining()
                return value

        try:
            return await _scope(timeout, run)
        except BaseException as error:
            if sent and not isinstance(error, _RejectedRequest):
                raise PipelineScheduleError("replay_unknown", ids) from None
            if isinstance(error, PipelineScheduleError):
                raise
            if isinstance(error, JobError) and error.code == "deadline":
                raise PipelineScheduleError("deadline") from None
            if not isinstance(error, Exception):
                raise
            raise PipelineScheduleError("request_failed") from None

    async def _verify_binding(self, request: Request) -> None:
        s = self._schedule
        deployed = await request("/v1/deployments/" + self._deployment)
        if (
            deployed.get("id") != self._deployment
            or deployed.get("app_id") != s.scope.app_id
            or (deployed.get("scope") or "default") != "default"
        ):
            raise PipelineScheduleError("scope_mismatch")
        defs = [d for d in deployed.get("workflows", []) if d.get("name") == s.contract.spec["name"]]
        if (
            len(defs) != 1
            or job_value_hash(calendar(defs[0].get("trigger"))) != job_value_hash(calendar(s.trigger))
            or job_value_hash(defs[0]["trigger"].get("input")) != job_value_hash(s.trigger["input"])
        ):
            raise PipelineScheduleError("definition_changed")

    async def _verify(self, request: Request) -> PipelineScheduleInspection:
        s = self._schedule
        listing = await request(self._root + "/workflows/schedules")
        if type(listing.get("runtime_enabled")) is not bool or not isinstance(listing.get("schedules"), list):
            raise PipelineScheduleError("invalid_response")
        rows = [r for r in listing["schedules"] if r.get("workflow_name") == s.contract.spec["name"]]
        if len(rows) != 1:
            raise PipelineScheduleError("not_found")
        current = metadata(rows[0], s, self._deployment)
        await self._verify_binding(request)
        reason = listing.get("unavailable_reason")
        if reason is not None and reason not in (
            "runtime_disabled",
            "account_inactive",
            "plan_not_allowed",
            "maintenance",
            "tenant_required",
        ):
            raise PipelineScheduleError("invalid_response")
        return PipelineScheduleInspection(
            runtime_enabled=listing["runtime_enabled"],
            schedule=PipelineScheduleInfo(**current),
            unavailable_reason=reason,
        )

    async def _page(self, request: Request, cursor: str | None = None, limit: int = 100) -> dict[str, Any]:
        integer(limit, 1, 200)
        if cursor is not None and not _uuid(cursor):
            raise PipelineScheduleError("invalid_input")
        raw = await request(
            self._root
            + "/workflows/schedules/occurrences?"
            + urlencode(dict(limit=limit, **({"cursor": cursor} if cursor else {})))
        )
        if (
            not isinstance(raw.get("occurrences"), list)
            or len(raw["occurrences"]) > limit
            or "next_cursor" in raw
            and (not _uuid(raw["next_cursor"]) or raw["next_cursor"] == cursor)
        ):
            raise PipelineScheduleError("invalid_response")
        rows = [occurrence(r, self._schedule.scope.app_id) for r in raw["occurrences"]]
        seen = set()
        last = None
        for row in rows:
            stamp = instant(row["scheduled_for"])
            if row["id"] in seen or last is not None and stamp > last:
                raise PipelineScheduleError("invalid_response")
            seen.add(row["id"])
            last = stamp
        return dict(
            occurrences=[
                r
                for r in rows
                if r["workflow_name"] == self._schedule.contract.spec["name"]
                and r["deployment_id"] == self._deployment
                and r.get("platform_tenant_id") is None
            ],
            **({"next_cursor": raw["next_cursor"]} if raw.get("next_cursor") else {}),
        )

    @staticmethod
    def _selection(ids: list[str] | tuple[str, ...]) -> tuple[str, ...]:
        if (
            not isinstance(ids, (tuple, list))
            or not 1 <= len(ids) <= 20
            or any(not _uuid(i) for i in ids)
            or len(set(ids)) != len(ids)
        ):
            raise PipelineScheduleError("invalid_input")
        return tuple(ids)

    async def _find(self, request: Request, ids: tuple[str, ...], maximum: int = 5) -> list[dict[str, Any]]:
        integer(maximum, 1, 100)
        found = {}
        cursors = set()
        cursor = None
        for _ in range(maximum):
            page = await self._page(request, cursor, 200)
            for row in page["occurrences"]:
                if row["id"] in ids:
                    if row["id"] in found:
                        raise PipelineScheduleError("invalid_response")
                    found[row["id"]] = row
            if len(found) == len(ids):
                return [found[i] for i in ids]
            if not page.get("next_cursor"):
                raise PipelineScheduleError("not_found")
            if page["next_cursor"] in cursors:
                raise PipelineScheduleError("invalid_response")
            cursors.add(page["next_cursor"])
            cursor = page["next_cursor"]
        raise PipelineScheduleError("history_limit")

    async def inspect(self, access: A, *, timeout: float) -> PipelineScheduleInspection:
        async def work(
            request: Request, remaining: Callable[[], float], dispatch: Callable[[], None]
        ) -> PipelineScheduleInspection:
            return await self._verify(request)

        return await self._operation(access, "inspect", timeout, work)

    async def preview(
        self, access: A, *, timeout: float, count: int = 5, at: str | None = None, since: str | None = None
    ) -> PipelineSchedulePreview:
        async def work(
            request: Request, remaining: Callable[[], float], dispatch: Callable[[], None]
        ) -> PipelineSchedulePreview:
            integer(count, 1, 20)
            for t in (at, since):
                if t is not None:
                    instant(t)
            await self._verify(request)
            params = dict(count=str(count), **({"at": at} if at else {}), **({"since": since} if since else {}))
            r = await request(
                self._root
                + "/workflows/schedules/"
                + quote(self._schedule.contract.spec["name"], safe="")
                + "/preview?"
                + urlencode(params)
            )
            return PipelineSchedulePreview.from_dict(preview(r, self._schedule, self._deployment, count))

        return await self._operation(access, "preview", timeout, work)

    async def history(
        self, access: A, *, timeout: float, cursor: str | None = None, limit: int = 100
    ) -> PipelineScheduleHistory:
        async def work(
            request: Request, remaining: Callable[[], float], dispatch: Callable[[], None]
        ) -> PipelineScheduleHistory:
            await self._verify_binding(request)
            page = await self._page(request, cursor, limit)
            return PipelineScheduleHistory(
                occurrences=tuple(PipelineScheduleOccurrence(**r) for r in page["occurrences"]),
                next_cursor=page.get("next_cursor"),
            )

        return await self._operation(access, "history", timeout, work)

    async def _replay(
        self, ids: list[str] | tuple[str, ...], access: A, timeout: float, maximum: int, is_preview: bool
    ) -> tuple[PipelineScheduleReplayOutcome, ...]:
        saved = self._selection(ids)

        async def work(
            request: Request, remaining: Callable[[], float], dispatch: Callable[[], None]
        ) -> tuple[PipelineScheduleReplayOutcome, ...]:
            await self._verify(request)
            rows = await self._find(request, saved, maximum)
            remaining()
            if not is_preview:
                dispatch()
            value = await request(
                self._root + "/workflows/schedules/occurrences:" + ("replay-preview" if is_preview else "replay"),
                dict(occurrence_ids=saved),
            )
            return tuple(PipelineScheduleReplayOutcome(**r) for r in replay_outcomes(value, rows, is_preview))

        return await self._operation(access, "preview_replay" if is_preview else "replay", timeout, work, saved)

    async def preview_replay(
        self, ids: list[str] | tuple[str, ...], access: A, *, timeout: float, max_pages: int = 5
    ) -> tuple[PipelineScheduleReplayOutcome, ...]:
        return await self._replay(ids, access, timeout, max_pages, True)

    async def replay(
        self, ids: list[str] | tuple[str, ...], access: A, *, timeout: float, max_pages: int = 5
    ) -> tuple[PipelineScheduleReplayOutcome, ...]:
        return await self._replay(ids, access, timeout, max_pages, False)

    async def attach(
        self, id: str, access: A, *, timeout: float, max_pages: int = 5, replayed: bool = False
    ) -> ScheduledPipelineRun[O, A]:
        ids = self._selection([id])

        async def work(
            request: Request, remaining: Callable[[], float], dispatch: Callable[[], None]
        ) -> ScheduledPipelineRun[O, A]:
            await self._verify_binding(request)
            row = (await self._find(request, ids, max_pages))[0]
            run = row.get("replay_run_id" if replayed else "run_id")
            if not _uuid(run):
                raise PipelineScheduleError("not_found")
            record = await request("/v1/workflows/runs/" + str(run))
            s = self._schedule
            if (
                record.get("id") != run
                or record.get("app_id") != s.scope.app_id
                or record.get("deployment_id") != self._deployment
                or record.get("workflow_name") != s.contract.spec["name"]
                or record.get("platform_tenant_id") is not None
                or job_value_hash(record.get("input")) != job_value_hash(s.trigger["input"])
                or instant(record.get("scheduled_for")) != instant(row["scheduled_for"])
            ):
                raise PipelineScheduleError("scope_mismatch")
            attached = await self._pipeline.resume_by_id(str(run), timeout=remaining())
            if attached.checkpoint.input_sha256 != job_value_hash(s.trigger["input"]):
                raise PipelineScheduleError("scope_mismatch")
            return ScheduledPipelineRun(self, attached, access)

        return await self._operation(access, "attach", timeout, work)
