"""Typed fixed input and response validation for native workflow schedules."""

from __future__ import annotations

import copy
import json
import re
from dataclasses import asdict, dataclass
from typing import Any, Generic, Literal, TypeVar, cast
from zoneinfo import ZoneInfo

from ..job_runtime import JobContract, job_value_hash
from .event_outbox_state import _instant
from .event_pipeline_state import event_pipeline_scope
from .pipeline_state import PipelineScope, _uuid, pipeline_name

I = TypeVar("I")
O = TypeVar("O")


class PipelineScheduleError(RuntimeError):
    def __init__(self, code: str, occurrence_ids: tuple[str, ...] = ()):
        self.code, self.occurrence_ids = code, occurrence_ids
        super().__init__("pipeline schedule: " + code)


def integer(value: Any, low: int, high: int) -> int:
    if type(value) is not int or not low <= value <= high:
        raise PipelineScheduleError("invalid_input")
    return value


def instant(value: Any) -> int:
    try:
        return _instant(value)
    except Exception:
        raise PipelineScheduleError("invalid_response") from None


def _cron(value: Any) -> str:
    if not isinstance(value, str) or len(value) > 256:
        raise PipelineScheduleError("invalid_input")
    fields = value.split()
    if len(fields) != 5 or any(re.fullmatch(r"[A-Za-z0-9*,/\-?#]+", field) is None for field in fields):
        raise PipelineScheduleError("invalid_input")
    return " ".join(fields)


def zone(value: Any) -> str:
    try:
        if not isinstance(value, str) or not value or len(value) > 128:
            raise ValueError
        ZoneInfo(value)
        return value
    except Exception:
        raise PipelineScheduleError("invalid_input") from None


def minutes(value: Any) -> float:
    if not isinstance(value, str) or re.fullmatch(r"(?:\d+(?:\.\d+)?[hms])+", value) is None:
        raise PipelineScheduleError("invalid_response")
    seconds = sum(float(n) * {"h": 3600, "m": 60, "s": 1}[u] for n, u in re.findall(r"(\d+(?:\.\d+)?)([hms])", value))
    if not seconds.is_integer() or not 60 <= seconds <= 86400:
        raise PipelineScheduleError("invalid_response")
    return seconds / 60


def calendar(t: Any) -> dict[str, Any]:
    if (
        not isinstance(t, dict)
        or t.get("type") != "schedule"
        or t.get("tenant_configurable", False) is not False
        or t.get("source")
        or t.get("event_type")
        or t.get("filter")
    ):
        raise PipelineScheduleError("definition_changed")
    policy, overlap, enabled = t.get("catch_up", "skip"), t.get("overlap", "skip"), t.get("enabled", True)
    if (
        policy not in ("skip", "latest")
        or overlap not in ("skip", "allow")
        or type(enabled) is not bool
        or policy == "skip"
        and "catch_up_window" in t
    ):
        raise PipelineScheduleError("invalid_input")
    return dict(
        schedule=_cron(t.get("schedule")),
        timezone=zone(t.get("timezone", "UTC")),
        overlap=overlap,
        enabled=enabled,
        catch_up=policy,
        **({"window_minutes": minutes(t.get("catch_up_window", "60m"))} if policy == "latest" else {}),
    )


class PipelineSchedule(Generic[I, O]):
    def __init__(
        self,
        contract: JobContract[I, O],
        value: I,
        *,
        scope: PipelineScope,
        owner_id: str,
        cron: str,
        timezone: str = "UTC",
        overlap: str = "skip",
        catch_up: str = "skip",
        catch_up_window_minutes: int | None = None,
        enabled: bool = True,
        environment: str = "default",
    ):
        try:
            self._scope = event_pipeline_scope(scope)
            if (
                not pipeline_name(owner_id)
                or environment != "default"
                or catch_up == "skip"
                and catch_up_window_minutes is not None
            ):
                raise ValueError
            self._contract = JobContract[I, O](copy.deepcopy(contract.spec))
            native: dict[str, Any] = dict(
                type="schedule",
                schedule=_cron(cron),
                timezone=zone(timezone),
                overlap=overlap,
                catch_up=catch_up,
                enabled=enabled,
            )
            if catch_up == "latest":
                native["catch_up_window"] = (
                    str(integer(catch_up_window_minutes if catch_up_window_minutes is not None else 60, 1, 1440)) + "m"
                )
            normalized = calendar(native)
            bare = dict(
                version=1,
                scope=self._scope.to_dict(),
                owner_id=owner_id,
                contract_sha256=self._contract.sha256,
                input=self._contract.input(value),
            )
            self._identity = job_value_hash(
                ["gregale.pipeline-schedule.v1", self._contract.spec["name"], bare, normalized]
            )
            self._trigger = native | {
                "input": bare | {"trigger": dict(version=1, kind="schedule", identity=self._identity)}
            }
            if len(json.dumps(self._trigger, ensure_ascii=False).encode()) > 1048576:
                raise ValueError
            self._owner_id = owner_id
        except Exception:
            raise PipelineScheduleError("invalid_input") from None

    @property
    def contract(self) -> JobContract[I, O]:
        return self._contract

    @property
    def scope(self) -> PipelineScope:
        return self._scope

    @property
    def owner_id(self) -> str:
        return self._owner_id

    @property
    def identity(self) -> str:
        return self._identity

    @property
    def trigger(self) -> dict[str, Any]:
        return copy.deepcopy(self._trigger)


class _Metadata:
    def to_dict(self) -> dict[str, Any]:
        def clean(v: Any) -> Any:
            if isinstance(v, dict):
                return {k: clean(x) for k, x in v.items() if x is not None}
            if isinstance(v, (tuple, list)):
                return [clean(x) for x in v]
            return v

        return cast(dict[str, Any], clean(asdict(cast(Any, self))))


@dataclass(frozen=True, kw_only=True)
class PipelineScheduleInfo(_Metadata):
    workflow_name: str
    deployment_id: str
    schedule: str
    timezone: str
    overlap: Literal["skip", "allow"]
    enabled: bool
    catch_up: Literal["skip", "latest"]
    catch_up_window: str | None = None
    next_fire_at: str | None = None
    last_evaluated_at: str | None = None
    last_scheduled_for: str | None = None
    last_status: Literal["armed", "started", "skipped_overlap", "skipped_quota"] | None = None
    last_run_id: str | None = None


@dataclass(frozen=True, kw_only=True)
class PipelineScheduleOccurrence(_Metadata):
    id: str
    app_id: str
    workflow_name: str
    deployment_id: str
    scheduled_for: str
    evaluated_at: str
    status: Literal["started", "skipped_overlap", "skipped_quota"]
    run_id: str | None = None
    replay_run_id: str | None = None
    replayed_at: str | None = None


@dataclass(frozen=True, kw_only=True)
class PipelineScheduleFire(_Metadata):
    scheduled_for: str
    local_time: str
    dst_adjustment: Literal["shifted_forward"] | None = None


@dataclass(frozen=True, kw_only=True)
class PipelineScheduleDST(_Metadata):
    mode: Literal["fixed_wall_time", "interval"]
    spring_gap: Literal["shift_to_first_valid_minute", "follow_cron_interval"]
    fall_fold: Literal["run_once_at_first_occurrence", "repeat_as_clock_falls_back"]


@dataclass(frozen=True, kw_only=True)
class PipelineScheduleCatchUp(_Metadata):
    policy: Literal["skip", "latest"]
    outcome: Literal[
        "disabled",
        "first_evaluation_arms",
        "no_new_evaluation",
        "run_current_fire",
        "coalesce_latest",
        "skip_missed",
        "outside_catch_up_window",
        "no_due_occurrence",
    ]
    eligible_occurrences: int
    coalesced_occurrences: int
    missed_occurrences_not_recovered: bool
    missed_outside_window: bool
    window: str | None = None
    since: str | None = None
    selected: PipelineScheduleFire | None = None


@dataclass(frozen=True, kw_only=True)
class PipelineSchedulePreview(_Metadata):
    workflow_name: str
    deployment_id: str
    schedule: str
    timezone: str
    overlap: Literal["skip", "allow"]
    enabled: bool
    observed_at: str
    evaluation_at: str
    dst_behavior: PipelineScheduleDST
    catch_up: PipelineScheduleCatchUp
    upcoming: tuple[PipelineScheduleFire, ...]

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> PipelineSchedulePreview:
        c = dict(value["catch_up"])
        if "selected" in c:
            c["selected"] = PipelineScheduleFire(**c["selected"])
        return cls(
            **(
                value
                | dict(
                    dst_behavior=PipelineScheduleDST(**value["dst_behavior"]),
                    catch_up=PipelineScheduleCatchUp(**c),
                    upcoming=tuple(PipelineScheduleFire(**f) for f in value["upcoming"]),
                )
            )
        )


@dataclass(frozen=True, kw_only=True)
class PipelineScheduleReplayOutcome(_Metadata):
    occurrence_id: str
    workflow_name: str
    scheduled_for: str
    outcome: Literal[
        "eligible",
        "replayed",
        "already_replayed",
        "occurrence_not_found",
        "not_skipped",
        "history_not_replayable",
        "deployment_changed",
        "definition_changed",
        "schedule_disabled",
        "overlap_active",
        "quota_full",
        "tenant_unavailable",
        "target_unavailable",
        "plan_unavailable",
    ]
    replay_run_id: str | None = None


@dataclass(frozen=True, kw_only=True)
class PipelineScheduleHistory(_Metadata):
    occurrences: tuple[PipelineScheduleOccurrence, ...]
    next_cursor: str | None = None


@dataclass(frozen=True, kw_only=True)
class PipelineScheduleInspection(_Metadata):
    runtime_enabled: bool
    schedule: PipelineScheduleInfo
    unavailable_reason: str | None = None


def metadata(raw: Any, s: PipelineSchedule[Any, Any], deployment: str) -> dict[str, Any]:
    if not isinstance(raw, dict) or raw.get("workflow_name") != s.contract.spec["name"]:
        raise PipelineScheduleError("not_found")
    if raw.get("deployment_id") != deployment:
        raise PipelineScheduleError("deployment_changed")
    actual = calendar({"type": "schedule", **raw, "catch_up": raw.get("catch_up", s.trigger["catch_up"])})
    if job_value_hash(actual) != job_value_hash(calendar(s.trigger)):
        raise PipelineScheduleError("definition_changed")
    out = {k: actual[k] for k in ("schedule", "timezone", "overlap", "enabled", "catch_up")} | dict(
        workflow_name=raw["workflow_name"], deployment_id=deployment
    )
    if "catch_up_window" in raw:
        out["catch_up_window"] = raw["catch_up_window"]
    for key in ("next_fire_at", "last_evaluated_at", "last_scheduled_for"):
        if key in raw:
            instant(raw[key])
            out[key] = raw[key]
    if "last_status" in raw:
        if raw["last_status"] not in ("armed", "started", "skipped_overlap", "skipped_quota"):
            raise PipelineScheduleError("invalid_response")
        out["last_status"] = raw["last_status"]
    if "last_run_id" in raw:
        if not _uuid(raw["last_run_id"]):
            raise PipelineScheduleError("invalid_response")
        out["last_run_id"] = raw["last_run_id"]
    return out


def occurrence(r: Any, app: str) -> dict[str, Any]:
    if (
        not isinstance(r, dict)
        or not _uuid(r.get("id"))
        or r.get("app_id") != app
        or not _uuid(r.get("deployment_id"))
        or not isinstance(r.get("workflow_name"), str)
        or r.get("status") not in ("started", "skipped_overlap", "skipped_quota")
    ):
        raise PipelineScheduleError("invalid_response")
    out = {
        k: r[k] for k in ("id", "app_id", "deployment_id", "workflow_name", "status", "scheduled_for", "evaluated_at")
    }
    instant(r["scheduled_for"])
    instant(r["evaluated_at"])
    for key in ("platform_tenant_id", "run_id", "replay_run_id"):
        if r.get(key) is not None:
            if not _uuid(r[key]):
                raise PipelineScheduleError("invalid_response")
            out[key] = r[key]
    if r["status"] == "started" and "replay_run_id" in out or r["status"] != "started" and "run_id" in out:
        raise PipelineScheduleError("invalid_response")
    if "replayed_at" in r:
        instant(r["replayed_at"])
        out["replayed_at"] = r["replayed_at"]
    return out


def preview(r: Any, s: PipelineSchedule[Any, Any], deployment: str, count: int) -> dict[str, Any]:
    c = r.get("catch_up", {})
    raw = r | {"catch_up": c.get("policy")}
    if "window" in c:
        raw["catch_up_window"] = c["window"]
    metadata(raw, s, deployment)
    instant(r["observed_at"])
    instant(r["evaluation_at"])
    d = r.get("dst_behavior", {})
    if (
        d.get("mode") not in ("fixed_wall_time", "interval")
        or d.get("spring_gap") not in ("shift_to_first_valid_minute", "follow_cron_interval")
        or d.get("fall_fold") not in ("run_once_at_first_occurrence", "repeat_as_clock_falls_back")
        or c.get("outcome")
        not in (
            "disabled",
            "first_evaluation_arms",
            "no_new_evaluation",
            "run_current_fire",
            "coalesce_latest",
            "skip_missed",
            "outside_catch_up_window",
            "no_due_occurrence",
        )
        or type(c.get("missed_occurrences_not_recovered")) is not bool
        or type(c.get("missed_outside_window")) is not bool
    ):
        raise PipelineScheduleError("invalid_response")

    def fire(f: Any) -> dict[str, Any]:
        if (
            instant(f.get("scheduled_for")) != instant(f.get("local_time"))
            or "dst_adjustment" in f
            and f["dst_adjustment"] != "shifted_forward"
        ):
            raise PipelineScheduleError("invalid_response")
        return {k: f[k] for k in ("scheduled_for", "local_time", "dst_adjustment") if k in f}

    if not isinstance(r.get("upcoming"), list) or len(r["upcoming"]) > count:
        raise PipelineScheduleError("invalid_response")
    last = instant(r["evaluation_at"])
    for f in r["upcoming"]:
        current = instant(f["scheduled_for"])
        if current <= last:
            raise PipelineScheduleError("invalid_response")
        last = current
    catchup = {k: c[k] for k in ("policy", "outcome", "missed_occurrences_not_recovered", "missed_outside_window")}
    for key in ("eligible_occurrences", "coalesced_occurrences"):
        catchup[key] = integer(c[key], 0, 2147483647)
    if "window" in c:
        catchup["window"] = c["window"]
    if "since" in c:
        instant(c["since"])
        catchup["since"] = c["since"]
    if "selected" in c:
        if instant(c["selected"]["scheduled_for"]) > instant(r["evaluation_at"]):
            raise PipelineScheduleError("invalid_response")
        catchup["selected"] = fire(c["selected"])
    return {
        k: r[k]
        for k in (
            "workflow_name",
            "deployment_id",
            "schedule",
            "timezone",
            "overlap",
            "enabled",
            "observed_at",
            "evaluation_at",
        )
    } | dict(
        dst_behavior={k: d[k] for k in ("mode", "spring_gap", "fall_fold")},
        upcoming=[fire(f) for f in r["upcoming"]],
        catch_up=catchup,
    )


def replay_outcomes(value: Any, selected: list[dict[str, Any]], is_preview: bool) -> list[dict[str, Any]]:
    rows = value.get("outcomes")
    if not isinstance(rows, list) or len(rows) != len(selected):
        raise PipelineScheduleError("invalid_response")
    out = []
    seen = set()
    last = None
    for r in rows:
        old = next((v for v in selected if v["id"] == r.get("occurrence_id")), None)
        if (
            old is None
            or r["occurrence_id"] in seen
            or r.get("platform_tenant_id") is not None
            or "workflow_name" in r
            and r["workflow_name"] != old["workflow_name"]
            or "scheduled_for" in r
            and instant(r["scheduled_for"]) != instant(old["scheduled_for"])
            or r.get("outcome")
            not in (
                "eligible",
                "replayed",
                "already_replayed",
                "occurrence_not_found",
                "not_skipped",
                "history_not_replayable",
                "deployment_changed",
                "definition_changed",
                "schedule_disabled",
                "overlap_active",
                "quota_full",
                "tenant_unavailable",
                "target_unavailable",
                "plan_unavailable",
            )
            or is_preview
            and r["outcome"] == "replayed"
            or not is_preview
            and r["outcome"] == "eligible"
        ):
            raise PipelineScheduleError("invalid_response")
        seen.add(r["occurrence_id"])
        current = instant(old["scheduled_for"])
        if last is not None and current < last:
            raise PipelineScheduleError("invalid_response")
        last = current
        if (r["outcome"] in ("replayed", "already_replayed")) != _uuid(r.get("replay_run_id")):
            raise PipelineScheduleError("invalid_response")
        out.append(
            dict(
                occurrence_id=old["id"],
                workflow_name=old["workflow_name"],
                scheduled_for=old["scheduled_for"],
                outcome=r["outcome"],
                **({"replay_run_id": r["replay_run_id"]} if r.get("replay_run_id") else {}),
            )
        )
    return out


def capture_schedule(s: PipelineSchedule[I, O]) -> PipelineSchedule[I, O]:
    try:
        trigger = s.trigger
        c = calendar(trigger)
        window = c.get("window_minutes")
        captured = PipelineSchedule(
            s.contract,
            trigger["input"]["input"],
            scope=s.scope,
            owner_id=s.owner_id,
            cron=c["schedule"],
            timezone=c["timezone"],
            overlap=c["overlap"],
            enabled=c["enabled"],
            catch_up=c["catch_up"],
            catch_up_window_minutes=int(window) if window is not None and window == int(window) else window,
        )
        if captured.identity != s.identity or job_value_hash(captured.trigger["input"]) != job_value_hash(
            trigger["input"]
        ):
            raise ValueError
        return captured
    except Exception:
        raise PipelineScheduleError("invalid_input") from None
