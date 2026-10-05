from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.financial_budget_revision_response_mutation import (
    FinancialBudgetRevisionResponseMutation,
    check_financial_budget_revision_response_mutation,
)

if TYPE_CHECKING:
    from ..models.financial_budget_spec import FinancialBudgetSpec


T = TypeVar("T", bound="FinancialBudgetRevisionResponse")


@_attrs_define
class FinancialBudgetRevisionResponse:
    """Immutable intent audit written in the same transaction as the policy."""

    policy_id: UUID
    revision: int
    actor: str
    """Authenticated account or API key identity; never a supplied actor value."""
    mutation: FinancialBudgetRevisionResponseMutation
    spec: FinancialBudgetSpec
    """Customer budget intent; activation and enforcement are separately acknowledged."""
    recorded_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        policy_id = str(self.policy_id)

        revision = self.revision

        actor = self.actor

        mutation: str = self.mutation

        spec = self.spec.to_dict()

        recorded_at = self.recorded_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "policy_id": policy_id,
                "revision": revision,
                "actor": actor,
                "mutation": mutation,
                "spec": spec,
                "recorded_at": recorded_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_budget_spec import FinancialBudgetSpec

        d = dict(src_dict)
        policy_id = UUID(d.pop("policy_id"))

        revision = d.pop("revision")

        actor = d.pop("actor")

        mutation = check_financial_budget_revision_response_mutation(d.pop("mutation"))

        spec = FinancialBudgetSpec.from_dict(d.pop("spec"))

        recorded_at = datetime.datetime.fromisoformat(d.pop("recorded_at"))

        financial_budget_revision_response = cls(
            policy_id=policy_id,
            revision=revision,
            actor=actor,
            mutation=mutation,
            spec=spec,
            recorded_at=recorded_at,
        )

        financial_budget_revision_response.additional_properties = d
        return financial_budget_revision_response

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
