from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_migration_preparation_state import (
    ManagedPostgresMigrationPreparationState,
    check_managed_postgres_migration_preparation_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_migration_preparation_blocker import ManagedPostgresMigrationPreparationBlocker


T = TypeVar("T", bound="ManagedPostgresMigrationPreparation")


@_attrs_define
class ManagedPostgresMigrationPreparation:
    """Schema preparation on the fenced target before the final source write pause."""

    state: ManagedPostgresMigrationPreparationState
    indexes_total: int
    indexes_ready: int
    sequences: int
    reason: str | Unset = UNSET
    blockers: list[ManagedPostgresMigrationPreparationBlocker] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        indexes_total = self.indexes_total

        indexes_ready = self.indexes_ready

        sequences = self.sequences

        reason = self.reason

        blockers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.blockers, Unset):
            blockers = []
            for blockers_item_data in self.blockers:
                blockers_item = blockers_item_data.to_dict()
                blockers.append(blockers_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "indexes_total": indexes_total,
                "indexes_ready": indexes_ready,
                "sequences": sequences,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if blockers is not UNSET:
            field_dict["blockers"] = blockers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_migration_preparation_blocker import ManagedPostgresMigrationPreparationBlocker

        d = dict(src_dict)
        state = check_managed_postgres_migration_preparation_state(d.pop("state"))

        indexes_total = d.pop("indexes_total")

        indexes_ready = d.pop("indexes_ready")

        sequences = d.pop("sequences")

        reason = d.pop("reason", UNSET)

        _blockers = d.pop("blockers", UNSET)
        blockers: list[ManagedPostgresMigrationPreparationBlocker] | Unset = UNSET
        if _blockers is not UNSET:
            blockers = []
            for blockers_item_data in _blockers:
                blockers_item = ManagedPostgresMigrationPreparationBlocker.from_dict(blockers_item_data)

                blockers.append(blockers_item)

        managed_postgres_migration_preparation = cls(
            state=state,
            indexes_total=indexes_total,
            indexes_ready=indexes_ready,
            sequences=sequences,
            reason=reason,
            blockers=blockers,
        )

        managed_postgres_migration_preparation.additional_properties = d
        return managed_postgres_migration_preparation

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
