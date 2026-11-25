from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_autovacuum_profile import (
    ManagedPostgresAutovacuumProfile,
    check_managed_postgres_autovacuum_profile,
)
from ..models.managed_postgres_autovacuum_state import (
    ManagedPostgresAutovacuumState,
    check_managed_postgres_autovacuum_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_autovacuum_settings import ManagedPostgresAutovacuumSettings


T = TypeVar("T", bound="ManagedPostgresAutovacuum")


@_attrs_define
class ManagedPostgresAutovacuum:
    database_id: UUID
    profile: ManagedPostgresAutovacuumProfile
    state: ManagedPostgresAutovacuumState
    effective: ManagedPostgresAutovacuumSettings | Unset = UNSET
    """Active cluster-wide scale factors sampled from PostgreSQL."""
    replica_effective: ManagedPostgresAutovacuumSettings | Unset = UNSET
    """Active cluster-wide scale factors sampled from PostgreSQL."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = str(self.database_id)

        profile: str = self.profile

        state: str = self.state

        effective: dict[str, Any] | Unset = UNSET
        if not isinstance(self.effective, Unset):
            effective = self.effective.to_dict()

        replica_effective: dict[str, Any] | Unset = UNSET
        if not isinstance(self.replica_effective, Unset):
            replica_effective = self.replica_effective.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "profile": profile,
                "state": state,
            }
        )
        if effective is not UNSET:
            field_dict["effective"] = effective
        if replica_effective is not UNSET:
            field_dict["replica_effective"] = replica_effective

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_autovacuum_settings import ManagedPostgresAutovacuumSettings

        d = dict(src_dict)
        database_id = UUID(d.pop("database_id"))

        profile = check_managed_postgres_autovacuum_profile(d.pop("profile"))

        state = check_managed_postgres_autovacuum_state(d.pop("state"))

        _effective = d.pop("effective", UNSET)
        effective: ManagedPostgresAutovacuumSettings | Unset
        if isinstance(_effective, Unset):
            effective = UNSET
        else:
            effective = ManagedPostgresAutovacuumSettings.from_dict(_effective)

        _replica_effective = d.pop("replica_effective", UNSET)
        replica_effective: ManagedPostgresAutovacuumSettings | Unset
        if isinstance(_replica_effective, Unset):
            replica_effective = UNSET
        else:
            replica_effective = ManagedPostgresAutovacuumSettings.from_dict(_replica_effective)

        managed_postgres_autovacuum = cls(
            database_id=database_id,
            profile=profile,
            state=state,
            effective=effective,
            replica_effective=replica_effective,
        )

        managed_postgres_autovacuum.additional_properties = d
        return managed_postgres_autovacuum

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
