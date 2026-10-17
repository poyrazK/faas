from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_plan_response_tier_mode import (
    PlatformTenantPlanResponseTierMode,
    check_platform_tenant_plan_response_tier_mode,
)
from ..models.platform_tenant_plan_response_unit import (
    PlatformTenantPlanResponseUnit,
    check_platform_tenant_plan_response_unit,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_consumer_rate_card_tier import APIConsumerRateCardTier
    from ..models.platform_tenant_plan_budget import PlatformTenantPlanBudget
    from ..models.platform_tenant_plan_response_route_weights import PlatformTenantPlanResponseRouteWeights


T = TypeVar("T", bound="PlatformTenantPlanResponse")


@_attrs_define
class PlatformTenantPlanResponse:
    """One tenant plan."""

    id: UUID
    name: str
    currency: str
    price_millicents_per_unit: int
    included_units_per_month: int
    created_at: datetime.datetime
    updated_at: datetime.datetime
    tiers: list[APIConsumerRateCardTier] | Unset = UNSET
    route_weights: PlatformTenantPlanResponseRouteWeights | Unset = UNSET
    request_budget: PlatformTenantPlanBudget | Unset = UNSET
    """The request budget a tenant plan sets (ADR-240); zero disables a ceiling."""
    unit: PlatformTenantPlanResponseUnit | Unset = UNSET
    """What the plan's card prices (ADR-1310)."""
    tier_mode: PlatformTenantPlanResponseTierMode | Unset = UNSET
    """How the plan's ladder prices a month (ADR-1309)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        currency = self.currency

        price_millicents_per_unit = self.price_millicents_per_unit

        included_units_per_month = self.included_units_per_month

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        tiers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.tiers, Unset):
            tiers = []
            for tiers_item_data in self.tiers:
                tiers_item = tiers_item_data.to_dict()
                tiers.append(tiers_item)

        route_weights: dict[str, Any] | Unset = UNSET
        if not isinstance(self.route_weights, Unset):
            route_weights = self.route_weights.to_dict()

        request_budget: dict[str, Any] | Unset = UNSET
        if not isinstance(self.request_budget, Unset):
            request_budget = self.request_budget.to_dict()

        unit: str | Unset = UNSET
        if not isinstance(self.unit, Unset):
            unit = self.unit

        tier_mode: str | Unset = UNSET
        if not isinstance(self.tier_mode, Unset):
            tier_mode = self.tier_mode

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "currency": currency,
                "price_millicents_per_unit": price_millicents_per_unit,
                "included_units_per_month": included_units_per_month,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if tiers is not UNSET:
            field_dict["tiers"] = tiers
        if route_weights is not UNSET:
            field_dict["route_weights"] = route_weights
        if request_budget is not UNSET:
            field_dict["request_budget"] = request_budget
        if unit is not UNSET:
            field_dict["unit"] = unit
        if tier_mode is not UNSET:
            field_dict["tier_mode"] = tier_mode

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_rate_card_tier import APIConsumerRateCardTier
        from ..models.platform_tenant_plan_budget import PlatformTenantPlanBudget
        from ..models.platform_tenant_plan_response_route_weights import PlatformTenantPlanResponseRouteWeights

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        currency = d.pop("currency")

        price_millicents_per_unit = d.pop("price_millicents_per_unit")

        included_units_per_month = d.pop("included_units_per_month")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _tiers = d.pop("tiers", UNSET)
        tiers: list[APIConsumerRateCardTier] | Unset = UNSET
        if _tiers is not UNSET:
            tiers = []
            for tiers_item_data in _tiers:
                tiers_item = APIConsumerRateCardTier.from_dict(tiers_item_data)

                tiers.append(tiers_item)

        _route_weights = d.pop("route_weights", UNSET)
        route_weights: PlatformTenantPlanResponseRouteWeights | Unset
        if isinstance(_route_weights, Unset):
            route_weights = UNSET
        else:
            route_weights = PlatformTenantPlanResponseRouteWeights.from_dict(_route_weights)

        _request_budget = d.pop("request_budget", UNSET)
        request_budget: PlatformTenantPlanBudget | Unset
        if isinstance(_request_budget, Unset):
            request_budget = UNSET
        else:
            request_budget = PlatformTenantPlanBudget.from_dict(_request_budget)

        _unit = d.pop("unit", UNSET)
        unit: PlatformTenantPlanResponseUnit | Unset
        if isinstance(_unit, Unset):
            unit = UNSET
        else:
            unit = check_platform_tenant_plan_response_unit(_unit)

        _tier_mode = d.pop("tier_mode", UNSET)
        tier_mode: PlatformTenantPlanResponseTierMode | Unset
        if isinstance(_tier_mode, Unset):
            tier_mode = UNSET
        else:
            tier_mode = check_platform_tenant_plan_response_tier_mode(_tier_mode)

        platform_tenant_plan_response = cls(
            id=id,
            name=name,
            currency=currency,
            price_millicents_per_unit=price_millicents_per_unit,
            included_units_per_month=included_units_per_month,
            created_at=created_at,
            updated_at=updated_at,
            tiers=tiers,
            route_weights=route_weights,
            request_budget=request_budget,
            unit=unit,
            tier_mode=tier_mode,
        )

        platform_tenant_plan_response.additional_properties = d
        return platform_tenant_plan_response

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
