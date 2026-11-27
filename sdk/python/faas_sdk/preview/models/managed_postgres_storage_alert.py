from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_storage_alert_state import (
    ManagedPostgresStorageAlertState,
    check_managed_postgres_storage_alert_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresStorageAlert")


@_attrs_define
class ManagedPostgresStorageAlert:
    """A recorded transition of a primary storage incident."""

    incident_id: UUID
    state: ManagedPostgresStorageAlertState
    sampled_at: datetime.datetime
    total_bytes: int
    free_bytes: int
    growth_reason: str | Unset = UNSET
    """Automatic storage growth status when the transition was recorded."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        incident_id = str(self.incident_id)

        state: str = self.state

        sampled_at = self.sampled_at.isoformat()

        total_bytes = self.total_bytes

        free_bytes = self.free_bytes

        growth_reason = self.growth_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "incident_id": incident_id,
                "state": state,
                "sampled_at": sampled_at,
                "total_bytes": total_bytes,
                "free_bytes": free_bytes,
            }
        )
        if growth_reason is not UNSET:
            field_dict["growth_reason"] = growth_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        incident_id = UUID(d.pop("incident_id"))

        state = check_managed_postgres_storage_alert_state(d.pop("state"))

        sampled_at = datetime.datetime.fromisoformat(d.pop("sampled_at"))

        total_bytes = d.pop("total_bytes")

        free_bytes = d.pop("free_bytes")

        growth_reason = d.pop("growth_reason", UNSET)

        managed_postgres_storage_alert = cls(
            incident_id=incident_id,
            state=state,
            sampled_at=sampled_at,
            total_bytes=total_bytes,
            free_bytes=free_bytes,
            growth_reason=growth_reason,
        )

        managed_postgres_storage_alert.additional_properties = d
        return managed_postgres_storage_alert

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
