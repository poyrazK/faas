from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationTenantIdentity")


@_attrs_define
class OperationTenantIdentity:
    """Tenant credential identity used to bind a private local submission receipt; optional submission fence returns 409
    operation_identity_conflict if the principal changes.

    """

    account_id: UUID
    platform_tenant_id: UUID

    def to_dict(self) -> dict[str, Any]:
        account_id = str(self.account_id)

        platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "account_id": account_id,
                "platform_tenant_id": platform_tenant_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        platform_tenant_id = UUID(d.pop("platform_tenant_id"))

        operation_tenant_identity = cls(
            account_id=account_id,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_tenant_identity
