from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CronResponse")


@_attrs_define
class CronResponse:
    """A cron trigger: schedule (cron expression), target URL, last/next run timestamps, and enabled flag."""

    id: str
    app_id: str
    schedule: str
    path: str
    enabled: bool
    created_at: datetime.datetime
    last_fired_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        app_id = self.app_id

        schedule = self.schedule

        path = self.path

        enabled = self.enabled

        created_at = self.created_at.isoformat()

        last_fired_at: None | str | Unset
        if isinstance(self.last_fired_at, Unset):
            last_fired_at = UNSET
        elif isinstance(self.last_fired_at, datetime.datetime):
            last_fired_at = self.last_fired_at.isoformat()
        else:
            last_fired_at = self.last_fired_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "schedule": schedule,
                "path": path,
                "enabled": enabled,
                "created_at": created_at,
            }
        )
        if last_fired_at is not UNSET:
            field_dict["last_fired_at"] = last_fired_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        app_id = d.pop("app_id")

        schedule = d.pop("schedule")

        path = d.pop("path")

        enabled = d.pop("enabled")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        def _parse_last_fired_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_fired_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_fired_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        last_fired_at = _parse_last_fired_at(d.pop("last_fired_at", UNSET))

        cron_response = cls(
            id=id,
            app_id=app_id,
            schedule=schedule,
            path=path,
            enabled=enabled,
            created_at=created_at,
            last_fired_at=last_fired_at,
        )

        cron_response.additional_properties = d
        return cron_response

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
