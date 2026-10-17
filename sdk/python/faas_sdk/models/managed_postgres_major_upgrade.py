from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_major_upgrade_state import (
    ManagedPostgresMajorUpgradeState,
    check_managed_postgres_major_upgrade_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresMajorUpgrade")


@_attrs_define
class ManagedPostgresMajorUpgrade:
    """Durable major upgrade progress. Provider identity and credentials are never returned; existing connections are
    interrupted once.

    """

    id: UUID
    database_id: str
    from_postgres_major: int
    target_postgres_major: int
    generation: int
    state: ManagedPostgresMajorUpgradeState
    connection_interruption_expected: bool
    created_at: datetime.datetime
    last_error_code: str | Unset = UNSET
    """While pending, the latest retry's diagnostic. Once failed, why: upgrade_refused (the cluster stays on its
    major unchanged) or upgrade_failed (restore to a point before the upgrade)."""
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        database_id = self.database_id

        from_postgres_major = self.from_postgres_major

        target_postgres_major = self.target_postgres_major

        generation = self.generation

        state: str = self.state

        connection_interruption_expected = self.connection_interruption_expected

        created_at = self.created_at.isoformat()

        last_error_code = self.last_error_code

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "database_id": database_id,
                "from_postgres_major": from_postgres_major,
                "target_postgres_major": target_postgres_major,
                "generation": generation,
                "state": state,
                "connection_interruption_expected": connection_interruption_expected,
                "created_at": created_at,
            }
        )
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        database_id = d.pop("database_id")

        from_postgres_major = d.pop("from_postgres_major")

        target_postgres_major = d.pop("target_postgres_major")

        generation = d.pop("generation")

        state = check_managed_postgres_major_upgrade_state(d.pop("state"))

        connection_interruption_expected = d.pop("connection_interruption_expected")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        last_error_code = d.pop("last_error_code", UNSET)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        managed_postgres_major_upgrade = cls(
            id=id,
            database_id=database_id,
            from_postgres_major=from_postgres_major,
            target_postgres_major=target_postgres_major,
            generation=generation,
            state=state,
            connection_interruption_expected=connection_interruption_expected,
            created_at=created_at,
            last_error_code=last_error_code,
            completed_at=completed_at,
        )

        managed_postgres_major_upgrade.additional_properties = d
        return managed_postgres_major_upgrade

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
