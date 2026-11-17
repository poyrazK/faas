from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_preparation_phase_name import (
    ObsRuntimePreparationPhaseName,
    check_obs_runtime_preparation_phase_name,
)

T = TypeVar("T", bound="ObsRuntimePreparationPhaseCounts")


@_attrs_define
class ObsRuntimePreparationPhaseCounts:
    """Observation scope counts for one closed phase, retained over the complete inspected set before preview clipping.
    These are not checkpoint totals or completion counters.

    """

    phase: ObsRuntimePreparationPhaseName
    """Durable preparation checkpoint phase, ordered preparing, layer_published, scan_complete. This is the last
    committed phase, including time waiting to resume, not the currently executing substep."""
    waiting_scopes: int
    empty_scopes: int
    unknown_age_scopes: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        phase: str = self.phase

        waiting_scopes = self.waiting_scopes

        empty_scopes = self.empty_scopes

        unknown_age_scopes = self.unknown_age_scopes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "phase": phase,
                "waiting_scopes": waiting_scopes,
                "empty_scopes": empty_scopes,
                "unknown_age_scopes": unknown_age_scopes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        phase = check_obs_runtime_preparation_phase_name(d.pop("phase"))

        waiting_scopes = d.pop("waiting_scopes")

        empty_scopes = d.pop("empty_scopes")

        unknown_age_scopes = d.pop("unknown_age_scopes")

        obs_runtime_preparation_phase_counts = cls(
            phase=phase,
            waiting_scopes=waiting_scopes,
            empty_scopes=empty_scopes,
            unknown_age_scopes=unknown_age_scopes,
        )

        obs_runtime_preparation_phase_counts.additional_properties = d
        return obs_runtime_preparation_phase_counts

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
