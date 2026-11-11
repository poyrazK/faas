from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.agent_budget_response_source import AgentBudgetResponseSource, check_agent_budget_response_source
from ..types import UNSET, Unset

T = TypeVar("T", bound="AgentBudgetResponse")


@_attrs_define
class AgentBudgetResponse:
    """An agent connection's monthly budget for always-on capacity, and what it committed this month."""

    source: AgentBudgetResponseSource
    """Where the budget comes from."""
    spent_cents: int
    """Always-on capacity the agent added this UTC month, priced at the overage rate and rounded up to the cent."""
    month: str
    """The UTC month spend counts in."""
    monthly_cents: int | Unset = UNSET
    """The budget that applies, in cents a month; absent when unlimited."""
    key_monthly_cents: int | Unset = UNSET
    """The budget set for this agent, when one was."""
    key_unlimited: bool | Unset = UNSET
    """The agent was set to have no budget of its own."""
    org_max_cents: int | Unset = UNSET
    """The organization's ceiling on agents' budgets, when it set one."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source: str = self.source

        spent_cents = self.spent_cents

        month = self.month

        monthly_cents = self.monthly_cents

        key_monthly_cents = self.key_monthly_cents

        key_unlimited = self.key_unlimited

        org_max_cents = self.org_max_cents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "spent_cents": spent_cents,
                "month": month,
            }
        )
        if monthly_cents is not UNSET:
            field_dict["monthly_cents"] = monthly_cents
        if key_monthly_cents is not UNSET:
            field_dict["key_monthly_cents"] = key_monthly_cents
        if key_unlimited is not UNSET:
            field_dict["key_unlimited"] = key_unlimited
        if org_max_cents is not UNSET:
            field_dict["org_max_cents"] = org_max_cents

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source = check_agent_budget_response_source(d.pop("source"))

        spent_cents = d.pop("spent_cents")

        month = d.pop("month")

        monthly_cents = d.pop("monthly_cents", UNSET)

        key_monthly_cents = d.pop("key_monthly_cents", UNSET)

        key_unlimited = d.pop("key_unlimited", UNSET)

        org_max_cents = d.pop("org_max_cents", UNSET)

        agent_budget_response = cls(
            source=source,
            spent_cents=spent_cents,
            month=month,
            monthly_cents=monthly_cents,
            key_monthly_cents=key_monthly_cents,
            key_unlimited=key_unlimited,
            org_max_cents=org_max_cents,
        )

        agent_budget_response.additional_properties = d
        return agent_budget_response

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
