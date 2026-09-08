from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateCronRequest")


@_attrs_define
class CreateCronRequest:
    """Cron creation payload: schedule expression, target URL, and optional timezone/overlap policy."""

    app_id: str
    schedule: str
    path: str | Unset = UNSET
    enabled: bool | None | Unset = UNSET
    timezone: str | Unset = UNSET
    """IANA timezone; defaults to UTC."""
    skip_if_running: bool | None | Unset = UNSET
    """Skip a scheduled fire when an earlier cron invocation is still running."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        schedule = self.schedule

        path = self.path

        enabled: bool | None | Unset
        if isinstance(self.enabled, Unset):
            enabled = UNSET
        else:
            enabled = self.enabled

        timezone = self.timezone

        skip_if_running: bool | None | Unset
        if isinstance(self.skip_if_running, Unset):
            skip_if_running = UNSET
        else:
            skip_if_running = self.skip_if_running

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "schedule": schedule,
            }
        )
        if path is not UNSET:
            field_dict["path"] = path
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if timezone is not UNSET:
            field_dict["timezone"] = timezone
        if skip_if_running is not UNSET:
            field_dict["skip_if_running"] = skip_if_running

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        schedule = d.pop("schedule")

        path = d.pop("path", UNSET)

        def _parse_enabled(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        enabled = _parse_enabled(d.pop("enabled", UNSET))

        timezone = d.pop("timezone", UNSET)

        def _parse_skip_if_running(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        skip_if_running = _parse_skip_if_running(d.pop("skip_if_running", UNSET))

        create_cron_request = cls(
            app_id=app_id,
            schedule=schedule,
            path=path,
            enabled=enabled,
            timezone=timezone,
            skip_if_running=skip_if_running,
        )

        create_cron_request.additional_properties = d
        return create_cron_request

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
