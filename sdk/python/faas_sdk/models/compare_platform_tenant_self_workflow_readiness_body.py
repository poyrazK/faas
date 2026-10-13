from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operation_workflow_readiness_comparison_request_change import (
    OperationWorkflowReadinessComparisonRequestChange,
    check_operation_workflow_readiness_comparison_request_change,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_snapshot import OperationWorkflowReadinessSnapshot


T = TypeVar("T", bound="ComparePlatformTenantSelfWorkflowReadinessBody")


@_attrs_define
class ComparePlatformTenantSelfWorkflowReadinessBody:
    scope: str
    """ReadinessComparisonRequest. Explicit application environment for this comparison."""
    baseline: OperationWorkflowReadinessSnapshot
    """Caller-held compact readiness observations for at most 100 returned transitions. No historical
    authentication or execution authority is implied."""
    app_id: UUID
    """ReadinessComparisonRequest. Required explicit app UUID for customer-self requests; reject on account routes."""
    tenant_id: UUID | Unset = UNSET
    """ReadinessComparisonRequest. Optional account-owned customer selector; reject on customer-self routes."""
    change: OperationWorkflowReadinessComparisonRequestChange | Unset = UNSET
    """ReadinessComparisonRequest. Filter after all baseline transitions have been classified; unavailable includes
    contract and state boundaries."""
    limit: int | Unset = 20
    """ReadinessComparisonRequest. Maximum matching rows returned after comparison; defaults to 20 and is capped at
    100."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope = self.scope

        baseline = self.baseline.to_dict()

        app_id = str(self.app_id)

        tenant_id: str | Unset = UNSET
        if not isinstance(self.tenant_id, Unset):
            tenant_id = str(self.tenant_id)

        change: str | Unset = UNSET
        if not isinstance(self.change, Unset):
            change = self.change

        limit = self.limit

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "baseline": baseline,
                "app_id": app_id,
            }
        )
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id
        if change is not UNSET:
            field_dict["change"] = change
        if limit is not UNSET:
            field_dict["limit"] = limit

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_snapshot import OperationWorkflowReadinessSnapshot

        d = dict(src_dict)
        scope = d.pop("scope")

        baseline = OperationWorkflowReadinessSnapshot.from_dict(d.pop("baseline"))

        app_id = UUID(d.pop("app_id"))

        _tenant_id = d.pop("tenant_id", UNSET)
        tenant_id: UUID | Unset
        if isinstance(_tenant_id, Unset):
            tenant_id = UNSET
        else:
            tenant_id = UUID(_tenant_id)

        _change = d.pop("change", UNSET)
        change: OperationWorkflowReadinessComparisonRequestChange | Unset
        if isinstance(_change, Unset):
            change = UNSET
        else:
            change = check_operation_workflow_readiness_comparison_request_change(_change)

        limit = d.pop("limit", UNSET)

        compare_platform_tenant_self_workflow_readiness_body = cls(
            scope=scope,
            baseline=baseline,
            app_id=app_id,
            tenant_id=tenant_id,
            change=change,
            limit=limit,
        )

        compare_platform_tenant_self_workflow_readiness_body.additional_properties = d
        return compare_platform_tenant_self_workflow_readiness_body

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
