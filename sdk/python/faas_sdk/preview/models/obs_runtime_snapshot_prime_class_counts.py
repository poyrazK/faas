from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_snapshot_prime_class_name import (
    ObsRuntimeSnapshotPrimeClassName,
    check_obs_runtime_snapshot_prime_class_name,
)

T = TypeVar("T", bound="ObsRuntimeSnapshotPrimeClassCounts")


@_attrs_define
class ObsRuntimeSnapshotPrimeClassCounts:
    """Observation scope counts for one closed class across the full inspected set before clipping. These are neither
    unique attempt totals nor completion counters.

    """

    class_: ObsRuntimeSnapshotPrimeClassName
    """Closed current snapshot_prepare classification. These are durable evidence classes, not a new eligibility or
    health verdict. Unknown class counts always have unknown ages."""
    nonempty_scopes: int
    empty_scopes: int
    unknown_age_scopes: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        class_: str = self.class_

        nonempty_scopes = self.nonempty_scopes

        empty_scopes = self.empty_scopes

        unknown_age_scopes = self.unknown_age_scopes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "class": class_,
                "nonempty_scopes": nonempty_scopes,
                "empty_scopes": empty_scopes,
                "unknown_age_scopes": unknown_age_scopes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        class_ = check_obs_runtime_snapshot_prime_class_name(d.pop("class"))

        nonempty_scopes = d.pop("nonempty_scopes")

        empty_scopes = d.pop("empty_scopes")

        unknown_age_scopes = d.pop("unknown_age_scopes")

        obs_runtime_snapshot_prime_class_counts = cls(
            class_=class_,
            nonempty_scopes=nonempty_scopes,
            empty_scopes=empty_scopes,
            unknown_age_scopes=unknown_age_scopes,
        )

        obs_runtime_snapshot_prime_class_counts.additional_properties = d
        return obs_runtime_snapshot_prime_class_counts

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
