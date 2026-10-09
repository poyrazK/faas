from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_tenant_identity import OperationTenantIdentity


T = TypeVar("T", bound="OperationSubmissionLookupRequest")


@_attrs_define
class OperationSubmissionLookupRequest:
    """Selectors for an authenticated customer's retained idempotency identity; bounded to 4096 bytes."""

    app_id: UUID
    scope: str
    name: str
    idempotency_key: str
    expected_identity: OperationTenantIdentity | Unset = UNSET
    """Tenant credential identity used to bind a private local submission receipt; optional submission fence
    returns 409 operation_identity_conflict if the principal changes."""

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        scope = self.scope

        name = self.name

        idempotency_key = self.idempotency_key

        expected_identity: dict[str, Any] | Unset = UNSET
        if not isinstance(self.expected_identity, Unset):
            expected_identity = self.expected_identity.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "scope": scope,
                "name": name,
                "idempotency_key": idempotency_key,
            }
        )
        if expected_identity is not UNSET:
            field_dict["expected_identity"] = expected_identity

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_tenant_identity import OperationTenantIdentity

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        name = d.pop("name")

        idempotency_key = d.pop("idempotency_key")

        _expected_identity = d.pop("expected_identity", UNSET)
        expected_identity: OperationTenantIdentity | Unset
        if isinstance(_expected_identity, Unset):
            expected_identity = UNSET
        else:
            expected_identity = OperationTenantIdentity.from_dict(_expected_identity)

        operation_submission_lookup_request = cls(
            app_id=app_id,
            scope=scope,
            name=name,
            idempotency_key=idempotency_key,
            expected_identity=expected_identity,
        )

        return operation_submission_lookup_request
