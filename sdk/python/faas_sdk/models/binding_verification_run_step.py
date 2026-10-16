from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.binding_verification_run_step_resource_kind import (
    BindingVerificationRunStepResourceKind,
    check_binding_verification_run_step_resource_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_verification_plan_step import BindingVerificationPlanStep


T = TypeVar("T", bound="BindingVerificationRunStep")


@_attrs_define
class BindingVerificationRunStep:
    """One frozen verification step and its retained admission and completion state."""

    plan: BindingVerificationPlanStep
    """Selected binding resource and immutable operation planned for a verification receipt."""
    owned: bool
    status: str
    resource_id: UUID | Unset = UNSET
    resource_kind: BindingVerificationRunStepResourceKind | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        plan = self.plan.to_dict()

        owned = self.owned

        status = self.status

        resource_id: str | Unset = UNSET
        if not isinstance(self.resource_id, Unset):
            resource_id = str(self.resource_id)

        resource_kind: str | Unset = UNSET
        if not isinstance(self.resource_kind, Unset):
            resource_kind = self.resource_kind

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "plan": plan,
                "owned": owned,
                "status": status,
            }
        )
        if resource_id is not UNSET:
            field_dict["resource_id"] = resource_id
        if resource_kind is not UNSET:
            field_dict["resource_kind"] = resource_kind

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_verification_plan_step import BindingVerificationPlanStep

        d = dict(src_dict)
        plan = BindingVerificationPlanStep.from_dict(d.pop("plan"))

        owned = d.pop("owned")

        status = d.pop("status")

        _resource_id = d.pop("resource_id", UNSET)
        resource_id: UUID | Unset
        if isinstance(_resource_id, Unset):
            resource_id = UNSET
        else:
            resource_id = UUID(_resource_id)

        _resource_kind = d.pop("resource_kind", UNSET)
        resource_kind: BindingVerificationRunStepResourceKind | Unset
        if isinstance(_resource_kind, Unset):
            resource_kind = UNSET
        else:
            resource_kind = check_binding_verification_run_step_resource_kind(_resource_kind)

        binding_verification_run_step = cls(
            plan=plan,
            owned=owned,
            status=status,
            resource_id=resource_id,
            resource_kind=resource_kind,
        )

        binding_verification_run_step.additional_properties = d
        return binding_verification_run_step

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
