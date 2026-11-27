from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ManagedPostgresLockWait")


@_attrs_define
class ManagedPostgresLockWait:
    """Session waiting for a PostgreSQL lock and its observed blockers."""

    pid: int
    blocking_pids: list[int]
    """Blocking session process IDs. Zero indicates a prepared transaction."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        pid = self.pid

        blocking_pids = self.blocking_pids

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "pid": pid,
                "blocking_pids": blocking_pids,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        pid = d.pop("pid")

        blocking_pids = cast(list[int], d.pop("blocking_pids"))

        managed_postgres_lock_wait = cls(
            pid=pid,
            blocking_pids=blocking_pids,
        )

        managed_postgres_lock_wait.additional_properties = d
        return managed_postgres_lock_wait

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
