from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.automation_check_evidence import AutomationCheckEvidence


T = TypeVar("T", bound="CheckAutomationPublicationResponse")


@_attrs_define
class CheckAutomationPublicationResponse:
    """Expiring server receipt with safe verified scenario and coverage metadata."""

    receipt: str
    """Opaque 30-minute receipt bound to app, name, draft, actor and policy version. Do not log or expose it."""
    expires_at: datetime.datetime
    evidence: AutomationCheckEvidence
    """Simulation check metadata. server_verified is set only for server-issued publishing receipts; legacy or
    plain client evidence is unverified. Bound to the saved draft hash and version. Contains metadata only; no
    sample inputs, outputs, or failure text. Checked time must be within the past day (five minutes of future clock
    skew allowed)."""

    def to_dict(self) -> dict[str, Any]:
        receipt = self.receipt

        expires_at = self.expires_at.isoformat()

        evidence = self.evidence.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "receipt": receipt,
                "expires_at": expires_at,
                "evidence": evidence,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_check_evidence import AutomationCheckEvidence

        d = dict(src_dict)
        receipt = d.pop("receipt")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        evidence = AutomationCheckEvidence.from_dict(d.pop("evidence"))

        check_automation_publication_response = cls(
            receipt=receipt,
            expires_at=expires_at,
            evidence=evidence,
        )

        return check_automation_publication_response
