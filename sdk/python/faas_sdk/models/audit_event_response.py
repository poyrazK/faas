from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.audit_event_response_data import AuditEventResponseData


T = TypeVar("T", bound="AuditEventResponse")


@_attrs_define
class AuditEventResponse:
    """One row of the customer's security event timeline. The `kind` taxonomy and per-kind `data` schema are documented in
    `docs/adr/035-auth-audit-events.md`.

    """

    id: str
    """ Audit event row id (bigint as string). """
    at: datetime.datetime
    """ When the event was recorded (RFC 3339, UTC). """
    actor: str
    """ Which daemon wrote the row. `apid` for IAM-4 surface; `schedd` for state-transition events (instance wakes /
    parks / watchdog timeouts). """
    kind: str
    """ Namespaced event kind. Common values: `auth.login`, `auth.logout`, `key.created`, `key.deleted`,
    `secret.set`, `secret.deleted`, `account.plan_changed`, `account.deletion_scheduled`,
    `account.deletion_restored`. """
    data: AuditEventResponseData
    """ Kind-specific payload. Always a JSON object; the inner shape depends on `kind`. Plaintext values (e.g.
    secret VALUE) are NEVER carried in `data`. """
    subject: str | Unset = UNSET
    """ Account id (uuid string form) the event was recorded against. Omitted when the event has no subject (e.g.
    system-level). """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        at = self.at.isoformat()

        actor = self.actor

        kind = self.kind

        data = self.data.to_dict()

        subject = self.subject

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "at": at,
                "actor": actor,
                "kind": kind,
                "data": data,
            }
        )
        if subject is not UNSET:
            field_dict["subject"] = subject

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.audit_event_response_data import AuditEventResponseData

        d = dict(src_dict)
        id = d.pop("id")

        at = datetime.datetime.fromisoformat(d.pop("at"))

        actor = d.pop("actor")

        kind = d.pop("kind")

        data = AuditEventResponseData.from_dict(d.pop("data"))

        subject = d.pop("subject", UNSET)

        audit_event_response = cls(
            id=id,
            at=at,
            actor=actor,
            kind=kind,
            data=data,
            subject=subject,
        )

        audit_event_response.additional_properties = d
        return audit_event_response

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
