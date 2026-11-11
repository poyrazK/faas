from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="AgentBudgetRequest")


@_attrs_define
class AgentBudgetRequest:
    """Sets an agent connection's budget: exactly one of monthly_cents, unlimited or default."""

    monthly_cents: int | Unset = UNSET
    """A monthly budget in cents."""
    unlimited: bool | Unset = UNSET
    """No budget of its own; the organization's ceiling still applies."""
    default: bool | Unset = UNSET
    """Return to the plan's default budget."""

    def to_dict(self) -> dict[str, Any]:
        monthly_cents = self.monthly_cents

        unlimited = self.unlimited

        default = self.default

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if monthly_cents is not UNSET:
            field_dict["monthly_cents"] = monthly_cents
        if unlimited is not UNSET:
            field_dict["unlimited"] = unlimited
        if default is not UNSET:
            field_dict["default"] = default

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        monthly_cents = d.pop("monthly_cents", UNSET)

        unlimited = d.pop("unlimited", UNSET)

        default = d.pop("default", UNSET)

        agent_budget_request = cls(
            monthly_cents=monthly_cents,
            unlimited=unlimited,
            default=default,
        )

        return agent_budget_request
