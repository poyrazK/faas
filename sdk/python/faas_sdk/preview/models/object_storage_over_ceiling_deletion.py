from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObjectStorageOverCeilingDeletion")


@_attrs_define
class ObjectStorageOverCeilingDeletion:
    """Present while the account stores more than its object-storage ceiling. Unless the account gets back under the
    ceiling first, Gregale deletes the excess on deletion_scheduled_at.

    """

    over_ceiling_since: datetime.datetime
    deletion_scheduled_at: datetime.datetime
    excess_bytes: int
    """Bytes stored above the ceiling at the last check."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        over_ceiling_since = self.over_ceiling_since.isoformat()

        deletion_scheduled_at = self.deletion_scheduled_at.isoformat()

        excess_bytes = self.excess_bytes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "over_ceiling_since": over_ceiling_since,
                "deletion_scheduled_at": deletion_scheduled_at,
                "excess_bytes": excess_bytes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        over_ceiling_since = datetime.datetime.fromisoformat(d.pop("over_ceiling_since"))

        deletion_scheduled_at = datetime.datetime.fromisoformat(d.pop("deletion_scheduled_at"))

        excess_bytes = d.pop("excess_bytes")

        object_storage_over_ceiling_deletion = cls(
            over_ceiling_since=over_ceiling_since,
            deletion_scheduled_at=deletion_scheduled_at,
            excess_bytes=excess_bytes,
        )

        object_storage_over_ceiling_deletion.additional_properties = d
        return object_storage_over_ceiling_deletion

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
