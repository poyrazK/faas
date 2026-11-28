from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_readiness_reason import ObsRuntimeReadinessReason, check_obs_runtime_readiness_reason
from ..models.obs_runtime_readiness_state import ObsRuntimeReadinessState, check_obs_runtime_readiness_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_readiness_counts import ObsRuntimeReadinessCounts
    from ..models.obs_runtime_readiness_reason_count import ObsRuntimeReadinessReasonCount


T = TypeVar("T", bound="ObsRuntimeReadiness")


@_attrs_define
class ObsRuntimeReadiness:
    """Advisory readiness for the same inspected targets; independent of revision matching and qualification. Omitted by
    older servers.

    """

    state: ObsRuntimeReadinessState
    counts: ObsRuntimeReadinessCounts
    """Readiness states partition the full inspected set before preview truncation."""
    reasons: list[ObsRuntimeReadinessReasonCount]
    reason: ObsRuntimeReadinessReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        counts = self.counts.to_dict()

        reasons = []
        for reasons_item_data in self.reasons:
            reasons_item = reasons_item_data.to_dict()
            reasons.append(reasons_item)

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "counts": counts,
                "reasons": reasons,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_runtime_readiness_counts import ObsRuntimeReadinessCounts
        from ..models.obs_runtime_readiness_reason_count import ObsRuntimeReadinessReasonCount

        d = dict(src_dict)
        state = check_obs_runtime_readiness_state(d.pop("state"))

        counts = ObsRuntimeReadinessCounts.from_dict(d.pop("counts"))

        reasons = []
        _reasons = d.pop("reasons")
        for reasons_item_data in _reasons:
            reasons_item = ObsRuntimeReadinessReasonCount.from_dict(reasons_item_data)

            reasons.append(reasons_item)

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeReadinessReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_readiness_reason(_reason)

        obs_runtime_readiness = cls(
            state=state,
            counts=counts,
            reasons=reasons,
            reason=reason,
        )

        obs_runtime_readiness.additional_properties = d
        return obs_runtime_readiness

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
