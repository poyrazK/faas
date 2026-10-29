from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_offboarding_plan_response_status import (
    PlatformTenantOffboardingPlanResponseStatus,
    check_platform_tenant_offboarding_plan_response_status,
)

if TYPE_CHECKING:
    from ..models.platform_tenant_offboarding_plan_actions import PlatformTenantOffboardingPlanActions


T = TypeVar("T", bound="PlatformTenantOffboardingPlanResponse")


@_attrs_define
class PlatformTenantOffboardingPlanResponse:
    """Read-only preview of the status, confirmation digest, and planned offboarding actions for a platform tenant."""

    tenant_id: UUID
    status: PlatformTenantOffboardingPlanResponseStatus
    plan_hash: str
    actions: PlatformTenantOffboardingPlanActions
    """Counts and ownership-scoped changes that would be applied if the offboarding plan is confirmed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tenant_id = str(self.tenant_id)

        status: str = self.status

        plan_hash = self.plan_hash

        actions = self.actions.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tenant_id": tenant_id,
                "status": status,
                "plan_hash": plan_hash,
                "actions": actions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_offboarding_plan_actions import PlatformTenantOffboardingPlanActions

        d = dict(src_dict)
        tenant_id = UUID(d.pop("tenant_id"))

        status = check_platform_tenant_offboarding_plan_response_status(d.pop("status"))

        plan_hash = d.pop("plan_hash")

        actions = PlatformTenantOffboardingPlanActions.from_dict(d.pop("actions"))

        platform_tenant_offboarding_plan_response = cls(
            tenant_id=tenant_id,
            status=status,
            plan_hash=plan_hash,
            actions=actions,
        )

        platform_tenant_offboarding_plan_response.additional_properties = d
        return platform_tenant_offboarding_plan_response

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
