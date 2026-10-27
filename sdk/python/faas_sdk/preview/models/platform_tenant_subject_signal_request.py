from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject


T = TypeVar("T", bound="PlatformTenantSubjectSignalRequest")


@_attrs_define
class PlatformTenantSubjectSignalRequest:
    """One named event for the authenticated customer's own active operations of a definition about one business subject."""

    definition_id: UUID
    """Operation definition whose app"""
    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    signal_id: str
    event: str
    payload: Any | Unset = UNSET
    """JSON each of the customer's reached operations receives as its wait step output; at most 64 KiB."""

    def to_dict(self) -> dict[str, Any]:
        definition_id = str(self.definition_id)

        subject = self.subject.to_dict()

        signal_id = self.signal_id

        event = self.event

        payload = self.payload

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "definition_id": definition_id,
                "subject": subject,
                "signal_id": signal_id,
                "event": event,
            }
        )
        if payload is not UNSET:
            field_dict["payload"] = payload

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject

        d = dict(src_dict)
        definition_id = UUID(d.pop("definition_id"))

        subject = OperationSubject.from_dict(d.pop("subject"))

        signal_id = d.pop("signal_id")

        event = d.pop("event")

        payload = d.pop("payload", UNSET)

        platform_tenant_subject_signal_request = cls(
            definition_id=definition_id,
            subject=subject,
            signal_id=signal_id,
            event=event,
            payload=payload,
        )

        return platform_tenant_subject_signal_request
