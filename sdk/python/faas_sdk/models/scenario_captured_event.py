from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ScenarioCapturedEvent")


@_attrs_define
class ScenarioCapturedEvent:
    """An event an enrolled app published. It never reached the account's event bus; the sealed snapshot is its only
    record. Platform-generated IDs and times do not compare (ADR-20261020090000).

    """

    id: UUID
    workload: str
    source: str
    type_: str
    data: Any
    """Published JSON event data bounded to 64 KiB."""
    schema_version: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        workload = self.workload

        source = self.source

        type_ = self.type_

        data = self.data

        schema_version = self.schema_version

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "workload": workload,
                "source": source,
                "type": type_,
                "data": data,
            }
        )
        if schema_version is not UNSET:
            field_dict["schema_version"] = schema_version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        workload = d.pop("workload")

        source = d.pop("source")

        type_ = d.pop("type")

        data = d.pop("data")

        schema_version = d.pop("schema_version", UNSET)

        scenario_captured_event = cls(
            id=id,
            workload=workload,
            source=source,
            type_=type_,
            data=data,
            schema_version=schema_version,
        )

        scenario_captured_event.additional_properties = d
        return scenario_captured_event

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
