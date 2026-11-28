from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_activity_cause_cause import (
    ManagedPostgresActivityCauseCause,
    check_managed_postgres_activity_cause_cause,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresActivityCause")


@_attrs_define
class ManagedPostgresActivityCause:
    """One cause's share of a period's starts and awake time."""

    cause: ManagedPostgresActivityCauseCause
    starts: int
    awake_seconds: int
    first_started_at: datetime.datetime
    app_id: str | Unset = UNSET
    app_slug: str | Unset = UNSET
    detail: str | Unset = UNSET
    """The cron's path for a cron cause."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        cause: str = self.cause

        starts = self.starts

        awake_seconds = self.awake_seconds

        first_started_at = self.first_started_at.isoformat()

        app_id = self.app_id

        app_slug = self.app_slug

        detail = self.detail

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "cause": cause,
                "starts": starts,
                "awake_seconds": awake_seconds,
                "first_started_at": first_started_at,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if app_slug is not UNSET:
            field_dict["app_slug"] = app_slug
        if detail is not UNSET:
            field_dict["detail"] = detail

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        cause = check_managed_postgres_activity_cause_cause(d.pop("cause"))

        starts = d.pop("starts")

        awake_seconds = d.pop("awake_seconds")

        first_started_at = datetime.datetime.fromisoformat(d.pop("first_started_at"))

        app_id = d.pop("app_id", UNSET)

        app_slug = d.pop("app_slug", UNSET)

        detail = d.pop("detail", UNSET)

        managed_postgres_activity_cause = cls(
            cause=cause,
            starts=starts,
            awake_seconds=awake_seconds,
            first_started_at=first_started_at,
            app_id=app_id,
            app_slug=app_slug,
            detail=detail,
        )

        managed_postgres_activity_cause.additional_properties = d
        return managed_postgres_activity_cause

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
