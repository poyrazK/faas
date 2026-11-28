"""Account-owned project preview, reviewed apply, and receipt-only recovery."""

from __future__ import annotations

import asyncio
import copy
import hashlib
import inspect
import json
import math
import os
import re
import stat
import struct
import tempfile
import threading
from collections.abc import Awaitable, Callable, Coroutine
from dataclasses import asdict, dataclass, field, replace
from pathlib import Path
from typing import TYPE_CHECKING, Any, Literal, TypeVar
from uuid import uuid4

from ._async_request import (
    RequestOptions,
    RequestTimeoutError,
    owned_async_transport,
    run_request,
    snapshot_async_client,
)
from ._source_deployment import SourceDeploymentPreparationOptions
from ._source_preparation import SourcePreparationOptions, prepare_source
from ._source_upload import _base, _status
from ._waiters import DeploymentWaitOptions, WaiterError, wait_for_deployment
from .models.plan_response import PlanResponse
from .models.project_submission_receipt import ProjectSubmissionReceipt
from .models.release_timeline import ReleaseTimeline

if TYPE_CHECKING:
    from ._wrapper import FaaSClient

ProjectWorkflowCode = Literal[
    "invalid_plan",
    "invalid_state",
    "state_mismatch",
    "busy",
    "state_failed",
    "preview_failed",
    "preview_expired",
    "plan_stale",
    "permission_denied",
    "blocked",
    "acceptance_unknown",
    "invalid_response",
    "poll_limit",
    "progress_failed",
    "timeout",
    "cancelled",
]
ProjectWorkflowPhase = Literal["preparing", "previewing", "applying", "recovering", "observing", "complete"]
ProjectWorkloadStatus = Literal["succeeded", "failed", "blocked", "unchanged", "unknown"]
T = TypeVar("T")
_ACTIVE: set[str] = set()
_LOCK = threading.Lock()


def _stable(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)


def _canonical(value: Any) -> str:
    # Typed number encoding avoids differences in JS/Python decimal/exponent
    # formatting. JSON strings stay quoted, so the numeric tag is unambiguous.
    if type(value) in (int, float):
        number = float(value)
        if not math.isfinite(number):
            raise ValueError("Non-finite plan number")
        return "n" + struct.pack(">d", number if number else 0.0).hex()
    if isinstance(value, list):
        return "[" + ",".join(_canonical(item) for item in value) + "]"
    if isinstance(value, dict):
        return "{" + ",".join(_stable(key) + ":" + _canonical(value[key]) for key in sorted(value)) + "}"
    return _stable(value)


def _slug(value: Any) -> bool:
    return isinstance(value, str) and re.fullmatch(r"[a-z0-9](?:[a-z0-9-]*[a-z0-9])?", value) is not None


def _hash(value: Any) -> bool:
    return isinstance(value, str) and re.fullmatch(r"[a-f0-9]{64}", value) is not None


_ARRAY_FIELDS = (
    "workloads",
    "managed",
    "crons",
    "apps",
    "builds",
    "unchanged",
    "will_deploy",
    "unaffected",
    "skipped",
    "removed",
    "feature_hints",
    "warnings",
    "detection_warnings",
    "async_routes",
    "persisted_exclusions",
    "stale_persisted_exclusions",
    "can_apply_reasons",
)


def _normalize(value: dict[str, Any]) -> dict[str, Any]:
    return {k: ([] if v is None and k in _ARRAY_FIELDS else v) for k, v in value.items()}


def _valid_preview(value: Any, project: str) -> bool:
    return (
        isinstance(value, dict)
        and value.get("project_slug") == project
        and type(value.get("can_apply")) is bool
        and isinstance(value.get("plan_token"), str)
        and bool(value["plan_token"])
        and all(isinstance(value.get(k), list) for k in ("workloads", "managed", "crons"))
        and all(
            type(value.get(k)) is int and value[k] >= 0
            for k in ("observed_apps", "observed_crons", "limit_apps", "limit_crons")
        )
        and all(isinstance(w, dict) and isinstance(w.get("name"), str) and w["name"] for w in value["workloads"])
    )


@dataclass(frozen=True, slots=True)
class ProjectArchive:
    size: int
    sha256: str


@dataclass(frozen=True, slots=True)
class ReviewedProjectPlan:
    version: int
    base_url: str
    project_slug: str
    environment: str | None
    source: ProjectArchive
    _preview_json: str
    fingerprint: str

    @property
    def preview(self) -> PlanResponse:
        """A fresh typed copy; callers cannot mutate the reviewed intent."""
        return PlanResponse.from_dict(json.loads(self._preview_json))

    def to_dict(self) -> dict[str, Any]:
        return {
            "version": self.version,
            "base_url": self.base_url,
            "project_slug": self.project_slug,
            "environment": self.environment,
            "source": asdict(self.source),
            "preview": json.loads(self._preview_json),
            "fingerprint": self.fingerprint,
        }

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> ReviewedProjectPlan:
        try:
            if (
                set(value) != {"version", "base_url", "project_slug", "environment", "source", "preview", "fingerprint"}
                or type(value["version"]) is not int
                or value["version"] != 1
                or _base(value["base_url"]) != value["base_url"]
                or not _slug(value["project_slug"])
                or value["environment"] is not None
                and not _slug(value["environment"])
                or set(value["source"]) != {"size", "sha256"}
                or type(value["source"]["size"]) is not int
                or not 0 < value["source"]["size"] <= 2**53 - 1
                or not _hash(value["source"]["sha256"])
                or not _valid_preview(value["preview"], value["project_slug"])
                or value["preview"].get("environment") != value["environment"]
                or len(_canonical(value).encode()) > 4 * 1024 * 1024
            ):
                raise ValueError()
            rest = {k: v for k, v in value.items() if k != "fingerprint"}
            if hashlib.sha256(_canonical(rest).encode()).hexdigest() != value["fingerprint"]:
                raise ValueError()
            PlanResponse.from_dict(value["preview"])
            return cls(
                value["version"],
                value["base_url"],
                value["project_slug"],
                value["environment"],
                ProjectArchive(**value["source"]),
                _stable(value["preview"]),
                value["fingerprint"],
            )
        except (ValueError, TypeError, KeyError, AttributeError, OverflowError, RecursionError):
            raise ProjectWorkflowError("invalid_plan", "previewing") from None


@dataclass(frozen=True, slots=True)
class ProjectWorkflowProgress:
    phase: ProjectWorkflowPhase
    project_slug: str
    attempt_id: str | None
    elapsed_ms: float
    workload: str | None = None
    outcome: ProjectWorkloadStatus | None = None


@dataclass(frozen=True, kw_only=True)
class ProjectWorkflowOptions(RequestOptions):
    timeout: float
    on_progress: Callable[[ProjectWorkflowProgress], None | Awaitable[None]] | None = None

    def __post_init__(self) -> None:
        RequestOptions.__post_init__(self)
        if not 0 < self.timeout <= 2147483.647:
            raise ValueError("A positive total timeout is required")
        if self.on_progress is not None and not callable(self.on_progress):
            raise TypeError("on_progress must be callable")


@dataclass(frozen=True, kw_only=True)
class ProjectPreviewOptions(ProjectWorkflowOptions):
    preparation: SourceDeploymentPreparationOptions = field(default_factory=SourceDeploymentPreparationOptions)


@dataclass(frozen=True, kw_only=True)
class ProjectApplyOptions(ProjectWorkflowOptions):
    state_path: str | os.PathLike[str]
    approval_token: str | None = None
    initial_secrets: dict[str, dict[str, str]] | None = None
    min_delay: float = 1
    max_delay: float = 5
    max_polls: int | None = None
    stop_on_blocked: bool = True
    max_receipt_polls: int = 30

    def __post_init__(self) -> None:
        ProjectWorkflowOptions.__post_init__(self)
        if (
            not os.fspath(self.state_path)
            or type(self.max_receipt_polls) is not int
            or not 1 <= self.max_receipt_polls <= 10000
        ):
            raise ValueError("A state_path and valid receipt poll limit are required")
        DeploymentWaitOptions(
            timeout=self.timeout,
            min_delay=self.min_delay,
            max_delay=self.max_delay,
            max_polls=self.max_polls,
            stop_on_blocked=self.stop_on_blocked,
        )


@dataclass(frozen=True, slots=True)
class ProjectApplicationState:
    version: int
    base_url: str
    project_slug: str
    environment: str | None
    plan_fingerprint: str
    attempt_id: str
    stage: Literal["prepared", "applying", "accepted", "rejected", "complete"]
    rejection: ProjectWorkflowCode | None

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> ProjectApplicationState:
        try:
            result = cls(**value)
            if (
                type(result.version) is not int
                or result.version != 1
                or _base(result.base_url) != result.base_url
                or not _slug(result.project_slug)
                or result.environment is not None
                and not _slug(result.environment)
                or not _hash(result.plan_fingerprint)
                or not re.fullmatch(r"[a-f0-9]{32}", result.attempt_id)
                or result.stage not in ("prepared", "applying", "accepted", "rejected", "complete")
                or (
                    result.rejection not in ("preview_expired", "plan_stale", "permission_denied", "blocked")
                    if result.stage == "rejected"
                    else result.rejection is not None
                )
            ):
                raise ValueError()
            return result
        except (ValueError, TypeError, KeyError):
            raise ValueError("Invalid project application state") from None


def _state_path(path: str | os.PathLike[str]) -> Path:
    p = Path(path).absolute()
    return p.parent.resolve(strict=True) / p.name


def load_project_application_state(path: str | os.PathLike[str]) -> ProjectApplicationState:
    fd = os.open(_state_path(path), os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
    with os.fdopen(fd, "rb") as file:
        info = os.fstat(file.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size > 65536:
            raise ValueError("Invalid project application state")
        data = file.read(65537)
        if len(data) > 65536:
            raise ValueError("Invalid project application state")
        return ProjectApplicationState.from_dict(json.loads(data))


def _save(path: Path, state: ProjectApplicationState) -> None:
    fd, temporary = tempfile.mkstemp(prefix="." + path.name + ".", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as file:
            file.write(_stable(state.to_dict()))
            file.flush()
            os.fsync(file.fileno())
        os.replace(temporary, path)
        if os.name != "nt":
            parent = os.open(path.parent, os.O_RDONLY)
            try:
                os.fsync(parent)
            finally:
                os.close(parent)
    finally:
        Path(temporary).unlink(missing_ok=True)


@dataclass(frozen=True, slots=True)
class ProjectWorkloadOutcome:
    slug: str
    app_id: str | None
    deployment_id: str | None
    build_id: str | None
    outcome: ProjectWorkloadStatus
    code: str | None = None
    _timeline_json: str | None = None

    @property
    def timeline(self) -> ReleaseTimeline | None:
        return ReleaseTimeline.from_dict(json.loads(self._timeline_json)) if self._timeline_json else None


@dataclass(frozen=True, slots=True)
class ProjectApplicationResult:
    outcome: Literal["succeeded", "partial", "failed", "blocked"]
    attempt_id: str
    state_path: str
    _receipt_json: str
    workloads: tuple[ProjectWorkloadOutcome, ...]

    @property
    def receipt(self) -> ProjectSubmissionReceipt:
        value = json.loads(self._receipt_json)
        value["apply"] = _normalize(value["apply"])
        return ProjectSubmissionReceipt.from_dict(value)


class ProjectWorkflowError(Exception):
    def __init__(
        self,
        code: ProjectWorkflowCode,
        phase: ProjectWorkflowPhase,
        state: ProjectApplicationState | None = None,
        cause: BaseException | None = None,
    ):
        self.code, self.phase, self.state, self.cause = code, phase, state, cause
        super().__init__(f"SDK project workflow stopped: {code}")


def _refusal(error: Exception) -> ProjectWorkflowCode | None:
    status = _status(error)
    problem = getattr(error, "problem", None)
    code = problem.get("code") if isinstance(problem, dict) else getattr(problem, "code", None)
    return (
        "preview_expired"
        if status == 410
        else "plan_stale"
        if code == "plan_token_stale"
        else "permission_denied"
        if status in (401, 403)
        else "blocked"
        if status == 409
        else None
    )


class _Operation:
    def __init__(self, owner: FaaSClient, project: str, options: ProjectWorkflowOptions):
        if not _slug(project):
            raise ValueError("A project slug is required")
        self.options = options
        self.transport_owner = owned_async_transport(owner.async_httpx_client).owner
        self.client = snapshot_async_client(owner.async_httpx_client)
        self.client.follow_redirects = False
        self.owner = copy.copy(owner)
        self.owner._gen = copy.copy(owner._gen)
        self.owner._gen.set_async_httpx_client(self.client)
        self.base, self.project = _base(str(self.client.base_url)), project
        self.state: ProjectApplicationState | None = None
        self.phase: ProjectWorkflowPhase = "preparing"
        self.active, self.start = True, math.inf

    def error(self, code: ProjectWorkflowCode, cause: BaseException | None = None) -> ProjectWorkflowError:
        return ProjectWorkflowError(code, self.phase, self.state, cause)

    def check(self) -> None:
        if not self.active:
            raise asyncio.CancelledError()
        if asyncio.get_running_loop().time() - self.start >= self.options.timeout:
            raise self.error("timeout")

    def remaining(self) -> float:
        self.check()
        return max(0.000001, self.options.timeout - (asyncio.get_running_loop().time() - self.start))

    async def event(self, workload: str | None = None, outcome: ProjectWorkloadStatus | None = None) -> None:
        self.check()
        try:
            if self.options.on_progress:
                result = self.options.on_progress(
                    ProjectWorkflowProgress(
                        self.phase,
                        self.project,
                        self.state.attempt_id if self.state else None,
                        (asyncio.get_running_loop().time() - self.start) * 1000,
                        workload,
                        outcome,
                    )
                )
                if inspect.isawaitable(result):
                    await result
        except Exception as error:
            self.check()
            raise self.error("progress_failed", error) from error
        self.check()

    async def request(self, method: str, url: str, **kwargs: Any) -> dict[str, Any]:
        self.check()

        async def send() -> dict[str, Any]:
            self.check()
            response = await self.client.request(method, url, **kwargs)
            try:
                response.raise_for_status()
                result = response.json()
                if not isinstance(result, dict):
                    raise self.error("invalid_response")
                return result
            finally:
                await response.aclose()

        return await run_request(
            self.transport_owner,
            send,
            RequestOptions(
                timeout=0, retry=False if method == "POST" else self.options.retry, on_response=self.options.on_response
            ),
            0,
        )

    async def run(self, work: Callable[[], Coroutine[Any, Any, T]]) -> T:
        self.start = asyncio.get_running_loop().time()
        try:
            return await run_request(object(), work, RequestOptions(timeout=self.options.timeout), self.options.timeout)
        except RequestTimeoutError as error:
            raise self.error("timeout", error) from None
        except ProjectWorkflowError:
            raise
        except Exception as error:
            raise self.error(
                "preview_failed" if self.phase in ("preparing", "previewing") else "acceptance_unknown", error
            ) from error
        finally:
            self.active = False
            await self.client.aclose()


def preview_project(
    owner: FaaSClient,
    directory: str | os.PathLike[str],
    project_slug: str,
    *,
    options: ProjectPreviewOptions,
    environment: str | None = None,
    only: tuple[str, ...] = (),
    no_triggers: bool | None = None,
    app_slugs: dict[str, str] | None = None,
) -> Coroutine[Any, Any, ReviewedProjectPlan]:
    if (
        environment is not None
        and not _slug(environment)
        or any(not isinstance(s, str) or not s or "," in s or re.search(r"[\x00-\x1f]", s) for s in only)
        or no_triggers is not None
        and type(no_triggers) is not bool
    ):
        raise ValueError("Invalid project preview selection")
    app_slugs = dict(app_slugs) if app_slugs is not None else None
    if app_slugs is not None and any(not isinstance(k, str) or not k or not _slug(v) for k, v in app_slugs.items()):
        raise ValueError("Invalid project app names")
    options = replace(options, preparation=replace(options.preparation, exclude=tuple(options.preparation.exclude)))
    operation = _Operation(owner, project_slug, options)
    directory, only = os.path.abspath(directory), tuple(only)

    async def work() -> ReviewedProjectPlan:
        with tempfile.TemporaryDirectory(prefix="gregale-project-preview-") as temporary:
            await operation.event()

            async def progress(_: Any) -> None:
                await operation.event()

            source = await prepare_source(
                directory,
                Path(temporary) / "source.tar.gz",
                options=SourcePreparationOptions(
                    **{**vars(options.preparation), "on_progress": progress},
                    timeout=operation.remaining(),
                    source_root=".",
                ),
            )
            operation.phase = "previewing"
            await operation.event()
            data = {"project_slug": project_slug}
            if app_slugs is not None:
                data["app_slugs"] = _stable(app_slugs)
            if environment is not None:
                data["environment"] = environment
            if only:
                data["only"] = ",".join(only)
            if no_triggers is not None:
                data["no_triggers"] = str(no_triggers).lower()
            with open(source.path, "rb") as archive:
                preview = await operation.request(
                    "POST",
                    "/v1/projects/scan",
                    data=data,
                    files={"source": ("source.tar.gz", archive, "application/gzip")},
                )
            preview = _normalize(preview)
            if not _valid_preview(preview, project_slug) or preview.get("environment") != environment:
                raise operation.error("invalid_response")
            value = {
                "version": 1,
                "base_url": operation.base,
                "project_slug": project_slug,
                "environment": environment,
                "source": {"size": source.size, "sha256": source.sha256},
                "preview": preview,
            }
            value["fingerprint"] = hashlib.sha256(_canonical(value).encode()).hexdigest()
            return ReviewedProjectPlan.from_dict(value)

    return operation.run(work)


def apply_project_plan(
    owner: FaaSClient, original: ReviewedProjectPlan, *, options: ProjectApplyOptions, resume: bool = False
) -> Coroutine[Any, Any, ProjectApplicationResult]:
    plan = ReviewedProjectPlan.from_dict(original.to_dict())
    options = replace(options, initial_secrets=copy.deepcopy(options.initial_secrets))
    operation = _Operation(owner, plan.project_slug, options)

    async def work() -> ProjectApplicationResult:
        held, path = False, _state_path(options.state_path)

        def persist(state: ProjectApplicationState) -> None:
            operation.check()
            try:
                _save(path, state)
            except OSError as error:
                raise operation.error("state_failed", error) from error
            operation.state = state
            operation.check()

        try:
            if operation.base != plan.base_url:
                raise operation.error("state_mismatch")
            operation.check()
            with _LOCK:
                if str(path) in _ACTIVE:
                    raise operation.error("busy")
                _ACTIVE.add(str(path))
                held = True
            try:
                operation.state = load_project_application_state(path)
            except FileNotFoundError as error:
                if resume:
                    raise operation.error("invalid_state", error) from error
            except (ValueError, OSError) as error:
                raise operation.error("invalid_state", error) from error
            if operation.state:
                state = operation.state
                if (
                    state.base_url != plan.base_url
                    or state.project_slug != plan.project_slug
                    or state.environment != plan.environment
                    or state.plan_fingerprint != plan.fingerprint
                ):
                    raise operation.error("state_mismatch")
            else:
                persist(
                    ProjectApplicationState(
                        1,
                        plan.base_url,
                        plan.project_slug,
                        plan.environment,
                        plan.fingerprint,
                        uuid4().hex,
                        "prepared",
                        None,
                    )
                )
            assert operation.state
            if operation.state.stage == "rejected":
                assert operation.state.rejection
                raise operation.error(operation.state.rejection)
            if operation.state.stage == "prepared":
                if not plan.preview.can_apply:
                    raise operation.error("blocked")
                operation.phase = "applying"
                persist(replace(operation.state, stage="applying"))
                await operation.event()
                body = {"plan_token": plan.preview.plan_token}
                if options.approval_token:
                    body["approval_token"] = options.approval_token
                if options.initial_secrets is not None:
                    body["initial_secrets"] = options.initial_secrets  # type: ignore[assignment]
                try:
                    response = await operation.request(
                        "POST",
                        "/v1/projects/apply-plan",
                        json=body,
                        headers={
                            "Deployment-Attempt-ID": operation.state.attempt_id,
                            "Idempotency-Key": operation.state.attempt_id,
                        },
                    )
                    if (
                        response.get("submission_attempt_id") != operation.state.attempt_id
                        or response.get("project_slug") != plan.project_slug
                    ):
                        raise operation.error("acceptance_unknown")
                    persist(replace(operation.state, stage="accepted"))
                except ProjectWorkflowError:
                    raise
                except Exception as error:
                    operation.check()
                    code = _refusal(error)
                    if code:
                        persist(replace(operation.state, stage="rejected", rejection=code))
                        raise operation.error(code, error) from error
                    # A transport failure may follow acceptance. Only read the receipt.
            operation.phase = "recovering"
            receipt: dict[str, Any] = {}
            for poll in range(1, options.max_receipt_polls + 1):
                await operation.event()
                try:
                    receipt = await operation.request("GET", "/v1/project-submissions/" + operation.state.attempt_id)
                except Exception as error:
                    operation.check()
                    raise operation.error("acceptance_unknown", error) from error
                applied = receipt.get("apply")
                if (
                    receipt.get("attempt_id") != operation.state.attempt_id
                    or receipt.get("project_slug") != plan.project_slug
                    or receipt.get("environment") != plan.environment
                    or type(receipt.get("selection_complete")) is not bool
                    or receipt.get("status") not in ("processing", "complete", "rejected", "interrupted")
                    or not isinstance(applied, dict)
                    or applied.get("project_slug")
                    and applied["project_slug"] != plan.project_slug
                    or applied.get("submission_attempt_id")
                    and applied["submission_attempt_id"] != operation.state.attempt_id
                ):
                    raise operation.error("invalid_response")
                if receipt["status"] != "processing":
                    break
                if poll == options.max_receipt_polls:
                    raise operation.error("poll_limit")
                await asyncio.sleep(min(options.max_delay, options.min_delay * 2 ** min(poll - 1, 20)))
            if not receipt["selection_complete"]:
                raise operation.error("acceptance_unknown")
            persist(replace(operation.state, stage="accepted"))
            operation.phase = "observing"
            applied = receipt["apply"]
            apps, rows, unchanged = (
                (applied.get("apps") or []),
                (applied.get("builds") or []),
                set(applied.get("unchanged") or []),
            )
            seen, ids = set(), set()
            for entry in rows:
                if (
                    not isinstance(entry, dict)
                    or not _slug(entry.get("slug"))
                    or not entry.get("app_id")
                    or entry["slug"] in seen
                    or entry.get("deployment_id")
                    and entry["deployment_id"] in ids
                    or not any(a.get("slug") == entry["slug"] and a.get("id") == entry["app_id"] for a in apps)
                    or entry.get("build_id")
                    and not entry.get("deployment_id")
                ):
                    raise operation.error("invalid_response")
                seen.add(entry["slug"])
                if entry.get("deployment_id"):
                    ids.add(entry["deployment_id"])
            results = []
            for app in apps:
                if (
                    not _slug(app.get("slug"))
                    or not app.get("id")
                    or sum(a.get("slug") == app["slug"] for a in apps) != 1
                ):
                    raise operation.error("invalid_response")
                row: dict[str, Any] = next((r for r in rows if r["slug"] == app["slug"]), {})
                result = ProjectWorkloadOutcome(
                    app["slug"],
                    app["id"],
                    row.get("deployment_id"),
                    row.get("build_id"),
                    "unknown",
                    "submission_incomplete",
                )
                if row.get("error") and not row.get("deployment_id"):
                    result = replace(result, outcome="failed", code="enqueue_failed")
                elif row.get("deployment_id"):

                    async def progress(_: Any, __: Any) -> None:
                        await operation.event(workload=app["slug"])

                    try:
                        timeline = await wait_for_deployment(
                            operation.owner,
                            row["deployment_id"],
                            DeploymentWaitOptions(
                                timeout=operation.remaining(),
                                min_delay=options.min_delay,
                                max_delay=options.max_delay,
                                max_polls=options.max_polls,
                                stop_on_blocked=options.stop_on_blocked,
                                retry=options.retry,
                                on_response=options.on_response,
                                on_progress=progress,
                            ),
                        )
                        result = replace(
                            result,
                            outcome="failed" if row.get("error") else "succeeded",
                            code="post_acceptance_failed" if row.get("error") else None,
                            _timeline_json=_stable(timeline.to_dict()),
                        )
                    except WaiterError as error:
                        operation.check()
                        if error.code in ("timeout", "poll_limit"):
                            raise operation.error(error.code, error) from error
                        last = error.last_observed
                        result = replace(
                            result,
                            outcome="blocked" if error.code == "blocked" else "failed",
                            code=error.code,
                            _timeline_json=_stable(last.to_dict()) if last is not None else None,
                        )
                elif app["slug"] in unchanged:
                    result = replace(result, outcome="unchanged", code=None)
                results.append(result)
                await operation.event(app["slug"], result.outcome)
            review = plan.to_dict()["preview"]
            unchanged_review = {
                r["slug"]
                for r in [
                    *(review.get("unaffected") or []),
                    *(review.get("skipped") or []),
                    *(r for r in (review.get("will_deploy") or []) if r["action"] == "noop"),
                ]
            }
            for reviewed in [
                *(review.get("will_deploy") or []),
                *(review.get("unaffected") or []),
                *(review.get("skipped") or []),
            ]:
                if not any(r.slug == reviewed["slug"] for r in results):
                    keep = (
                        reviewed["slug"] in unchanged_review
                        or bool(reviewed.get("id"))
                        and receipt["status"] == "complete"
                    )
                    results.append(
                        ProjectWorkloadOutcome(
                            reviewed["slug"],
                            reviewed.get("id"),
                            None,
                            None,
                            "unchanged" if keep else "unknown",
                            None if keep else "submission_incomplete",
                        )
                    )
            success = any(r.outcome in ("succeeded", "unchanged") for r in results)
            bad = any(r.outcome in ("failed", "unknown") for r in results) or receipt["status"] != "complete"
            blocked = any(r.outcome == "blocked" for r in results)
            outcome: Literal["succeeded", "partial", "failed", "blocked"] = (
                ("partial" if success else "failed") if bad else "blocked" if blocked else "succeeded"
            )
            persist(replace(operation.state, stage="complete"))
            operation.phase = "complete"
            await operation.event()
            return ProjectApplicationResult(
                outcome, operation.state.attempt_id, str(path), _stable(receipt), tuple(results)
            )  # type: ignore[arg-type]
        finally:
            if held:
                with _LOCK:
                    _ACTIVE.discard(str(path))

    return operation.run(work)
