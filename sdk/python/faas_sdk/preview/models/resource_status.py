from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.resource_status_kind import ResourceStatusKind, check_resource_status_kind

if TYPE_CHECKING:
    from ..models.condition import Condition


T = TypeVar("T", bound="ResourceStatus")


@_attrs_define
class ResourceStatus:
    """A resource's status subresource: conditions derived when read, never stored."""

    kind: ResourceStatusKind
    name: str
    """The app slug or deployment ID."""
    observed_at: datetime.datetime
    """When the evidence behind the conditions was read."""
    conditions: list[Condition]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        name = self.name

        observed_at = self.observed_at.isoformat()

        conditions = []
        for conditions_item_data in self.conditions:
            conditions_item = conditions_item_data.to_dict()
            conditions.append(conditions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "name": name,
                "observed_at": observed_at,
                "conditions": conditions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.condition import Condition

        d = dict(src_dict)
        kind = check_resource_status_kind(d.pop("kind"))

        name = d.pop("name")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        conditions = []
        _conditions = d.pop("conditions")
        for conditions_item_data in _conditions:
            conditions_item = Condition.from_dict(conditions_item_data)

            conditions.append(conditions_item)

        resource_status = cls(
            kind=kind,
            name=name,
            observed_at=observed_at,
            conditions=conditions,
        )

        resource_status.additional_properties = d
        return resource_status

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
