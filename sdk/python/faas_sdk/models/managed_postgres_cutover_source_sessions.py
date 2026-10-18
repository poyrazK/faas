from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_cutover_source_sessions_state import (
    ManagedPostgresCutoverSourceSessionsState,
    check_managed_postgres_cutover_source_sessions_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_cutover_writer_isolation import ManagedPostgresCutoverWriterIsolation


T = TypeVar("T", bound="ManagedPostgresCutoverSourceSessions")


@_attrs_define
class ManagedPostgresCutoverSourceSessions:
    """Historical source SQL admission and session observations for the frozen selected database set. Never complete
    writer-isolation proof. Preflight and GET initiate no SQL fences. Absent on pending and older reports. Inspect the
    parent stale and expires_at fields.

    """

    state: ManagedPostgresCutoverSourceSessionsState
    admission_closed: bool
    """Admission closure was independently confirmed and fresh at checked_at."""
    selected_databases_drained: bool
    """Selected databases had no observed sessions or prepared transactions at checked_at. Background writers and
    future catalog stability remain unproven."""
    selected_database_count: int
    sessions: int
    prepared_transactions: int
    unselected_databases: int
    blockers: list[str]
    observed_at: datetime.datetime | Unset = UNSET
    writer_isolation: ManagedPostgresCutoverWriterIsolation | Unset = UNSET
    """Historical SQL writer inventory and optional provider guard evidence. Confirmed requires fresh matching
    evidence and idle complete SQL inventory; it does not qualify a deployed provider or allow activation. Neon
    currently lacks the stronger provider guarantee. Names, catalog fingerprints and ownership tokens remain
    private."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        admission_closed = self.admission_closed

        selected_databases_drained = self.selected_databases_drained

        selected_database_count = self.selected_database_count

        sessions = self.sessions

        prepared_transactions = self.prepared_transactions

        unselected_databases = self.unselected_databases

        blockers = self.blockers

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        writer_isolation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.writer_isolation, Unset):
            writer_isolation = self.writer_isolation.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "admission_closed": admission_closed,
                "selected_databases_drained": selected_databases_drained,
                "selected_database_count": selected_database_count,
                "sessions": sessions,
                "prepared_transactions": prepared_transactions,
                "unselected_databases": unselected_databases,
                "blockers": blockers,
            }
        )
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if writer_isolation is not UNSET:
            field_dict["writer_isolation"] = writer_isolation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_cutover_writer_isolation import ManagedPostgresCutoverWriterIsolation

        d = dict(src_dict)
        state = check_managed_postgres_cutover_source_sessions_state(d.pop("state"))

        admission_closed = d.pop("admission_closed")

        selected_databases_drained = d.pop("selected_databases_drained")

        selected_database_count = d.pop("selected_database_count")

        sessions = d.pop("sessions")

        prepared_transactions = d.pop("prepared_transactions")

        unselected_databases = d.pop("unselected_databases")

        blockers = cast(list[str], d.pop("blockers"))

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        _writer_isolation = d.pop("writer_isolation", UNSET)
        writer_isolation: ManagedPostgresCutoverWriterIsolation | Unset
        if isinstance(_writer_isolation, Unset):
            writer_isolation = UNSET
        else:
            writer_isolation = ManagedPostgresCutoverWriterIsolation.from_dict(_writer_isolation)

        managed_postgres_cutover_source_sessions = cls(
            state=state,
            admission_closed=admission_closed,
            selected_databases_drained=selected_databases_drained,
            selected_database_count=selected_database_count,
            sessions=sessions,
            prepared_transactions=prepared_transactions,
            unselected_databases=unselected_databases,
            blockers=blockers,
            observed_at=observed_at,
            writer_isolation=writer_isolation,
        )

        managed_postgres_cutover_source_sessions.additional_properties = d
        return managed_postgres_cutover_source_sessions

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
