from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_command_cron_class_name import (
    ObsRuntimeCommandCronClassName,
    check_obs_runtime_command_cron_class_name,
)
from ..models.obs_runtime_command_cron_class_state import (
    ObsRuntimeCommandCronClassState,
    check_obs_runtime_command_cron_class_state,
)
from ..models.obs_runtime_handoff_evidence_reason import (
    ObsRuntimeHandoffEvidenceReason,
    check_obs_runtime_handoff_evidence_reason,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_command_cron_class_values import ObsRuntimeCommandCronClassValues


T = TypeVar("T", bound="ObsRuntimeCommandCronClass")


@_attrs_define
class ObsRuntimeCommandCronClass:
    """One ordered schedule class. Usable values require fresh successful collection after the evidence boundary and
    matching original scrape clocks. Valid partial positives survive sibling gaps.

    """

    class_: ObsRuntimeCommandCronClassName
    """Closed schedule observation class. Disabled, suspended, account_held, not_due and unknown require all ages
    unknown. Due, overlap_due, replacement_wait, deployment_missing and exclusive_due report nominal due evidence
    without predicting committed admission outcomes."""
    state: ObsRuntimeCommandCronClassState
    reason: ObsRuntimeHandoffEvidenceReason | Unset = UNSET
    """Closed reason for incomplete or unusable handoff evidence; collection prerequisites apply to backlog
    observations."""
    observed_at: datetime.datetime | Unset = UNSET
    values: ObsRuntimeCommandCronClassValues | Unset = UNSET
    """Schedules in this ownership scope and included unknown-age subset. Known due timing is required only when
    schedules exceeds unknown_age; otherwise both timing fields are absent. Disabled, suspended, account-held, not-
    due and unknown classes have entirely unknown ages. Age starts at the first unconsumed nominal boundary using
    canonical timezone and DST rules; it is not continuous eligibility, command execution delay or a new stall
    threshold. Integer ceiling is wire safety, not an admission quota."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        class_: str = self.class_

        state: str = self.state

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        values: dict[str, Any] | Unset = UNSET
        if not isinstance(self.values, Unset):
            values = self.values.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "class": class_,
                "state": state,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if values is not UNSET:
            field_dict["values"] = values

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_runtime_command_cron_class_values import ObsRuntimeCommandCronClassValues

        d = dict(src_dict)
        class_ = check_obs_runtime_command_cron_class_name(d.pop("class"))

        state = check_obs_runtime_command_cron_class_state(d.pop("state"))

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeHandoffEvidenceReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_handoff_evidence_reason(_reason)

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        _values = d.pop("values", UNSET)
        values: ObsRuntimeCommandCronClassValues | Unset
        if isinstance(_values, Unset):
            values = UNSET
        else:
            values = ObsRuntimeCommandCronClassValues.from_dict(_values)

        obs_runtime_command_cron_class = cls(
            class_=class_,
            state=state,
            reason=reason,
            observed_at=observed_at,
            values=values,
        )

        obs_runtime_command_cron_class.additional_properties = d
        return obs_runtime_command_cron_class

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
