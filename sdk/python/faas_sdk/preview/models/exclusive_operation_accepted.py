from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ExclusiveOperationAccepted")


@_attrs_define
class ExclusiveOperationAccepted:
    """Receipt returned when an operation is durably accepted or joined."""

    id: UUID
    joined: bool
    """True only when an equivalent active operation was linked under join_existing."""
    status_url: str
    """Scoped status endpoint for the submitting credential."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        joined = self.joined

        status_url = self.status_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "joined": joined,
                "status_url": status_url,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        joined = d.pop("joined")

        status_url = d.pop("status_url")

        exclusive_operation_accepted = cls(
            id=id,
            joined=joined,
            status_url=status_url,
        )

        exclusive_operation_accepted.additional_properties = d
        return exclusive_operation_accepted

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
