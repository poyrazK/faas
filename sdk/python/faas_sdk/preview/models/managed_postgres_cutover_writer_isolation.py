from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_cutover_writer_isolation_state import (
    ManagedPostgresCutoverWriterIsolationState,
    check_managed_postgres_cutover_writer_isolation_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresCutoverWriterIsolation")


@_attrs_define
class ManagedPostgresCutoverWriterIsolation:
    """Historical SQL writer inventory and optional provider guard evidence. Confirmed requires fresh matching evidence and
    idle complete SQL inventory; it does not qualify a deployed provider or allow activation. Neon currently lacks the
    stronger provider guarantee. Names, catalog fingerprints and ownership tokens remain private.

    """

    state: ManagedPostgresCutoverWriterIsolationState
    confirmed: bool
    inventory_available: bool
    catalog_matches: bool
    """The complete observed catalog matches its immutable original fingerprint; this is not a catalog lock."""
    catalog_stable: bool
    """Fresh provider evidence attests a durable catalog guard."""
    background_writers_fenced: bool
    """Fresh provider evidence covers background and replication write paths across all dataset endpoints."""
    client_sessions: int
    background_workers: int
    replication_workers: int
    unknown_backends: int
    """Backends whose type is hidden by privileges or not recognized; these prevent confirmation."""
    expires_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        confirmed = self.confirmed

        inventory_available = self.inventory_available

        catalog_matches = self.catalog_matches

        catalog_stable = self.catalog_stable

        background_writers_fenced = self.background_writers_fenced

        client_sessions = self.client_sessions

        background_workers = self.background_workers

        replication_workers = self.replication_workers

        unknown_backends = self.unknown_backends

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "confirmed": confirmed,
                "inventory_available": inventory_available,
                "catalog_matches": catalog_matches,
                "catalog_stable": catalog_stable,
                "background_writers_fenced": background_writers_fenced,
                "client_sessions": client_sessions,
                "background_workers": background_workers,
                "replication_workers": replication_workers,
                "unknown_backends": unknown_backends,
            }
        )
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_managed_postgres_cutover_writer_isolation_state(d.pop("state"))

        confirmed = d.pop("confirmed")

        inventory_available = d.pop("inventory_available")

        catalog_matches = d.pop("catalog_matches")

        catalog_stable = d.pop("catalog_stable")

        background_writers_fenced = d.pop("background_writers_fenced")

        client_sessions = d.pop("client_sessions")

        background_workers = d.pop("background_workers")

        replication_workers = d.pop("replication_workers")

        unknown_backends = d.pop("unknown_backends")

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        managed_postgres_cutover_writer_isolation = cls(
            state=state,
            confirmed=confirmed,
            inventory_available=inventory_available,
            catalog_matches=catalog_matches,
            catalog_stable=catalog_stable,
            background_writers_fenced=background_writers_fenced,
            client_sessions=client_sessions,
            background_workers=background_workers,
            replication_workers=replication_workers,
            unknown_backends=unknown_backends,
            expires_at=expires_at,
        )

        managed_postgres_cutover_writer_isolation.additional_properties = d
        return managed_postgres_cutover_writer_isolation

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
