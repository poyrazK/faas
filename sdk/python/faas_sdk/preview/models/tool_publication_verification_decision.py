from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ToolPublicationVerificationDecision")


@_attrs_define
class ToolPublicationVerificationDecision:
    """Owner approval selecting one sealed test token or managed OAuth connection, or revoking unconsumed candidate-test
    authority.

    """

    request_sha256: str
    credential_secret_ref: str | Unset = UNSET
    """Existing ordinary secret:MCP_* app test-token reference; exclusive with credential_connection_id and
    forbidden for revocation"""
    credential_connection_id: UUID | Unset = UNSET
    """Existing owner-approved execution OAuth connection; exclusive with credential_secret_ref and forbidden for
    revocation"""
    approve_readiness_roles: bool | Unset = False
    """Required explicit owner acknowledgement for this candidate to use its reviewed policy readiness credentials;
    forbidden on revocation"""
    revoke: bool | Unset = False

    def to_dict(self) -> dict[str, Any]:
        request_sha256 = self.request_sha256

        credential_secret_ref = self.credential_secret_ref

        credential_connection_id: str | Unset = UNSET
        if not isinstance(self.credential_connection_id, Unset):
            credential_connection_id = str(self.credential_connection_id)

        approve_readiness_roles = self.approve_readiness_roles

        revoke = self.revoke

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_sha256": request_sha256,
            }
        )
        if credential_secret_ref is not UNSET:
            field_dict["credential_secret_ref"] = credential_secret_ref
        if credential_connection_id is not UNSET:
            field_dict["credential_connection_id"] = credential_connection_id
        if approve_readiness_roles is not UNSET:
            field_dict["approve_readiness_roles"] = approve_readiness_roles
        if revoke is not UNSET:
            field_dict["revoke"] = revoke

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_sha256 = d.pop("request_sha256")

        credential_secret_ref = d.pop("credential_secret_ref", UNSET)

        _credential_connection_id = d.pop("credential_connection_id", UNSET)
        credential_connection_id: UUID | Unset
        if isinstance(_credential_connection_id, Unset):
            credential_connection_id = UNSET
        else:
            credential_connection_id = UUID(_credential_connection_id)

        approve_readiness_roles = d.pop("approve_readiness_roles", UNSET)

        revoke = d.pop("revoke", UNSET)

        tool_publication_verification_decision = cls(
            request_sha256=request_sha256,
            credential_secret_ref=credential_secret_ref,
            credential_connection_id=credential_connection_id,
            approve_readiness_roles=approve_readiness_roles,
            revoke=revoke,
        )

        return tool_publication_verification_decision
