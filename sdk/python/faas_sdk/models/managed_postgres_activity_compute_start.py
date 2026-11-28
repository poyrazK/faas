from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_activity_compute_start_cause import (
    ManagedPostgresActivityComputeStartCause,
    check_managed_postgres_activity_compute_start_cause,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresActivityComputeStart")


@_attrs_define
class ManagedPostgresActivityComputeStart:
    """One recorded start of a database's compute."""

    started_at: datetime.datetime
    cause: ManagedPostgresActivityComputeStartCause
    suspended_at: datetime.datetime | Unset = UNSET
    """ Omitted while the compute runs or its suspend has not been read yet. """
    app_id: str | Unset = UNSET
    app_slug: str | Unset = UNSET
    detail: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        started_at = self.started_at.isoformat()

        cause: str = self.cause

        suspended_at: str | Unset = UNSET
        if not isinstance(self.suspended_at, Unset):
            suspended_at = self.suspended_at.isoformat()

        app_id = self.app_id

        app_slug = self.app_slug

        detail = self.detail

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "started_at": started_at,
                "cause": cause,
            }
        )
        if suspended_at is not UNSET:
            field_dict["suspended_at"] = suspended_at
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
        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        cause = check_managed_postgres_activity_compute_start_cause(d.pop("cause"))

        _suspended_at = d.pop("suspended_at", UNSET)
        suspended_at: datetime.datetime | Unset
        if isinstance(_suspended_at, Unset):
            suspended_at = UNSET
        else:
            suspended_at = datetime.datetime.fromisoformat(_suspended_at)

        app_id = d.pop("app_id", UNSET)

        app_slug = d.pop("app_slug", UNSET)

        detail = d.pop("detail", UNSET)

        managed_postgres_activity_compute_start = cls(
            started_at=started_at,
            cause=cause,
            suspended_at=suspended_at,
            app_id=app_id,
            app_slug=app_slug,
            detail=detail,
        )

        managed_postgres_activity_compute_start.additional_properties = d
        return managed_postgres_activity_compute_start

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
