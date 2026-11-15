from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tool_publication_verification_status import (
    ToolPublicationVerificationStatus,
    check_tool_publication_verification_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.tool_publication_verification_outcome import ToolPublicationVerificationOutcome


T = TypeVar("T", bound="ToolPublicationVerification")


@_attrs_define
class ToolPublicationVerification:
    """Durable public candidate-test metadata; never includes a token, secret reference or ciphertext."""

    request_sha256: str
    publication_id: str
    key_id: UUID
    app_id: UUID
    deployment_id: UUID
    deployment_source_sha256: str
    catalog_sha256: str
    status: ToolPublicationVerificationStatus
    created_at: datetime.datetime
    deadline: datetime.datetime
    readiness_artifact_sha256: str | Unset = UNSET
    """Candidate readiness configuration and credential authority fingerprint"""
    outcome: ToolPublicationVerificationOutcome | Unset = UNSET
    """Platform-observed catalog and named test results without credentials or upstream response data."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_sha256 = self.request_sha256

        publication_id = self.publication_id

        key_id = str(self.key_id)

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        deployment_source_sha256 = self.deployment_source_sha256

        catalog_sha256 = self.catalog_sha256

        status: str = self.status

        created_at = self.created_at.isoformat()

        deadline = self.deadline.isoformat()

        readiness_artifact_sha256 = self.readiness_artifact_sha256

        outcome: dict[str, Any] | Unset = UNSET
        if not isinstance(self.outcome, Unset):
            outcome = self.outcome.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_sha256": request_sha256,
                "publication_id": publication_id,
                "key_id": key_id,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "deployment_source_sha256": deployment_source_sha256,
                "catalog_sha256": catalog_sha256,
                "status": status,
                "created_at": created_at,
                "deadline": deadline,
            }
        )
        if readiness_artifact_sha256 is not UNSET:
            field_dict["readiness_artifact_sha256"] = readiness_artifact_sha256
        if outcome is not UNSET:
            field_dict["outcome"] = outcome

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tool_publication_verification_outcome import ToolPublicationVerificationOutcome

        d = dict(src_dict)
        request_sha256 = d.pop("request_sha256")

        publication_id = d.pop("publication_id")

        key_id = UUID(d.pop("key_id"))

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        deployment_source_sha256 = d.pop("deployment_source_sha256")

        catalog_sha256 = d.pop("catalog_sha256")

        status = check_tool_publication_verification_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        deadline = datetime.datetime.fromisoformat(d.pop("deadline"))

        readiness_artifact_sha256 = d.pop("readiness_artifact_sha256", UNSET)

        _outcome = d.pop("outcome", UNSET)
        outcome: ToolPublicationVerificationOutcome | Unset
        if isinstance(_outcome, Unset):
            outcome = UNSET
        else:
            outcome = ToolPublicationVerificationOutcome.from_dict(_outcome)

        tool_publication_verification = cls(
            request_sha256=request_sha256,
            publication_id=publication_id,
            key_id=key_id,
            app_id=app_id,
            deployment_id=deployment_id,
            deployment_source_sha256=deployment_source_sha256,
            catalog_sha256=catalog_sha256,
            status=status,
            created_at=created_at,
            deadline=deadline,
            readiness_artifact_sha256=readiness_artifact_sha256,
            outcome=outcome,
        )

        tool_publication_verification.additional_properties = d
        return tool_publication_verification

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
