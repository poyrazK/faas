from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tool_publication_record_evidence_origin import (
    ToolPublicationRecordEvidenceOrigin,
    check_tool_publication_record_evidence_origin,
)
from ..models.tool_publication_record_status import ToolPublicationRecordStatus, check_tool_publication_record_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.tool_publication_intent import ToolPublicationIntent
    from ..models.tool_publication_outcome import ToolPublicationOutcome


T = TypeVar("T", bound="ToolPublicationRecord")


@_attrs_define
class ToolPublicationRecord:
    """Account-owned durable publication review and immutable outcome bound to native deployment source identity."""

    id: str
    account_id: UUID
    intent: ToolPublicationIntent
    """Reviewed publication metadata bounded to 64 KiB; never includes local paths or credentials."""
    status: ToolPublicationRecordStatus
    created_at: datetime.datetime
    deadline: datetime.datetime
    evidence_origin: ToolPublicationRecordEvidenceOrigin
    outcome: ToolPublicationOutcome | Unset = UNSET
    """Immutable publisher-attested evidence bounded to 512 KiB. Completed outcomes require the complete observed
    catalog and exact passing tests. Failed or uncertain outcomes contain only status and protocol_verified=false.
   """
    deployment_source_sha256: str | Unset = UNSET
    """Native source-archive digest, distinct from the reviewed source-tree digest"""
    finished_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        account_id = str(self.account_id)

        intent = self.intent.to_dict()

        status: str = self.status

        created_at = self.created_at.isoformat()

        deadline = self.deadline.isoformat()

        evidence_origin: str = self.evidence_origin

        outcome: dict[str, Any] | Unset = UNSET
        if not isinstance(self.outcome, Unset):
            outcome = self.outcome.to_dict()

        deployment_source_sha256 = self.deployment_source_sha256

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "account_id": account_id,
                "intent": intent,
                "status": status,
                "created_at": created_at,
                "deadline": deadline,
                "evidence_origin": evidence_origin,
            }
        )
        if outcome is not UNSET:
            field_dict["outcome"] = outcome
        if deployment_source_sha256 is not UNSET:
            field_dict["deployment_source_sha256"] = deployment_source_sha256
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tool_publication_intent import ToolPublicationIntent
        from ..models.tool_publication_outcome import ToolPublicationOutcome

        d = dict(src_dict)
        id = d.pop("id")

        account_id = UUID(d.pop("account_id"))

        intent = ToolPublicationIntent.from_dict(d.pop("intent"))

        status = check_tool_publication_record_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        deadline = datetime.datetime.fromisoformat(d.pop("deadline"))

        evidence_origin = check_tool_publication_record_evidence_origin(d.pop("evidence_origin"))

        _outcome = d.pop("outcome", UNSET)
        outcome: ToolPublicationOutcome | Unset
        if isinstance(_outcome, Unset):
            outcome = UNSET
        else:
            outcome = ToolPublicationOutcome.from_dict(_outcome)

        deployment_source_sha256 = d.pop("deployment_source_sha256", UNSET)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        tool_publication_record = cls(
            id=id,
            account_id=account_id,
            intent=intent,
            status=status,
            created_at=created_at,
            deadline=deadline,
            evidence_origin=evidence_origin,
            outcome=outcome,
            deployment_source_sha256=deployment_source_sha256,
            finished_at=finished_at,
        )

        tool_publication_record.additional_properties = d
        return tool_publication_record

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
