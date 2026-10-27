from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.rollout_decision_kind import RolloutDecisionKind, check_rollout_decision_kind
from ..models.rollout_decision_rule import RolloutDecisionRule, check_rollout_decision_rule
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.release_gate_result import ReleaseGateResult
    from ..models.rollout_decision_sample import RolloutDecisionSample
    from ..models.rollout_decision_signals import RolloutDecisionSignals


T = TypeVar("T", bound="RolloutDecision")


@_attrs_define
class RolloutDecision:
    """Evidence behind one decision about a rollout: why a canary stage advanced, is held, or was aborted. Aggregates only;
    no request payloads, routes, URLs or customer text.

    """

    kind: RolloutDecisionKind
    rule: RolloutDecisionRule
    """Closed set; the breaker rules reuse the canary_progression_circuit_breaker_total event names."""
    step: int
    """Zero-based canary step that was evaluated."""
    traffic_percent: int
    """The candidate's share during the observation window."""
    decided_at: datetime.datetime
    summary: str | Unset = UNSET
    """The circuit breaker's aggregate-only reason."""
    window_start: datetime.datetime | Unset = UNSET
    window_end: datetime.datetime | Unset = UNSET
    stable_deployment_id: UUID | Unset = UNSET
    candidate: RolloutDecisionSample | Unset = UNSET
    """One side's aggregates over the observation window. Request counts are organic requests; probe counts are
    reported separately."""
    stable: RolloutDecisionSample | Unset = UNSET
    """One side's aggregates over the observation window. Request counts are organic requests; probe counts are
    reported separately."""
    probes_included: bool | Unset = UNSET
    """The 5xx sample included synthetic probes of the app."""
    signals: RolloutDecisionSignals | Unset = UNSET
    """The candidate's OOM kills and both sides' liveness-driven restarts over the window."""
    gates: list[ReleaseGateResult] | Unset = UNSET
    """The release gates judged with this decision."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        rule: str = self.rule

        step = self.step

        traffic_percent = self.traffic_percent

        decided_at = self.decided_at.isoformat()

        summary = self.summary

        window_start: str | Unset = UNSET
        if not isinstance(self.window_start, Unset):
            window_start = self.window_start.isoformat()

        window_end: str | Unset = UNSET
        if not isinstance(self.window_end, Unset):
            window_end = self.window_end.isoformat()

        stable_deployment_id: str | Unset = UNSET
        if not isinstance(self.stable_deployment_id, Unset):
            stable_deployment_id = str(self.stable_deployment_id)

        candidate: dict[str, Any] | Unset = UNSET
        if not isinstance(self.candidate, Unset):
            candidate = self.candidate.to_dict()

        stable: dict[str, Any] | Unset = UNSET
        if not isinstance(self.stable, Unset):
            stable = self.stable.to_dict()

        probes_included = self.probes_included

        signals: dict[str, Any] | Unset = UNSET
        if not isinstance(self.signals, Unset):
            signals = self.signals.to_dict()

        gates: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.gates, Unset):
            gates = []
            for gates_item_data in self.gates:
                gates_item = gates_item_data.to_dict()
                gates.append(gates_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "rule": rule,
                "step": step,
                "traffic_percent": traffic_percent,
                "decided_at": decided_at,
            }
        )
        if summary is not UNSET:
            field_dict["summary"] = summary
        if window_start is not UNSET:
            field_dict["window_start"] = window_start
        if window_end is not UNSET:
            field_dict["window_end"] = window_end
        if stable_deployment_id is not UNSET:
            field_dict["stable_deployment_id"] = stable_deployment_id
        if candidate is not UNSET:
            field_dict["candidate"] = candidate
        if stable is not UNSET:
            field_dict["stable"] = stable
        if probes_included is not UNSET:
            field_dict["probes_included"] = probes_included
        if signals is not UNSET:
            field_dict["signals"] = signals
        if gates is not UNSET:
            field_dict["gates"] = gates

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.release_gate_result import ReleaseGateResult
        from ..models.rollout_decision_sample import RolloutDecisionSample
        from ..models.rollout_decision_signals import RolloutDecisionSignals

        d = dict(src_dict)
        kind = check_rollout_decision_kind(d.pop("kind"))

        rule = check_rollout_decision_rule(d.pop("rule"))

        step = d.pop("step")

        traffic_percent = d.pop("traffic_percent")

        decided_at = datetime.datetime.fromisoformat(d.pop("decided_at"))

        summary = d.pop("summary", UNSET)

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

        _stable_deployment_id = d.pop("stable_deployment_id", UNSET)
        stable_deployment_id: UUID | Unset
        if isinstance(_stable_deployment_id, Unset):
            stable_deployment_id = UNSET
        else:
            stable_deployment_id = UUID(_stable_deployment_id)

        _candidate = d.pop("candidate", UNSET)
        candidate: RolloutDecisionSample | Unset
        if isinstance(_candidate, Unset):
            candidate = UNSET
        else:
            candidate = RolloutDecisionSample.from_dict(_candidate)

        _stable = d.pop("stable", UNSET)
        stable: RolloutDecisionSample | Unset
        if isinstance(_stable, Unset):
            stable = UNSET
        else:
            stable = RolloutDecisionSample.from_dict(_stable)

        probes_included = d.pop("probes_included", UNSET)

        _signals = d.pop("signals", UNSET)
        signals: RolloutDecisionSignals | Unset
        if isinstance(_signals, Unset):
            signals = UNSET
        else:
            signals = RolloutDecisionSignals.from_dict(_signals)

        _gates = d.pop("gates", UNSET)
        gates: list[ReleaseGateResult] | Unset = UNSET
        if _gates is not UNSET:
            gates = []
            for gates_item_data in _gates:
                gates_item = ReleaseGateResult.from_dict(gates_item_data)

                gates.append(gates_item)

        rollout_decision = cls(
            kind=kind,
            rule=rule,
            step=step,
            traffic_percent=traffic_percent,
            decided_at=decided_at,
            summary=summary,
            window_start=window_start,
            window_end=window_end,
            stable_deployment_id=stable_deployment_id,
            candidate=candidate,
            stable=stable,
            probes_included=probes_included,
            signals=signals,
            gates=gates,
        )

        rollout_decision.additional_properties = d
        return rollout_decision

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
