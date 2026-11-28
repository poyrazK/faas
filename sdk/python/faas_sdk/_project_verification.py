"""Read-only deployment-aware project verification. Portable identity-only targets."""

from __future__ import annotations

import asyncio
import inspect
import json
import math
import re
from collections.abc import Awaitable, Callable, Coroutine
from dataclasses import asdict, dataclass, replace
from datetime import UTC, datetime
from typing import TYPE_CHECKING, Any, Literal
from urllib.parse import quote

from ._async_request import (
    RequestOptions,
    RequestTimeoutError,
    ResponseInfo,
    owned_async_transport,
    run_request,
    snapshot_async_client,
)
from ._project_workflows import ProjectApplicationResult, load_project_application_state
from ._source_upload import _base

if TYPE_CHECKING:
    from ._wrapper import FaaSClient

VerificationOutcome = Literal["verified", "failed", "blocked", "inconclusive"]
VerificationCode = Literal[
    "verified",
    "not_observed",
    "complete",
    "release_blocked",
    "release_failed",
    "release_pending",
    "deployment_identity_unavailable",
    "identity_mismatch",
    "invalid_evidence",
    "response_limit",
    "request_limit",
    "deadline",
    "evidence_unavailable",
    "stale_evidence",
    "deployment_not_serving",
    "health_failed",
    "health_unknown",
    "smoke_failed",
    "smoke_inconclusive",
    "smoke_required",
    "progress_failed",
]
CheckOutcome = Literal["verified", "failed", "inconclusive"]


def _slug(value: Any) -> bool:
    return isinstance(value, str) and re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", value) is not None


def _id(value: Any) -> bool:
    return (
        isinstance(value, str)
        and re.fullmatch(r"(?:[a-f0-9]{32}|[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12})", value) is not None
    )


@dataclass(frozen=True, slots=True)
class VerificationWorkload:
    slug: str
    app_id: str | None
    deployment_id: str | None


@dataclass(frozen=True, slots=True)
class ProjectVerificationTarget:
    version: Literal[1]
    base_url: str
    project_slug: str
    environment: str | None
    workloads: tuple[VerificationWorkload, ...]

    def to_dict(self) -> dict[str, Any]:
        result = asdict(self)
        result["workloads"] = [asdict(w) for w in self.workloads]
        return result

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> ProjectVerificationTarget:
        try:
            if (
                set(value) != {"version", "base_url", "project_slug", "environment", "workloads"}
                or type(value["version"]) is not int
                or value["version"] != 1
                or _base(value["base_url"]) != value["base_url"]
                or not _slug(value["project_slug"])
                or value["environment"] is not None
                and not _slug(value["environment"])
                or not isinstance(value["workloads"], list)
                or not 1 <= len(value["workloads"]) <= 64
            ):
                raise ValueError
            workloads = []
            for w in value["workloads"]:
                if (
                    set(w) != {"slug", "app_id", "deployment_id"}
                    or not _slug(w["slug"])
                    or w["app_id"] is not None
                    and not _id(w["app_id"])
                    or w["deployment_id"] is not None
                    and not _id(w["deployment_id"])
                ):
                    raise ValueError
                workloads.append(VerificationWorkload(**w))
            if len({w.slug for w in workloads}) != len(workloads):
                raise ValueError
            return cls(1, value["base_url"], value["project_slug"], value["environment"], tuple(workloads))
        except (ValueError, TypeError, KeyError, AttributeError):
            raise ValueError("Invalid verification target") from None


def project_verification_target(result: ProjectApplicationResult) -> ProjectVerificationTarget:
    state = load_project_application_state(result.state_path)
    receipt = result.receipt.to_dict()
    if (
        state.attempt_id != result.attempt_id
        or receipt.get("attempt_id") != state.attempt_id
        or receipt.get("project_slug") != state.project_slug
    ):
        raise ValueError("Application identity mismatch")
    return ProjectVerificationTarget.from_dict(
        {
            "version": 1,
            "base_url": state.base_url,
            "project_slug": state.project_slug,
            "environment": state.environment,
            "workloads": [
                {"slug": w.slug, "app_id": w.app_id, "deployment_id": w.deployment_id} for w in result.workloads
            ],
        }
    )


@dataclass(frozen=True, slots=True)
class VerificationCheckResult:
    outcome: CheckOutcome
    deployment_id: str | None = None


@dataclass(frozen=True, slots=True)
class VerificationCheckContext(VerificationWorkload):
    remaining: float


@dataclass(frozen=True, slots=True)
class VerificationCheck:
    name: str
    workload: str
    run: Callable[[VerificationCheckContext], VerificationCheckResult | Awaitable[VerificationCheckResult]]


@dataclass(frozen=True, slots=True)
class VerificationCheckEvidence:
    name: str
    outcome: CheckOutcome
    code: str


@dataclass(frozen=True, slots=True)
class VerificationHealth:
    status: str
    phase: str
    evaluated_at: str | None
    metrics_as_of: str | None
    fresh: bool
    serving_deployment_ids: tuple[str, ...]


@dataclass(frozen=True, slots=True)
class VerificationTimelineEvent:
    kind: str
    at: str


@dataclass(frozen=True, slots=True)
class VerificationTimeline:
    phase: str
    status: str
    traffic_percent: float
    event_count: int
    events: tuple[VerificationTimelineEvent, ...]
    truncated: bool


@dataclass(frozen=True, slots=True)
class VerificationWorkloadReport(VerificationWorkload):
    outcome: VerificationOutcome = "inconclusive"
    code: VerificationCode = "not_observed"
    polls: int = 0
    timeline: VerificationTimeline | None = None
    health: VerificationHealth | None = None
    checks: tuple[VerificationCheckEvidence, ...] = ()
    request_ids: tuple[str, ...] = ()
    diagnostics: tuple[str, ...] = ()
    diagnostics_truncated: bool = False
    next_actions: tuple[str, ...] = ("inspect_evidence", "retry_verification")


@dataclass(frozen=True, slots=True)
class ProjectVerificationReport:
    version: Literal[1]
    target: ProjectVerificationTarget
    outcome: VerificationOutcome
    code: VerificationCode
    requests: int
    workloads: tuple[VerificationWorkloadReport, ...]

    def to_dict(self) -> dict[str, Any]:
        def portable(value: Any) -> Any:
            if isinstance(value, dict):
                return {k: portable(v) for k, v in value.items()}
            if isinstance(value, (tuple, list)):
                return [portable(v) for v in value]
            return value

        result: dict[str, Any] = portable(asdict(self))
        result["target"] = self.target.to_dict()
        # Preserve explicit absent deployment identities in the common wire contract.
        for w, original in zip(result["workloads"], self.workloads, strict=True):
            w.update(app_id=original.app_id, deployment_id=original.deployment_id)
            for key in ("timeline", "health"):
                if w[key] is None:
                    del w[key]
        return result


@dataclass(frozen=True, slots=True)
class ProjectVerificationOptions:
    timeout: float
    max_polls: int = 3
    poll_delay: float = 1
    concurrency: int = 2
    max_requests: int = 256
    max_response_bytes: int = 262144
    max_evidence_age: float = 120
    checks: tuple[VerificationCheck, ...] = ()
    require_smoke_checks: bool = True
    redact: Callable[[str], str] | None = None
    on_progress: Callable[[VerificationWorkloadReport], None | Awaitable[None]] | None = None

    def __post_init__(self) -> None:
        for value, minimum, maximum in (
            (self.timeout, 0.000001, 2147483.647),
            (self.poll_delay, 0, 60),
            (self.max_evidence_age, 0.001, 3600),
        ):
            if (
                isinstance(value, bool)
                or not isinstance(value, (int, float))
                or not math.isfinite(value)
                or not minimum <= value <= maximum
            ):
                raise ValueError("Invalid verification duration")
        for value, maximum in ((self.max_polls, 1000), (self.concurrency, 8), (self.max_requests, 10000)):
            if type(value) is not int or not 1 <= value <= maximum:
                raise ValueError("Invalid verification limit")
        if type(self.max_response_bytes) is not int or not 1024 <= self.max_response_bytes <= 1048576:
            raise ValueError("Invalid response limit")
        if (
            type(self.require_smoke_checks) is not bool
            or self.redact is not None
            and not callable(self.redact)
            or self.on_progress is not None
            and not callable(self.on_progress)
            or len(self.checks) > 64
            or any(
                not isinstance(c, VerificationCheck)
                or not _slug(c.name)
                or not _slug(c.workload)
                or not callable(c.run)
                for c in self.checks
            )
            or len({c.name for c in self.checks}) != len(self.checks)
        ):
            raise ValueError("Invalid verification checks")


class _EvidenceError(Exception):
    def __init__(self, code: VerificationCode):
        super().__init__(code)
        self.code = code


def _actions(code: VerificationCode) -> tuple[str, ...]:
    return {
        "verified": (),
        "release_blocked": ("review_release_approval",),
        "release_failed": ("inspect_build_and_deployment",),
        "smoke_failed": ("inspect_service", "review_source_change"),
        "health_failed": ("inspect_service", "review_source_change"),
        "smoke_required": ("add_identity_aware_smoke_check",),
        "evidence_unavailable": ("check_permissions", "retry_verification"),
    }.get(code, ("inspect_evidence", "retry_verification"))


def _time(value: Any) -> float:
    if not isinstance(value, str) or not re.fullmatch(
        r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})", value, flags=re.ASCII
    ):
        return math.nan
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        return parsed.timestamp() if parsed.tzinfo else math.nan
    except (ValueError, TypeError, AttributeError, OverflowError):
        return math.nan


def verify_project_application(
    owner: FaaSClient,
    input: ProjectVerificationTarget | ProjectApplicationResult,
    *,
    options: ProjectVerificationOptions,
) -> Coroutine[Any, Any, ProjectVerificationReport]:
    """Capture client configuration at invocation. Asyncio cancellation stops all probes.

    Checks must be read-only/repeatable and return the deployment identity actually
    observed. No SDK credentials are passed to checks. Text is omitted by default.
    """
    options = replace(options, checks=tuple(options.checks))
    target = (
        ProjectVerificationTarget.from_dict(input.to_dict())
        if isinstance(input, ProjectVerificationTarget)
        else project_verification_target(input)
    )
    if target.base_url != _base(str(owner.async_httpx_client.base_url)):
        raise ValueError("Verification endpoint mismatch")
    if any(c.workload not in {w.slug for w in target.workloads} for c in options.checks):
        raise ValueError("Unknown check workload")
    client = snapshot_async_client(owner.async_httpx_client)
    client.follow_redirects = False
    transport_owner = owned_async_transport(owner.async_httpx_client).owner
    reports = [VerificationWorkloadReport(w.slug, w.app_id, w.deployment_id) for w in target.workloads]
    requests, active, start = 0, True, math.inf

    def guard() -> None:
        if not active:
            raise asyncio.CancelledError
        if asyncio.get_running_loop().time() - start >= options.timeout:
            raise RequestTimeoutError

    def report(code: VerificationCode) -> ProjectVerificationReport:
        outcome: VerificationOutcome = next(
            (o for o in ("failed", "blocked", "inconclusive") if any(w.outcome == o for w in reports)), "verified"
        )
        if code != "complete" and outcome == "verified":
            outcome = "inconclusive"
        return ProjectVerificationReport(1, target, outcome, code, requests, tuple(reports))

    async def read(path: str, ids: list[str]) -> dict[str, Any]:
        nonlocal requests
        guard()
        if requests >= options.max_requests:
            raise _EvidenceError("request_limit")
        requests += 1

        async def send() -> dict[str, Any]:
            async with client.stream("GET", path, headers={"Accept-Encoding": "identity"}) as response:
                response.raise_for_status()
                data = bytearray()
                async for chunk in response.aiter_bytes(chunk_size=8192):
                    guard()
                    if len(data) + len(chunk) > options.max_response_bytes:
                        raise _EvidenceError("response_limit")
                    data.extend(chunk)
                result = json.loads(data)
                if not isinstance(result, dict):
                    raise _EvidenceError("invalid_evidence")
                return result

        def observed(info: ResponseInfo) -> None:
            if info.request_id and len(ids) < 32 and info.request_id not in ids:
                ids.append(info.request_id)

        return await run_request(transport_owner, send, RequestOptions(timeout=0, retry=False, on_response=observed), 0)

    async def work() -> ProjectVerificationReport:
        nonlocal start
        start = asyncio.get_running_loop().time()
        try:
            project = await read("/v1/projects/" + quote(target.project_slug, safe=""), [])
        except _EvidenceError:
            raise
        except Exception:
            raise _EvidenceError("evidence_unavailable") from None
        if project.get("slug") != target.project_slug or not _id(project.get("id")):
            raise _EvidenceError("identity_mismatch")
        semaphore = asyncio.Semaphore(options.concurrency)

        async def worker(index: int, workload: VerificationWorkload) -> None:
            async with semaphore:
                guard()
                current = reports[index]
                ids: list[str] = []

                def update(outcome: VerificationOutcome, code: VerificationCode) -> None:
                    nonlocal current
                    current = replace(
                        current, outcome=outcome, code=code, request_ids=tuple(ids), next_actions=_actions(code)
                    )

                try:
                    if not workload.app_id or not workload.deployment_id:
                        update("inconclusive", "deployment_identity_unavailable")
                    else:
                        app = await read("/v1/apps/" + quote(workload.slug, safe=""), ids)
                        if (
                            app.get("id") != workload.app_id
                            or app.get("slug") != workload.slug
                            or app.get("project_id") != project["id"]
                        ):
                            raise _EvidenceError("identity_mismatch")
                        for poll in range(1, options.max_polls + 1):
                            guard()
                            current = replace(
                                current, polls=poll, checks=(), diagnostics=(), diagnostics_truncated=False
                            )
                            dep = await read("/v1/deployments/" + workload.deployment_id, ids)
                            if (
                                dep.get("id") != workload.deployment_id
                                or dep.get("app_id") != workload.app_id
                                or (dep.get("scope") or "default") != (target.environment or "default")
                            ):
                                raise _EvidenceError("identity_mismatch")
                            timeline = await read("/v1/deployments/" + workload.deployment_id + "/timeline", ids)
                            if (
                                timeline.get("deployment_id") != workload.deployment_id
                                or timeline.get("app") != workload.slug
                                or (timeline.get("scope") or "default") != (target.environment or "default")
                            ):
                                raise _EvidenceError("identity_mismatch")
                            phase, status, traffic, events = (
                                timeline.get(k) for k in ("phase", "status", "traffic_percent", "events")
                            )
                            if (
                                not isinstance(phase, str)
                                or not re.fullmatch(r"[a-z_][a-z0-9_-]{0,63}", phase)
                                or not isinstance(status, str)
                                or not re.fullmatch(r"[a-z_][a-z0-9_-]{0,63}", status)
                                or not isinstance(traffic, (int, float))
                                or isinstance(traffic, bool)
                                or not math.isfinite(traffic)
                                or not 0 <= traffic <= 100
                                or not isinstance(events, list)
                            ):
                                raise _EvidenceError("invalid_evidence")
                            current = replace(
                                current,
                                timeline=VerificationTimeline(
                                    phase,
                                    status,
                                    traffic,
                                    len(events),
                                    tuple(
                                        VerificationTimelineEvent(
                                            e["kind"],
                                            datetime.fromtimestamp(_time(e.get("at")), UTC)
                                            .isoformat(timespec="milliseconds")
                                            .replace("+00:00", "Z"),
                                        )
                                        for e in events[-20:]
                                        if isinstance(e, dict)
                                        and isinstance(e.get("kind"), str)
                                        and re.fullmatch(r"[a-z_][a-z0-9_-]{0,63}", e["kind"])
                                        and math.isfinite(_time(e.get("at")))
                                    ),
                                    timeline.get("truncated") is True or len(events) > 20,
                                ),
                            )
                            diagnostics, truncated = [], False
                            if options.redact:
                                logs = dep.get("error_relevant_logs")
                                raw_texts = [timeline.get("reason"), dep.get("error_why"), dep.get("error_fix")]
                                if isinstance(logs, list):
                                    raw_texts.extend(log.get("message") for log in logs if isinstance(log, dict))
                                texts = [t for t in raw_texts if isinstance(t, str)]
                                truncated = len(texts) > 20
                                for text in texts[:20]:
                                    if len(text) > 4096:
                                        truncated = True
                                        continue
                                    try:
                                        safe = options.redact(text)
                                        if isinstance(safe, str):
                                            diagnostics.append(safe[:512])
                                            truncated |= len(text) > 4096 or len(safe) > 512
                                    except Exception:
                                        truncated = True
                            current = replace(current, diagnostics=tuple(diagnostics), diagnostics_truncated=truncated)
                            if phase in ("failed", "cancelled", "rolled_back"):
                                update("failed", "release_failed")
                            elif phase in ("held", "awaiting_approval"):
                                update("blocked", "release_blocked")
                            elif (
                                phase != "released" or status != "live" or dep.get("status") != "live" or traffic != 100
                            ):
                                update("inconclusive", "release_pending")
                            else:
                                suffix = (
                                    "?environment=" + quote(target.environment, safe="") if target.environment else ""
                                )
                                health = await read(
                                    "/v1/apps/" + quote(workload.slug, safe="") + "/health" + suffix, ids
                                )
                                if health.get("app_id") != workload.app_id or health.get("scope") != (
                                    target.environment or "default"
                                ):
                                    raise _EvidenceError("identity_mismatch")
                                now = datetime.now(UTC).timestamp()
                                evaluated, sampled, ttl = (
                                    _time(health.get("evaluated_at")),
                                    _time(health.get("metrics_as_of")),
                                    health.get("valid_for_seconds"),
                                )
                                fresh = (
                                    math.isfinite(evaluated)
                                    and math.isfinite(sampled)
                                    and sampled <= evaluated <= now
                                    and now - sampled <= options.max_evidence_age
                                    and isinstance(ttl, (int, float))
                                    and not isinstance(ttl, bool)
                                    and math.isfinite(ttl)
                                    and ttl > 0
                                    and now - evaluated < min(options.max_evidence_age, ttl)
                                )
                                matches = health.get("serving_deployment_ids") == [workload.deployment_id]
                                is_worker = app.get("workload_class") in ("worker", "job")
                                unsupported = is_worker and health.get("phase") == "unsupported"
                                serving = health.get("serving_deployment_ids")
                                if (
                                    health.get("status") not in ("healthy", "degraded", "unhealthy", "unknown")
                                    or health.get("phase")
                                    not in (
                                        "serving",
                                        "idle",
                                        "starting",
                                        "deploying",
                                        "stopped",
                                        "not_deployed",
                                        "maintenance",
                                        "unsupported",
                                        "unknown",
                                    )
                                    or not isinstance(serving, list)
                                    or len(serving) > 64
                                    or not all(_id(v) for v in serving)
                                ):
                                    raise _EvidenceError("invalid_evidence")

                                def stamp(t: float) -> str | None:
                                    if not math.isfinite(t):
                                        return None
                                    return (
                                        datetime.fromtimestamp(t, UTC)
                                        .isoformat(timespec="milliseconds")
                                        .replace("+00:00", "Z")
                                    )

                                current = replace(
                                    current,
                                    health=VerificationHealth(
                                        health["status"],
                                        health["phase"],
                                        stamp(evaluated),
                                        stamp(sampled),
                                        fresh,
                                        tuple(serving),
                                    ),
                                )
                                if not unsupported and (not fresh or not matches):
                                    update("inconclusive", "stale_evidence" if not fresh else "deployment_not_serving")
                                elif not unsupported and health.get("status") in ("unhealthy", "degraded"):
                                    update("failed", "health_failed")
                                elif not unsupported and (
                                    health.get("status") != "healthy" or health.get("phase") != "serving"
                                ):
                                    update("inconclusive", "health_unknown")
                                else:
                                    evidence = []
                                    for check in (c for c in options.checks if c.workload == workload.slug):
                                        try:
                                            result = check.run(
                                                VerificationCheckContext(
                                                    workload.slug,
                                                    workload.app_id,
                                                    workload.deployment_id,
                                                    max(
                                                        0, options.timeout - (asyncio.get_running_loop().time() - start)
                                                    ),
                                                )
                                            )
                                            if inspect.isawaitable(result):
                                                result = await result
                                        except Exception:
                                            guard()
                                            evidence.append(
                                                VerificationCheckEvidence(
                                                    check.name, "inconclusive", "check_unavailable"
                                                )
                                            )
                                            continue
                                        guard()
                                        valid = isinstance(result, VerificationCheckResult) and result.outcome in (
                                            "verified",
                                            "failed",
                                            "inconclusive",
                                        )
                                        attributed = valid and result.deployment_id == workload.deployment_id
                                        evidence.append(
                                            VerificationCheckEvidence(
                                                check.name,
                                                result.outcome
                                                if valid and (attributed or result.outcome == "inconclusive")
                                                else "inconclusive",
                                                "check_observed" if attributed else "check_identity_unconfirmed",
                                            )
                                        )
                                    current = replace(current, checks=tuple(evidence))
                                    if any(e.outcome == "failed" for e in evidence):
                                        update("failed", "smoke_failed")
                                    elif any(e.outcome == "inconclusive" for e in evidence):
                                        update("inconclusive", "smoke_inconclusive")
                                    elif not evidence and (options.require_smoke_checks or is_worker or unsupported):
                                        update("inconclusive", "smoke_required")
                                    else:
                                        update("verified", "verified")
                            guard()
                            reports[index] = current
                            if options.on_progress:
                                try:
                                    progress = options.on_progress(current)
                                    if inspect.isawaitable(progress):
                                        await progress
                                except Exception:
                                    guard()
                                    raise _EvidenceError("progress_failed") from None
                            if current.outcome != "inconclusive" or poll == options.max_polls:
                                break
                            await asyncio.sleep(options.poll_delay)
                except _EvidenceError as error:
                    guard()
                    update("inconclusive", error.code)
                except Exception:
                    guard()
                    update("inconclusive", "evidence_unavailable")
                guard()
                reports[index] = current

        tasks = [asyncio.create_task(worker(i, w)) for i, w in enumerate(target.workloads)]
        try:
            await asyncio.gather(*tasks)
        finally:
            for task in tasks:
                if not task.done():
                    task.cancel()
        return report("complete")

    async def run() -> ProjectVerificationReport:
        nonlocal active, reports
        try:
            return await run_request(object(), work, RequestOptions(timeout=options.timeout), options.timeout)
        except (RequestTimeoutError, _EvidenceError) as error:
            code: VerificationCode = "deadline" if isinstance(error, RequestTimeoutError) else error.code
            reports = [
                replace(w, code=code, next_actions=_actions(code)) if w.outcome == "inconclusive" else w
                for w in reports
            ]
            return report(code)
        finally:
            active = False
            await client.aclose()

    return run()
