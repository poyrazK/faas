from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_input_targets_reason import ObsInputTargetsReason, check_obs_input_targets_reason
from ..models.obs_input_targets_state import ObsInputTargetsState, check_obs_input_targets_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_input_target_counts import ObsInputTargetCounts


T = TypeVar("T", bound="ObsInputTargets")


@_attrs_define
class ObsInputTargets:
    """Configured scrape coverage with known failures retained independently of unknown expectations."""

    state: ObsInputTargetsState
    counts: None | ObsInputTargetCounts
    """Complete strict scrape vector counts; null when source evidence is unavailable or invalid."""
    reason: ObsInputTargetsReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.obs_input_target_counts import ObsInputTargetCounts

        state: str = self.state

        counts: dict[str, Any] | None
        if isinstance(self.counts, ObsInputTargetCounts):
            counts = self.counts.to_dict()
        else:
            counts = self.counts

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "counts": counts,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_input_target_counts import ObsInputTargetCounts

        d = dict(src_dict)
        state = check_obs_input_targets_state(d.pop("state"))

        def _parse_counts(data: object) -> None | ObsInputTargetCounts:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                counts_type_0 = ObsInputTargetCounts.from_dict(data)

                return counts_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ObsInputTargetCounts, data)

        counts = _parse_counts(d.pop("counts"))

        _reason = d.pop("reason", UNSET)
        reason: ObsInputTargetsReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_input_targets_reason(_reason)

        obs_input_targets = cls(
            state=state,
            counts=counts,
            reason=reason,
        )

        obs_input_targets.additional_properties = d
        return obs_input_targets

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
