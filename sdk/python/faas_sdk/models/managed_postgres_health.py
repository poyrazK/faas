from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_health_compute_state import (
    ManagedPostgresHealthComputeState,
    check_managed_postgres_health_compute_state,
)
from ..models.managed_postgres_health_last_error_code import (
    ManagedPostgresHealthLastErrorCode,
    check_managed_postgres_health_last_error_code,
)
from ..models.managed_postgres_health_provider_status import (
    ManagedPostgresHealthProviderStatus,
    check_managed_postgres_health_provider_status,
)
from ..models.managed_postgres_health_status import ManagedPostgresHealthStatus, check_managed_postgres_health_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresHealth")


@_attrs_define
class ManagedPostgresHealth:
    """Cached read-only provider observation, separate from lifecycle state. Healthy does not prove SQL connectivity.
    Failed checks update checked_at while retaining last_success_at; stale observations must not imply current
    availability.

    """

    enabled: bool
    status: ManagedPostgresHealthStatus
    fresh: bool
    provider_status: ManagedPostgresHealthProviderStatus
    compute_state: ManagedPostgresHealthComputeState
    stale_after_seconds: int
    checked_at: datetime.datetime | Unset = UNSET
    """Latest metadata attempt, including provider request failures."""
    last_success_at: datetime.datetime | Unset = UNSET
    """Latest valid metadata response, including responses describing degraded resources."""
    last_error_code: ManagedPostgresHealthLastErrorCode | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        status: str = self.status

        fresh = self.fresh

        provider_status: str = self.provider_status

        compute_state: str = self.compute_state

        stale_after_seconds = self.stale_after_seconds

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        last_success_at: str | Unset = UNSET
        if not isinstance(self.last_success_at, Unset):
            last_success_at = self.last_success_at.isoformat()

        last_error_code: str | Unset = UNSET
        if not isinstance(self.last_error_code, Unset):
            last_error_code = self.last_error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "status": status,
                "fresh": fresh,
                "provider_status": provider_status,
                "compute_state": compute_state,
                "stale_after_seconds": stale_after_seconds,
            }
        )
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if last_success_at is not UNSET:
            field_dict["last_success_at"] = last_success_at
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        status = check_managed_postgres_health_status(d.pop("status"))

        fresh = d.pop("fresh")

        provider_status = check_managed_postgres_health_provider_status(d.pop("provider_status"))

        compute_state = check_managed_postgres_health_compute_state(d.pop("compute_state"))

        stale_after_seconds = d.pop("stale_after_seconds")

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        _last_success_at = d.pop("last_success_at", UNSET)
        last_success_at: datetime.datetime | Unset
        if isinstance(_last_success_at, Unset):
            last_success_at = UNSET
        else:
            last_success_at = datetime.datetime.fromisoformat(_last_success_at)

        _last_error_code = d.pop("last_error_code", UNSET)
        last_error_code: ManagedPostgresHealthLastErrorCode | Unset
        if isinstance(_last_error_code, Unset):
            last_error_code = UNSET
        else:
            last_error_code = check_managed_postgres_health_last_error_code(_last_error_code)

        managed_postgres_health = cls(
            enabled=enabled,
            status=status,
            fresh=fresh,
            provider_status=provider_status,
            compute_state=compute_state,
            stale_after_seconds=stale_after_seconds,
            checked_at=checked_at,
            last_success_at=last_success_at,
            last_error_code=last_error_code,
        )

        managed_postgres_health.additional_properties = d
        return managed_postgres_health

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
