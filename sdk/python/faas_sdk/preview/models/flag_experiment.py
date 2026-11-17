from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.flag_experiment_metric_kind import FlagExperimentMetricKind, check_flag_experiment_metric_kind
from ..models.flag_experiment_status import FlagExperimentStatus, check_flag_experiment_status
from ..models.flag_experiment_stop_reason import FlagExperimentStopReason, check_flag_experiment_stop_reason
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.flag_experiment_breach import FlagExperimentBreach
    from ..models.flag_experiment_guardrail_settings import FlagExperimentGuardrailSettings
    from ..models.flag_experiment_weights import FlagExperimentWeights


T = TypeVar("T", bound="FlagExperiment")


@_attrs_define
class FlagExperiment:
    """A flag experiment."""

    key: str
    flag: str
    rule_id: str
    control: str
    metric_event: str
    metric_kind: FlagExperimentMetricKind
    status: FlagExperimentStatus
    mixing_lift: float
    weights: FlagExperimentWeights
    created_by: str
    created_at: datetime.datetime
    started_at: datetime.datetime | Unset = UNSET
    stopped_at: datetime.datetime | Unset = UNSET
    analyzed_until: datetime.datetime | Unset = UNSET
    """End of the request telemetry the results cover."""
    guardrails: FlagExperimentGuardrailSettings | Unset = UNSET
    """An experiment's guardrails."""
    latency_threshold_ms: int | Unset = UNSET
    """Control's p95 latency, fixed once control has enough requests."""
    stop_reason: FlagExperimentStopReason | Unset = UNSET
    breach: FlagExperimentBreach | Unset = UNSET
    """The guardrail breach that stopped the experiment."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        flag = self.flag

        rule_id = self.rule_id

        control = self.control

        metric_event = self.metric_event

        metric_kind: str = self.metric_kind

        status: str = self.status

        mixing_lift = self.mixing_lift

        weights = self.weights.to_dict()

        created_by = self.created_by

        created_at = self.created_at.isoformat()

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        stopped_at: str | Unset = UNSET
        if not isinstance(self.stopped_at, Unset):
            stopped_at = self.stopped_at.isoformat()

        analyzed_until: str | Unset = UNSET
        if not isinstance(self.analyzed_until, Unset):
            analyzed_until = self.analyzed_until.isoformat()

        guardrails: dict[str, Any] | Unset = UNSET
        if not isinstance(self.guardrails, Unset):
            guardrails = self.guardrails.to_dict()

        latency_threshold_ms = self.latency_threshold_ms

        stop_reason: str | Unset = UNSET
        if not isinstance(self.stop_reason, Unset):
            stop_reason = self.stop_reason

        breach: dict[str, Any] | Unset = UNSET
        if not isinstance(self.breach, Unset):
            breach = self.breach.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "flag": flag,
                "rule_id": rule_id,
                "control": control,
                "metric_event": metric_event,
                "metric_kind": metric_kind,
                "status": status,
                "mixing_lift": mixing_lift,
                "weights": weights,
                "created_by": created_by,
                "created_at": created_at,
            }
        )
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if stopped_at is not UNSET:
            field_dict["stopped_at"] = stopped_at
        if analyzed_until is not UNSET:
            field_dict["analyzed_until"] = analyzed_until
        if guardrails is not UNSET:
            field_dict["guardrails"] = guardrails
        if latency_threshold_ms is not UNSET:
            field_dict["latency_threshold_ms"] = latency_threshold_ms
        if stop_reason is not UNSET:
            field_dict["stop_reason"] = stop_reason
        if breach is not UNSET:
            field_dict["breach"] = breach

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_experiment_breach import FlagExperimentBreach
        from ..models.flag_experiment_guardrail_settings import FlagExperimentGuardrailSettings
        from ..models.flag_experiment_weights import FlagExperimentWeights

        d = dict(src_dict)
        key = d.pop("key")

        flag = d.pop("flag")

        rule_id = d.pop("rule_id")

        control = d.pop("control")

        metric_event = d.pop("metric_event")

        metric_kind = check_flag_experiment_metric_kind(d.pop("metric_kind"))

        status = check_flag_experiment_status(d.pop("status"))

        mixing_lift = d.pop("mixing_lift")

        weights = FlagExperimentWeights.from_dict(d.pop("weights"))

        created_by = d.pop("created_by")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _stopped_at = d.pop("stopped_at", UNSET)
        stopped_at: datetime.datetime | Unset
        if isinstance(_stopped_at, Unset):
            stopped_at = UNSET
        else:
            stopped_at = datetime.datetime.fromisoformat(_stopped_at)

        _analyzed_until = d.pop("analyzed_until", UNSET)
        analyzed_until: datetime.datetime | Unset
        if isinstance(_analyzed_until, Unset):
            analyzed_until = UNSET
        else:
            analyzed_until = datetime.datetime.fromisoformat(_analyzed_until)

        _guardrails = d.pop("guardrails", UNSET)
        guardrails: FlagExperimentGuardrailSettings | Unset
        if isinstance(_guardrails, Unset):
            guardrails = UNSET
        else:
            guardrails = FlagExperimentGuardrailSettings.from_dict(_guardrails)

        latency_threshold_ms = d.pop("latency_threshold_ms", UNSET)

        _stop_reason = d.pop("stop_reason", UNSET)
        stop_reason: FlagExperimentStopReason | Unset
        if isinstance(_stop_reason, Unset):
            stop_reason = UNSET
        else:
            stop_reason = check_flag_experiment_stop_reason(_stop_reason)

        _breach = d.pop("breach", UNSET)
        breach: FlagExperimentBreach | Unset
        if isinstance(_breach, Unset):
            breach = UNSET
        else:
            breach = FlagExperimentBreach.from_dict(_breach)

        flag_experiment = cls(
            key=key,
            flag=flag,
            rule_id=rule_id,
            control=control,
            metric_event=metric_event,
            metric_kind=metric_kind,
            status=status,
            mixing_lift=mixing_lift,
            weights=weights,
            created_by=created_by,
            created_at=created_at,
            started_at=started_at,
            stopped_at=stopped_at,
            analyzed_until=analyzed_until,
            guardrails=guardrails,
            latency_threshold_ms=latency_threshold_ms,
            stop_reason=stop_reason,
            breach=breach,
        )

        flag_experiment.additional_properties = d
        return flag_experiment

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
