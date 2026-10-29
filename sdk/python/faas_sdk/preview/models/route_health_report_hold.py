from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RouteHealthReportHold")


@_attrs_define
class RouteHealthReportHold:
    """Present on a read while the enforced guard holds this canary: when the hold began (its first blocking decision) and
    when the hold deadline aborts the canary if the rollout worker still finds it held.

    """

    since: datetime.datetime
    aborts_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        since = self.since.isoformat()

        aborts_at = self.aborts_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "since": since,
                "aborts_at": aborts_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        since = datetime.datetime.fromisoformat(d.pop("since"))

        aborts_at = datetime.datetime.fromisoformat(d.pop("aborts_at"))

        route_health_report_hold = cls(
            since=since,
            aborts_at=aborts_at,
        )

        route_health_report_hold.additional_properties = d
        return route_health_report_hold

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
