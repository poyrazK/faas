from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_cutover_preflight_blockers_item import (
    ManagedPostgresCutoverPreflightBlockersItem,
    check_managed_postgres_cutover_preflight_blockers_item,
)
from ..models.managed_postgres_cutover_preflight_state import (
    ManagedPostgresCutoverPreflightState,
    check_managed_postgres_cutover_preflight_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_cutover_runtime_drain import ManagedPostgresCutoverRuntimeDrain
    from ..models.managed_postgres_cutover_source_sessions import ManagedPostgresCutoverSourceSessions


T = TypeVar("T", bound="ManagedPostgresCutoverPreflight")


@_attrs_define
class ManagedPostgresCutoverPreflight:
    """Historical catalog checks and remaining activation blockers. Never an activation token; sealed credentials and
    provider identities are private.

    """

    cutover_id: UUID
    state: ManagedPostgresCutoverPreflightState
    revision: int
    activation_allowed: bool
    """Always false; customer activation remains disabled."""
    stale: bool
    """Recomputed on read for pending checks, expiry, future check time, cutover changes, cancellation or changed
    runtime authority and receipts. Older reports without runtime observations require a new check."""
    binding_ids: list[UUID]
    """Affected source binding IDs observed during evaluation."""
    blockers: list[ManagedPostgresCutoverPreflightBlockersItem]
    verification_fresh: bool
    """All staged credentials had fresh SQL verification at checked_at."""
    source_matches: bool
    target_matches: bool
    bindings_match: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime
    runtime_drain: ManagedPostgresCutoverRuntimeDrain | Unset = UNSET
    """Historical scheduler instance catalog evidence at checked_at. Catalog retirement does not prove complete
    writer isolation. No fence or drain is initiated by preflight. Absent on pending requests and reports created
    before runtime observations were supported."""
    source_sessions: ManagedPostgresCutoverSourceSessions | Unset = UNSET
    """Historical source SQL admission and session observations for the frozen selected database set. Never
    complete writer-isolation proof. Preflight and GET initiate no SQL fences. Absent on pending and older reports.
    Inspect the parent stale and expires_at fields."""
    observed_cutover_updated_at: datetime.datetime | Unset = UNSET
    checked_at: datetime.datetime | Unset = UNSET
    expires_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        cutover_id = str(self.cutover_id)

        state: str = self.state

        revision = self.revision

        activation_allowed = self.activation_allowed

        stale = self.stale

        binding_ids = []
        for binding_ids_item_data in self.binding_ids:
            binding_ids_item = str(binding_ids_item_data)
            binding_ids.append(binding_ids_item)

        blockers = []
        for blockers_item_data in self.blockers:
            blockers_item: str = blockers_item_data
            blockers.append(blockers_item)

        verification_fresh = self.verification_fresh

        source_matches = self.source_matches

        target_matches = self.target_matches

        bindings_match = self.bindings_match

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        runtime_drain: dict[str, Any] | Unset = UNSET
        if not isinstance(self.runtime_drain, Unset):
            runtime_drain = self.runtime_drain.to_dict()

        source_sessions: dict[str, Any] | Unset = UNSET
        if not isinstance(self.source_sessions, Unset):
            source_sessions = self.source_sessions.to_dict()

        observed_cutover_updated_at: str | Unset = UNSET
        if not isinstance(self.observed_cutover_updated_at, Unset):
            observed_cutover_updated_at = self.observed_cutover_updated_at.isoformat()

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "cutover_id": cutover_id,
                "state": state,
                "revision": revision,
                "activation_allowed": activation_allowed,
                "stale": stale,
                "binding_ids": binding_ids,
                "blockers": blockers,
                "verification_fresh": verification_fresh,
                "source_matches": source_matches,
                "target_matches": target_matches,
                "bindings_match": bindings_match,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if runtime_drain is not UNSET:
            field_dict["runtime_drain"] = runtime_drain
        if source_sessions is not UNSET:
            field_dict["source_sessions"] = source_sessions
        if observed_cutover_updated_at is not UNSET:
            field_dict["observed_cutover_updated_at"] = observed_cutover_updated_at
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_cutover_runtime_drain import ManagedPostgresCutoverRuntimeDrain
        from ..models.managed_postgres_cutover_source_sessions import ManagedPostgresCutoverSourceSessions

        d = dict(src_dict)
        cutover_id = UUID(d.pop("cutover_id"))

        state = check_managed_postgres_cutover_preflight_state(d.pop("state"))

        revision = d.pop("revision")

        activation_allowed = d.pop("activation_allowed")

        stale = d.pop("stale")

        binding_ids = []
        _binding_ids = d.pop("binding_ids")
        for binding_ids_item_data in _binding_ids:
            binding_ids_item = UUID(binding_ids_item_data)

            binding_ids.append(binding_ids_item)

        blockers = []
        _blockers = d.pop("blockers")
        for blockers_item_data in _blockers:
            blockers_item = check_managed_postgres_cutover_preflight_blockers_item(blockers_item_data)

            blockers.append(blockers_item)

        verification_fresh = d.pop("verification_fresh")

        source_matches = d.pop("source_matches")

        target_matches = d.pop("target_matches")

        bindings_match = d.pop("bindings_match")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _runtime_drain = d.pop("runtime_drain", UNSET)
        runtime_drain: ManagedPostgresCutoverRuntimeDrain | Unset
        if isinstance(_runtime_drain, Unset):
            runtime_drain = UNSET
        else:
            runtime_drain = ManagedPostgresCutoverRuntimeDrain.from_dict(_runtime_drain)

        _source_sessions = d.pop("source_sessions", UNSET)
        source_sessions: ManagedPostgresCutoverSourceSessions | Unset
        if isinstance(_source_sessions, Unset):
            source_sessions = UNSET
        else:
            source_sessions = ManagedPostgresCutoverSourceSessions.from_dict(_source_sessions)

        _observed_cutover_updated_at = d.pop("observed_cutover_updated_at", UNSET)
        observed_cutover_updated_at: datetime.datetime | Unset
        if isinstance(_observed_cutover_updated_at, Unset):
            observed_cutover_updated_at = UNSET
        else:
            observed_cutover_updated_at = datetime.datetime.fromisoformat(_observed_cutover_updated_at)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        managed_postgres_cutover_preflight = cls(
            cutover_id=cutover_id,
            state=state,
            revision=revision,
            activation_allowed=activation_allowed,
            stale=stale,
            binding_ids=binding_ids,
            blockers=blockers,
            verification_fresh=verification_fresh,
            source_matches=source_matches,
            target_matches=target_matches,
            bindings_match=bindings_match,
            created_at=created_at,
            updated_at=updated_at,
            runtime_drain=runtime_drain,
            source_sessions=source_sessions,
            observed_cutover_updated_at=observed_cutover_updated_at,
            checked_at=checked_at,
            expires_at=expires_at,
        )

        managed_postgres_cutover_preflight.additional_properties = d
        return managed_postgres_cutover_preflight

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
