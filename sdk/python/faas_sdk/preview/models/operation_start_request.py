from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_submission_scope import OperationSubmissionScope
    from ..models.operation_tenant_identity import OperationTenantIdentity


T = TypeVar("T", bound="OperationStartRequest")


@_attrs_define
class OperationStartRequest:
    """Submission owned by the authenticated platform tenant."""

    definition_id: UUID
    input_: Any
    """JSON input matching the pinned definition."""
    expected_identity: OperationTenantIdentity | Unset = UNSET
    """Tenant credential identity used to bind a private local submission receipt; optional submission fence
    returns 409 operation_identity_conflict if the principal changes."""
    expected_scope: OperationSubmissionScope | Unset = UNSET
    """Optional immutable-definition fence for a browser feature."""

    def to_dict(self) -> dict[str, Any]:
        definition_id = str(self.definition_id)

        input_ = self.input_

        expected_identity: dict[str, Any] | Unset = UNSET
        if not isinstance(self.expected_identity, Unset):
            expected_identity = self.expected_identity.to_dict()

        expected_scope: dict[str, Any] | Unset = UNSET
        if not isinstance(self.expected_scope, Unset):
            expected_scope = self.expected_scope.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "definition_id": definition_id,
                "input": input_,
            }
        )
        if expected_identity is not UNSET:
            field_dict["expected_identity"] = expected_identity
        if expected_scope is not UNSET:
            field_dict["expected_scope"] = expected_scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_submission_scope import OperationSubmissionScope
        from ..models.operation_tenant_identity import OperationTenantIdentity

        d = dict(src_dict)
        definition_id = UUID(d.pop("definition_id"))

        input_ = d.pop("input")

        _expected_identity = d.pop("expected_identity", UNSET)
        expected_identity: OperationTenantIdentity | Unset
        if isinstance(_expected_identity, Unset):
            expected_identity = UNSET
        else:
            expected_identity = OperationTenantIdentity.from_dict(_expected_identity)

        _expected_scope = d.pop("expected_scope", UNSET)
        expected_scope: OperationSubmissionScope | Unset
        if isinstance(_expected_scope, Unset):
            expected_scope = UNSET
        else:
            expected_scope = OperationSubmissionScope.from_dict(_expected_scope)

        operation_start_request = cls(
            definition_id=definition_id,
            input_=input_,
            expected_identity=expected_identity,
            expected_scope=expected_scope,
        )

        return operation_start_request
