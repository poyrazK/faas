from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.financial_budget_target_kind import FinancialBudgetTargetKind, check_financial_budget_target_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="FinancialBudgetTarget")


@_attrs_define
class FinancialBudgetTarget:
    """Current workload selected or left running by a proposed response."""

    kind: FinancialBudgetTargetKind
    id: UUID
    name: str
    effect: str
    environment_id: UUID | Unset = UNSET
    deployment_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        id = str(self.id)

        name = self.name

        effect = self.effect

        environment_id: str | Unset = UNSET
        if not isinstance(self.environment_id, Unset):
            environment_id = str(self.environment_id)

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "id": id,
                "name": name,
                "effect": effect,
            }
        )
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_financial_budget_target_kind(d.pop("kind"))

        id = UUID(d.pop("id"))

        name = d.pop("name")

        effect = d.pop("effect")

        _environment_id = d.pop("environment_id", UNSET)
        environment_id: UUID | Unset
        if isinstance(_environment_id, Unset):
            environment_id = UNSET
        else:
            environment_id = UUID(_environment_id)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        financial_budget_target = cls(
            kind=kind,
            id=id,
            name=name,
            effect=effect,
            environment_id=environment_id,
            deployment_id=deployment_id,
        )

        financial_budget_target.additional_properties = d
        return financial_budget_target

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
