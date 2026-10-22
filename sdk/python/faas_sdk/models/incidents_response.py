from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.incident import Incident


T = TypeVar("T", bound="IncidentsResponse")


@_attrs_define
class IncidentsResponse:
    since: datetime.datetime
    incidents: list[Incident]
    truncated: bool
    unavailable_sources: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        since = self.since.isoformat()

        incidents = []
        for incidents_item_data in self.incidents:
            incidents_item = incidents_item_data.to_dict()
            incidents.append(incidents_item)

        truncated = self.truncated

        unavailable_sources = self.unavailable_sources

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "since": since,
                "incidents": incidents,
                "truncated": truncated,
                "unavailable_sources": unavailable_sources,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.incident import Incident

        d = dict(src_dict)
        since = datetime.datetime.fromisoformat(d.pop("since"))

        incidents = []
        _incidents = d.pop("incidents")
        for incidents_item_data in _incidents:
            incidents_item = Incident.from_dict(incidents_item_data)

            incidents.append(incidents_item)

        truncated = d.pop("truncated")

        unavailable_sources = cast(list[str], d.pop("unavailable_sources"))

        incidents_response = cls(
            since=since,
            incidents=incidents,
            truncated=truncated,
            unavailable_sources=unavailable_sources,
        )

        incidents_response.additional_properties = d
        return incidents_response

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
