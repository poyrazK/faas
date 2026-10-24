from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.audit_log_entry_data import AuditLogEntryData


T = TypeVar("T", bound="AuditLogEntry")


@_attrs_define
class AuditLogEntry:
    """One row of the FK-free `audit_log` table (issue #755 /
    PR-6). The row outlives the account it relates to so a
    regulator / DPO can re-derive the post-deletion state
    without joining back to a deleted `accounts` row. The
    table is append-only by spec (ISO 27001 SoA A.5.33 —
    retention forever); there is no UPDATE / DELETE
    permission in production and no Go-side path that would
    emit one.

    """

    id: UUID
    """Audit-log row id (uuid canonical form)."""
    kind: str
    """Namespaced audit-log kind. The PR-6 surface currently
    emits only `account.deleted` from inside
    `(*PgStore).DeleteAccount` / `(*MemStore).DeleteAccount`
    so the regulator can replay post-deletion state. The
    closed vocabulary will widen in a follow-up PR if a
    future audit emitter reuses the table for a new
    evidence surface.
    """
    received_at: datetime.datetime
    """When the audit-log row was recorded (RFC 3339, UTC)."""
    account_id: UUID | Unset = UNSET
    """Account id (uuid canonical form) the row was recorded
    against. Nullable in the schema (anonymous /
    background activity can emit rows); omitted on the
    wire when the column is NULL.
    """
    account_email: str | Unset = UNSET
    """Email captured at copy-time inside
    `DeleteAccount`. The audit row is self-contained: a
    regulator reading a row for a UUID that no longer
    exists in `accounts` still sees the human identifier
    without joining back to a deleted accounts row.
    Omitted when the row was emitted by anonymous
    activity.
    """
    actor: str | Unset = UNSET
    """Which daemon wrote the row. PR-6 emits `grace-sweep`
    from inside the DeleteAccount store method. Future
    emitters (follow-up PRs) will widen the vocabulary.
    """
    data: AuditLogEntryData | Unset = UNSET
    """Kind-specific payload. Always a JSON object; the
    inner shape depends on `kind`. For
    `account.deleted`, the payload carries `source`
    (`grace-sweep` today), `email` (the verbatim
    captured email), and `actor` (`grace-sweep` today)
    so the dashboard can render the row without joining
    back to a deleted `accounts` row.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        kind = self.kind

        received_at = self.received_at.isoformat()

        account_id: str | Unset = UNSET
        if not isinstance(self.account_id, Unset):
            account_id = str(self.account_id)

        account_email = self.account_email

        actor = self.actor

        data: dict[str, Any] | Unset = UNSET
        if not isinstance(self.data, Unset):
            data = self.data.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "kind": kind,
                "received_at": received_at,
            }
        )
        if account_id is not UNSET:
            field_dict["account_id"] = account_id
        if account_email is not UNSET:
            field_dict["account_email"] = account_email
        if actor is not UNSET:
            field_dict["actor"] = actor
        if data is not UNSET:
            field_dict["data"] = data

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.audit_log_entry_data import AuditLogEntryData

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        kind = d.pop("kind")

        received_at = datetime.datetime.fromisoformat(d.pop("received_at"))

        _account_id = d.pop("account_id", UNSET)
        account_id: UUID | Unset
        if isinstance(_account_id, Unset):
            account_id = UNSET
        else:
            account_id = UUID(_account_id)

        account_email = d.pop("account_email", UNSET)

        actor = d.pop("actor", UNSET)

        _data = d.pop("data", UNSET)
        data: AuditLogEntryData | Unset
        if isinstance(_data, Unset):
            data = UNSET
        else:
            data = AuditLogEntryData.from_dict(_data)

        audit_log_entry = cls(
            id=id,
            kind=kind,
            received_at=received_at,
            account_id=account_id,
            account_email=account_email,
            actor=actor,
            data=data,
        )

        audit_log_entry.additional_properties = d
        return audit_log_entry

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
