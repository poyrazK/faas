from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.cron_response_suspended_reason import CronResponseSuspendedReason, check_cron_response_suspended_reason
from ..types import UNSET, Unset

T = TypeVar("T", bound="CronResponse")


@_attrs_define
class CronResponse:
    """A cron trigger with an optional IANA timezone and overlap policy."""

    id: str
    app_id: str
    schedule: str
    path: str
    enabled: bool
    timezone: str
    """IANA timezone used to evaluate the schedule; defaults to UTC."""
    skip_if_running: bool
    """When true, consume a scheduled occurrence while a prior cron invocation is pending or dispatching."""
    created_at: datetime.datetime
    suspended_reason: CronResponseSuspendedReason | Unset = UNSET
    """Why an enabled schedule is paused. Redeploy the app successfully to clear no_live_deployment."""
    last_fired_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        app_id = self.app_id

        schedule = self.schedule

        path = self.path

        enabled = self.enabled

        timezone = self.timezone

        skip_if_running = self.skip_if_running

        created_at = self.created_at.isoformat()

        suspended_reason: str | Unset = UNSET
        if not isinstance(self.suspended_reason, Unset):
            suspended_reason = self.suspended_reason

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
                "timezone": timezone,
                "skip_if_running": skip_if_running,
                "created_at": created_at,
            }
        )
        if suspended_reason is not UNSET:
            field_dict["suspended_reason"] = suspended_reason
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

        timezone = d.pop("timezone")

        skip_if_running = d.pop("skip_if_running")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _suspended_reason = d.pop("suspended_reason", UNSET)
        suspended_reason: CronResponseSuspendedReason | Unset
        if isinstance(_suspended_reason, Unset):
            suspended_reason = UNSET
        else:
            suspended_reason = check_cron_response_suspended_reason(_suspended_reason)

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
            timezone=timezone,
            skip_if_running=skip_if_running,
            created_at=created_at,
            suspended_reason=suspended_reason,
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
