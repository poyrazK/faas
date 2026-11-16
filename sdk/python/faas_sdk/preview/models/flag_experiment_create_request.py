from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.flag_experiment_create_request_metric_kind import (
    FlagExperimentCreateRequestMetricKind,
    check_flag_experiment_create_request_metric_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.flag_experiment_guardrails_request import FlagExperimentGuardrailsRequest


T = TypeVar("T", bound="FlagExperimentCreateRequest")


@_attrs_define
class FlagExperimentCreateRequest:
    """A flag experiment to create."""

    key: str
    flag: str
    """The variant flag."""
    rule_id: str
    """The flag's weighted rule."""
    control: str
    """The baseline variant."""
    metric_event: str
    """The metric event."""
    metric_kind: FlagExperimentCreateRequestMetricKind | Unset = UNSET
    """conversion (default) compares the share of exposed customers that sent the event; mean compares the mean
    event value per exposed customer."""
    mixing_lift: float | Unset = UNSET
    """mSPRT mixing standard deviation: an absolute conversion-rate lift (default 0.02), or a standardized effect
    size for mean metrics (default 0.1)."""
    guardrails: FlagExperimentGuardrailsRequest | Unset = UNSET
    """Changes the default guardrails; both are on unless turned off."""
    start: bool | Unset = UNSET
    """Start the experiment at once."""

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        flag = self.flag

        rule_id = self.rule_id

        control = self.control

        metric_event = self.metric_event

        metric_kind: str | Unset = UNSET
        if not isinstance(self.metric_kind, Unset):
            metric_kind = self.metric_kind

        mixing_lift = self.mixing_lift

        guardrails: dict[str, Any] | Unset = UNSET
        if not isinstance(self.guardrails, Unset):
            guardrails = self.guardrails.to_dict()

        start = self.start

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "flag": flag,
                "rule_id": rule_id,
                "control": control,
                "metric_event": metric_event,
            }
        )
        if metric_kind is not UNSET:
            field_dict["metric_kind"] = metric_kind
        if mixing_lift is not UNSET:
            field_dict["mixing_lift"] = mixing_lift
        if guardrails is not UNSET:
            field_dict["guardrails"] = guardrails
        if start is not UNSET:
            field_dict["start"] = start

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_experiment_guardrails_request import FlagExperimentGuardrailsRequest

        d = dict(src_dict)
        key = d.pop("key")

        flag = d.pop("flag")

        rule_id = d.pop("rule_id")

        control = d.pop("control")

        metric_event = d.pop("metric_event")

        _metric_kind = d.pop("metric_kind", UNSET)
        metric_kind: FlagExperimentCreateRequestMetricKind | Unset
        if isinstance(_metric_kind, Unset):
            metric_kind = UNSET
        else:
            metric_kind = check_flag_experiment_create_request_metric_kind(_metric_kind)

        mixing_lift = d.pop("mixing_lift", UNSET)

        _guardrails = d.pop("guardrails", UNSET)
        guardrails: FlagExperimentGuardrailsRequest | Unset
        if isinstance(_guardrails, Unset):
            guardrails = UNSET
        else:
            guardrails = FlagExperimentGuardrailsRequest.from_dict(_guardrails)

        start = d.pop("start", UNSET)

        flag_experiment_create_request = cls(
            key=key,
            flag=flag,
            rule_id=rule_id,
            control=control,
            metric_event=metric_event,
            metric_kind=metric_kind,
            mixing_lift=mixing_lift,
            guardrails=guardrails,
            start=start,
        )

        return flag_experiment_create_request
