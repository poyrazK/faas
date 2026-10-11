from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_route_target import OutboundAIRouteTarget


T = TypeVar("T", bound="OutboundAIRoute")


@_attrs_define
class OutboundAIRoute:
    """An ordered list of same-format managed AI integrations. Apps call $GREGALE_OUTBOUND_URL/r/{id}/{provider path}
    (ADR-983).

    """

    id: UUID
    name: str
    targets: list[OutboundAIRouteTarget]
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "targets": targets,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_route_target import OutboundAIRouteTarget

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = OutboundAIRouteTarget.from_dict(targets_item_data)

            targets.append(targets_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        outbound_ai_route = cls(
            id=id,
            name=name,
            targets=targets,
            created_at=created_at,
            updated_at=updated_at,
        )

        outbound_ai_route.additional_properties = d
        return outbound_ai_route

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
