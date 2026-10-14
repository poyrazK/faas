from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateDeploymentTrafficRequestExpectedMcpAccessPolicy")


@_attrs_define
class UpdateDeploymentTrafficRequestExpectedMcpAccessPolicy:
    """Optional account-wide stored MCP policy condition, checked in the traffic transaction. Changed overlapping JWT rules
    return 409 mcp_access_policy_changed without changing traffic. When account_revision is supplied, the transaction
    also requires the unchanged account revision and fresh replay from every currently registered serving gateway.
    Gateway evidence is a database snapshot at admission, not runtime authorization or a lease on fleet membership.

    """

    resource: str
    """Reviewed HTTPS MCP endpoint without credentials, query or fragment."""
    fingerprint: str
    """Version 1 SHA-256 fingerprint returned by gregale mcp access audit."""
    account_revision: int | Unset = UNSET
    """Optional audited account edge-rule revision. Returns 409 mcp_account_revision_changed,
    mcp_gateway_evidence_unavailable, mcp_gateway_evidence_stale, or mcp_gateway_replay_pending when the transaction
    cannot admit promotion."""

    def to_dict(self) -> dict[str, Any]:
        resource = self.resource

        fingerprint = self.fingerprint

        account_revision = self.account_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "resource": resource,
                "fingerprint": fingerprint,
            }
        )
        if account_revision is not UNSET:
            field_dict["account_revision"] = account_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        resource = d.pop("resource")

        fingerprint = d.pop("fingerprint")

        account_revision = d.pop("account_revision", UNSET)

        update_deployment_traffic_request_expected_mcp_access_policy = cls(
            resource=resource,
            fingerprint=fingerprint,
            account_revision=account_revision,
        )

        return update_deployment_traffic_request_expected_mcp_access_policy
