from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_rollout_explanation_state import DebugRolloutExplanationState, check_debug_rollout_explanation_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_regression_item import DebugRegressionItem
    from ..models.debug_rollout_route import DebugRolloutRoute
    from ..models.debug_telemetry_request_item import DebugTelemetryRequestItem
    from ..models.rollout_decision import RolloutDecision


T = TypeVar("T", bound="DebugRolloutExplanation")


@_attrs_define
class DebugRolloutExplanation:
    """Why a release was rolled back or is held, with the request evidence
    behind the decision (ADR-127 amendment following ADR-1447).

    """

    state: DebugRolloutExplanationState
    traffic_percent: int
    headline: str
    """Deterministic one-line explanation."""
    routes: list[DebugRolloutRoute]
    examples: list[DebugTelemetryRequestItem]
    regressions: list[DebugRegressionItem]
    generated_at: datetime.datetime
    deployment_id: UUID | Unset = UNSET
    stable_deployment_id: UUID | Unset = UNSET
    decision: RolloutDecision | Unset = UNSET
    """Evidence behind one decision about a rollout (ADR-1447): why a canary stage advanced, is held, or was
    aborted. Aggregates only; no request payloads, routes, URLs or customer text."""
    aborted_reason: str | Unset = UNSET
    """Reason recorded by an abort that has no ADR-1447 decision (route monitor, alert rollback, profile gate)."""
    window_start: datetime.datetime | Unset = UNSET
    window_end: datetime.datetime | Unset = UNSET
    retention_clamped: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        traffic_percent = self.traffic_percent

        headline = self.headline

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        examples = []
        for examples_item_data in self.examples:
            examples_item = examples_item_data.to_dict()
            examples.append(examples_item)

        regressions = []
        for regressions_item_data in self.regressions:
            regressions_item = regressions_item_data.to_dict()
            regressions.append(regressions_item)

        generated_at = self.generated_at.isoformat()

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        stable_deployment_id: str | Unset = UNSET
        if not isinstance(self.stable_deployment_id, Unset):
            stable_deployment_id = str(self.stable_deployment_id)

        decision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision.to_dict()

        aborted_reason = self.aborted_reason

        window_start: str | Unset = UNSET
        if not isinstance(self.window_start, Unset):
            window_start = self.window_start.isoformat()

        window_end: str | Unset = UNSET
        if not isinstance(self.window_end, Unset):
            window_end = self.window_end.isoformat()

        retention_clamped = self.retention_clamped

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "traffic_percent": traffic_percent,
                "headline": headline,
                "routes": routes,
                "examples": examples,
                "regressions": regressions,
                "generated_at": generated_at,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if stable_deployment_id is not UNSET:
            field_dict["stable_deployment_id"] = stable_deployment_id
        if decision is not UNSET:
            field_dict["decision"] = decision
        if aborted_reason is not UNSET:
            field_dict["aborted_reason"] = aborted_reason
        if window_start is not UNSET:
            field_dict["window_start"] = window_start
        if window_end is not UNSET:
            field_dict["window_end"] = window_end
        if retention_clamped is not UNSET:
            field_dict["retention_clamped"] = retention_clamped

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_regression_item import DebugRegressionItem
        from ..models.debug_rollout_route import DebugRolloutRoute
        from ..models.debug_telemetry_request_item import DebugTelemetryRequestItem
        from ..models.rollout_decision import RolloutDecision

        d = dict(src_dict)
        state = check_debug_rollout_explanation_state(d.pop("state"))

        traffic_percent = d.pop("traffic_percent")

        headline = d.pop("headline")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = DebugRolloutRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        examples = []
        _examples = d.pop("examples")
        for examples_item_data in _examples:
            examples_item = DebugTelemetryRequestItem.from_dict(examples_item_data)

            examples.append(examples_item)

        regressions = []
        _regressions = d.pop("regressions")
        for regressions_item_data in _regressions:
            regressions_item = DebugRegressionItem.from_dict(regressions_item_data)

            regressions.append(regressions_item)

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        _stable_deployment_id = d.pop("stable_deployment_id", UNSET)
        stable_deployment_id: UUID | Unset
        if isinstance(_stable_deployment_id, Unset):
            stable_deployment_id = UNSET
        else:
            stable_deployment_id = UUID(_stable_deployment_id)

        _decision = d.pop("decision", UNSET)
        decision: RolloutDecision | Unset
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = RolloutDecision.from_dict(_decision)

        aborted_reason = d.pop("aborted_reason", UNSET)

        _window_start = d.pop("window_start", UNSET)
        window_start: datetime.datetime | Unset
        if isinstance(_window_start, Unset):
            window_start = UNSET
        else:
            window_start = datetime.datetime.fromisoformat(_window_start)

        _window_end = d.pop("window_end", UNSET)
        window_end: datetime.datetime | Unset
        if isinstance(_window_end, Unset):
            window_end = UNSET
        else:
            window_end = datetime.datetime.fromisoformat(_window_end)

        retention_clamped = d.pop("retention_clamped", UNSET)

        debug_rollout_explanation = cls(
            state=state,
            traffic_percent=traffic_percent,
            headline=headline,
            routes=routes,
            examples=examples,
            regressions=regressions,
            generated_at=generated_at,
            deployment_id=deployment_id,
            stable_deployment_id=stable_deployment_id,
            decision=decision,
            aborted_reason=aborted_reason,
            window_start=window_start,
            window_end=window_end,
            retention_clamped=retention_clamped,
        )

        debug_rollout_explanation.additional_properties = d
        return debug_rollout_explanation

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
