from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_status_response_verdict import DebugStatusResponseVerdict, check_debug_status_response_verdict
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_regression_item import DebugRegressionItem
    from ..models.debug_rollout_explanation import DebugRolloutExplanation
    from ..models.debug_status_deployment import DebugStatusDeployment
    from ..models.debug_status_health import DebugStatusHealth
    from ..models.debug_status_oom_kill import DebugStatusOOMKill
    from ..models.debug_telemetry_request_item import DebugTelemetryRequestItem


T = TypeVar("T", bound="DebugStatusResponse")


@_attrs_define
class DebugStatusResponse:
    """One summary of an app's health, release, regressions, and recent failures over `since`."""

    verdict: DebugStatusResponseVerdict
    headline: str
    since: str
    window_start: datetime.datetime
    window_end: datetime.datetime
    health: DebugStatusHealth
    """The serving deployments' traffic over the window."""
    deployments: list[DebugStatusDeployment]
    regressions: list[DebugRegressionItem]
    regressions_total: int
    recent_failures: list[DebugTelemetryRequestItem]
    tracing_enabled: bool
    generated_at: datetime.datetime
    rollout: DebugRolloutExplanation | Unset = UNSET
    """Why a release was rolled back or is held, with the request evidence
    behind the safe-release decision.
    """
    oom_kills: list[DebugStatusOOMKill] | Unset = UNSET
    """Out-of-memory kills of the app's instances in the window, newest first."""
    oom_kills_total: int | Unset = UNSET
    oom_fix: str | Unset = UNSET
    """The remedy for the newest out-of-memory kill, one suggestion per line."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        verdict: str = self.verdict

        headline = self.headline

        since = self.since

        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        health = self.health.to_dict()

        deployments = []
        for deployments_item_data in self.deployments:
            deployments_item = deployments_item_data.to_dict()
            deployments.append(deployments_item)

        regressions = []
        for regressions_item_data in self.regressions:
            regressions_item = regressions_item_data.to_dict()
            regressions.append(regressions_item)

        regressions_total = self.regressions_total

        recent_failures = []
        for recent_failures_item_data in self.recent_failures:
            recent_failures_item = recent_failures_item_data.to_dict()
            recent_failures.append(recent_failures_item)

        tracing_enabled = self.tracing_enabled

        generated_at = self.generated_at.isoformat()

        rollout: dict[str, Any] | Unset = UNSET
        if not isinstance(self.rollout, Unset):
            rollout = self.rollout.to_dict()

        oom_kills: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.oom_kills, Unset):
            oom_kills = []
            for oom_kills_item_data in self.oom_kills:
                oom_kills_item = oom_kills_item_data.to_dict()
                oom_kills.append(oom_kills_item)

        oom_kills_total = self.oom_kills_total

        oom_fix = self.oom_fix

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "verdict": verdict,
                "headline": headline,
                "since": since,
                "window_start": window_start,
                "window_end": window_end,
                "health": health,
                "deployments": deployments,
                "regressions": regressions,
                "regressions_total": regressions_total,
                "recent_failures": recent_failures,
                "tracing_enabled": tracing_enabled,
                "generated_at": generated_at,
            }
        )
        if rollout is not UNSET:
            field_dict["rollout"] = rollout
        if oom_kills is not UNSET:
            field_dict["oom_kills"] = oom_kills
        if oom_kills_total is not UNSET:
            field_dict["oom_kills_total"] = oom_kills_total
        if oom_fix is not UNSET:
            field_dict["oom_fix"] = oom_fix

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_regression_item import DebugRegressionItem
        from ..models.debug_rollout_explanation import DebugRolloutExplanation
        from ..models.debug_status_deployment import DebugStatusDeployment
        from ..models.debug_status_health import DebugStatusHealth
        from ..models.debug_status_oom_kill import DebugStatusOOMKill
        from ..models.debug_telemetry_request_item import DebugTelemetryRequestItem

        d = dict(src_dict)
        verdict = check_debug_status_response_verdict(d.pop("verdict"))

        headline = d.pop("headline")

        since = d.pop("since")

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        health = DebugStatusHealth.from_dict(d.pop("health"))

        deployments = []
        _deployments = d.pop("deployments")
        for deployments_item_data in _deployments:
            deployments_item = DebugStatusDeployment.from_dict(deployments_item_data)

            deployments.append(deployments_item)

        regressions = []
        _regressions = d.pop("regressions")
        for regressions_item_data in _regressions:
            regressions_item = DebugRegressionItem.from_dict(regressions_item_data)

            regressions.append(regressions_item)

        regressions_total = d.pop("regressions_total")

        recent_failures = []
        _recent_failures = d.pop("recent_failures")
        for recent_failures_item_data in _recent_failures:
            recent_failures_item = DebugTelemetryRequestItem.from_dict(recent_failures_item_data)

            recent_failures.append(recent_failures_item)

        tracing_enabled = d.pop("tracing_enabled")

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        _rollout = d.pop("rollout", UNSET)
        rollout: DebugRolloutExplanation | Unset
        if isinstance(_rollout, Unset):
            rollout = UNSET
        else:
            rollout = DebugRolloutExplanation.from_dict(_rollout)

        _oom_kills = d.pop("oom_kills", UNSET)
        oom_kills: list[DebugStatusOOMKill] | Unset = UNSET
        if _oom_kills is not UNSET:
            oom_kills = []
            for oom_kills_item_data in _oom_kills:
                oom_kills_item = DebugStatusOOMKill.from_dict(oom_kills_item_data)

                oom_kills.append(oom_kills_item)

        oom_kills_total = d.pop("oom_kills_total", UNSET)

        oom_fix = d.pop("oom_fix", UNSET)

        debug_status_response = cls(
            verdict=verdict,
            headline=headline,
            since=since,
            window_start=window_start,
            window_end=window_end,
            health=health,
            deployments=deployments,
            regressions=regressions,
            regressions_total=regressions_total,
            recent_failures=recent_failures,
            tracing_enabled=tracing_enabled,
            generated_at=generated_at,
            rollout=rollout,
            oom_kills=oom_kills,
            oom_kills_total=oom_kills_total,
            oom_fix=oom_fix,
        )

        debug_status_response.additional_properties = d
        return debug_status_response

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
