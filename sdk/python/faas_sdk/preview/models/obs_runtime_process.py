from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_process_reason import ObsRuntimeProcessReason, check_obs_runtime_process_reason
from ..models.obs_runtime_process_state import ObsRuntimeProcessState, check_obs_runtime_process_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_process_counts import ObsRuntimeProcessCounts


T = TypeVar("T", bound="ObsRuntimeProcess")


@_attrs_define
class ObsRuntimeProcess:
    """Advisory sampled process continuity at two evaluation times; an observed change does not prove a crash or count
    every restart between samples.

    """

    state: ObsRuntimeProcessState
    counts: ObsRuntimeProcessCounts
    """Sample states partition current inspected targets. Comparison counts cover fresh samples; removed targets
    belong to the prior observation and overlap no current partition."""
    incomplete: bool
    reason: ObsRuntimeProcessReason | Unset = UNSET
    comparison_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        counts = self.counts.to_dict()

        incomplete = self.incomplete

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        comparison_at: str | Unset = UNSET
        if not isinstance(self.comparison_at, Unset):
            comparison_at = self.comparison_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "counts": counts,
                "incomplete": incomplete,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if comparison_at is not UNSET:
            field_dict["comparison_at"] = comparison_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_runtime_process_counts import ObsRuntimeProcessCounts

        d = dict(src_dict)
        state = check_obs_runtime_process_state(d.pop("state"))

        counts = ObsRuntimeProcessCounts.from_dict(d.pop("counts"))

        incomplete = d.pop("incomplete")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeProcessReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_process_reason(_reason)

        _comparison_at = d.pop("comparison_at", UNSET)
        comparison_at: datetime.datetime | Unset
        if isinstance(_comparison_at, Unset):
            comparison_at = UNSET
        else:
            comparison_at = datetime.datetime.fromisoformat(_comparison_at)

        obs_runtime_process = cls(
            state=state,
            counts=counts,
            incomplete=incomplete,
            reason=reason,
            comparison_at=comparison_at,
        )

        obs_runtime_process.additional_properties = d
        return obs_runtime_process

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
