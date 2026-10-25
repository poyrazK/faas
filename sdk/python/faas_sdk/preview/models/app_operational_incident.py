from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AppOperationalIncident")


@_attrs_define
class AppOperationalIncident:
    """Metadata for the app's open production-route incident. Reading this summary never closes the incident or includes
    saved request evidence.

    """

    id: str
    deployment_id: str
    opened_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        deployment_id = self.deployment_id

        opened_at = self.opened_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "deployment_id": deployment_id,
                "opened_at": opened_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        deployment_id = d.pop("deployment_id")

        opened_at = datetime.datetime.fromisoformat(d.pop("opened_at"))

        app_operational_incident = cls(
            id=id,
            deployment_id=deployment_id,
            opened_at=opened_at,
        )

        app_operational_incident.additional_properties = d
        return app_operational_incident

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
