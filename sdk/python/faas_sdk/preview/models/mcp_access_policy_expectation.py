from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPAccessPolicyExpectation")


@_attrs_define
class MCPAccessPolicyExpectation:
    """Native stored-policy concurrency condition for the exact canonical MCP resource, optionally including the converged
    account revision. It does not replace runtime authorization checks.

    """

    resource: str
    fingerprint: str
    account_revision: int | Unset = UNSET

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

        mcp_access_policy_expectation = cls(
            resource=resource,
            fingerprint=fingerprint,
            account_revision=account_revision,
        )

        return mcp_access_policy_expectation
