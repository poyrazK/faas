from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_plan_request_tier_mode import (
    PlatformTenantPlanRequestTierMode,
    check_platform_tenant_plan_request_tier_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_consumer_rate_card_tier import APIConsumerRateCardTier
    from ..models.platform_tenant_plan_budget import PlatformTenantPlanBudget
    from ..models.platform_tenant_plan_request_route_weights import PlatformTenantPlanRequestRouteWeights


T = TypeVar("T", bound="PlatformTenantPlanRequest")


@_attrs_define
class PlatformTenantPlanRequest:
    """A reusable tenant plan (ADR-1307): rate card terms and an optional request budget that assigning the plan applies."""

    name: str
    currency: str
    price_millicents_per_unit: int
    included_units_per_month: int | Unset = UNSET
    tiers: list[APIConsumerRateCardTier] | Unset = UNSET
    route_weights: PlatformTenantPlanRequestRouteWeights | Unset = UNSET
    request_budget: PlatformTenantPlanBudget | Unset = UNSET
    """The request budget a tenant plan sets (ADR-240); zero disables a ceiling."""
    unit: str | Unset = "request"
    """What the card a plan assignment creates prices (ADR-1310, ADR-1319). Assigning a plan also starts a free
    card for each other unit the tenant is charged for, so the plan is the tenant's whole price."""
    base_fee_millicents_per_month: int | Unset = UNSET
    """Fixed monthly charge per tenant the plan's card sets (ADR-1322)."""
    minimum_millicents_per_month: int | Unset = UNSET
    """Minimum monthly usage charge per tenant the plan's card sets (ADR-1322)."""
    prorate_monthly_fees: bool | Unset = False
    """Prorate the first month's fees (ADR-1325)."""
    tier_mode: PlatformTenantPlanRequestTierMode | Unset = "graduated"
    """How the plan's ladder prices a tenant's month (ADR-1309); volume needs tiers."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        currency = self.currency

        price_millicents_per_unit = self.price_millicents_per_unit

        included_units_per_month = self.included_units_per_month

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

        unit = self.unit

        base_fee_millicents_per_month = self.base_fee_millicents_per_month

        minimum_millicents_per_month = self.minimum_millicents_per_month

        prorate_monthly_fees = self.prorate_monthly_fees

        tier_mode: str | Unset = UNSET
        if not isinstance(self.tier_mode, Unset):
            tier_mode = self.tier_mode

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "currency": currency,
                "price_millicents_per_unit": price_millicents_per_unit,
            }
        )
        if included_units_per_month is not UNSET:
            field_dict["included_units_per_month"] = included_units_per_month
        if tiers is not UNSET:
            field_dict["tiers"] = tiers
        if route_weights is not UNSET:
            field_dict["route_weights"] = route_weights
        if request_budget is not UNSET:
            field_dict["request_budget"] = request_budget
        if unit is not UNSET:
            field_dict["unit"] = unit
        if base_fee_millicents_per_month is not UNSET:
            field_dict["base_fee_millicents_per_month"] = base_fee_millicents_per_month
        if minimum_millicents_per_month is not UNSET:
            field_dict["minimum_millicents_per_month"] = minimum_millicents_per_month
        if prorate_monthly_fees is not UNSET:
            field_dict["prorate_monthly_fees"] = prorate_monthly_fees
        if tier_mode is not UNSET:
            field_dict["tier_mode"] = tier_mode

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_rate_card_tier import APIConsumerRateCardTier
        from ..models.platform_tenant_plan_budget import PlatformTenantPlanBudget
        from ..models.platform_tenant_plan_request_route_weights import PlatformTenantPlanRequestRouteWeights

        d = dict(src_dict)
        name = d.pop("name")

        currency = d.pop("currency")

        price_millicents_per_unit = d.pop("price_millicents_per_unit")

        included_units_per_month = d.pop("included_units_per_month", UNSET)

        _tiers = d.pop("tiers", UNSET)
        tiers: list[APIConsumerRateCardTier] | Unset = UNSET
        if _tiers is not UNSET:
            tiers = []
            for tiers_item_data in _tiers:
                tiers_item = APIConsumerRateCardTier.from_dict(tiers_item_data)

                tiers.append(tiers_item)

        _route_weights = d.pop("route_weights", UNSET)
        route_weights: PlatformTenantPlanRequestRouteWeights | Unset
        if isinstance(_route_weights, Unset):
            route_weights = UNSET
        else:
            route_weights = PlatformTenantPlanRequestRouteWeights.from_dict(_route_weights)

        _request_budget = d.pop("request_budget", UNSET)
        request_budget: PlatformTenantPlanBudget | Unset
        if isinstance(_request_budget, Unset):
            request_budget = UNSET
        else:
            request_budget = PlatformTenantPlanBudget.from_dict(_request_budget)

        unit = d.pop("unit", UNSET)

        base_fee_millicents_per_month = d.pop("base_fee_millicents_per_month", UNSET)

        minimum_millicents_per_month = d.pop("minimum_millicents_per_month", UNSET)

        prorate_monthly_fees = d.pop("prorate_monthly_fees", UNSET)

        _tier_mode = d.pop("tier_mode", UNSET)
        tier_mode: PlatformTenantPlanRequestTierMode | Unset
        if isinstance(_tier_mode, Unset):
            tier_mode = UNSET
        else:
            tier_mode = check_platform_tenant_plan_request_tier_mode(_tier_mode)

        platform_tenant_plan_request = cls(
            name=name,
            currency=currency,
            price_millicents_per_unit=price_millicents_per_unit,
            included_units_per_month=included_units_per_month,
            tiers=tiers,
            route_weights=route_weights,
            request_budget=request_budget,
            unit=unit,
            base_fee_millicents_per_month=base_fee_millicents_per_month,
            minimum_millicents_per_month=minimum_millicents_per_month,
            prorate_monthly_fees=prorate_monthly_fees,
            tier_mode=tier_mode,
        )

        platform_tenant_plan_request.additional_properties = d
        return platform_tenant_plan_request

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
