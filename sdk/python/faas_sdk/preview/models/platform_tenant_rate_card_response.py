from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_rate_card_response_tier_mode import (
    PlatformTenantRateCardResponseTierMode,
    check_platform_tenant_rate_card_response_tier_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_consumer_rate_card_tier import APIConsumerRateCardTier
    from ..models.platform_tenant_rate_card_response_route_weights import PlatformTenantRateCardResponseRouteWeights


T = TypeVar("T", bound="PlatformTenantRateCardResponse")


@_attrs_define
class PlatformTenantRateCardResponse:
    """Immutable, versioned customer price shared across every app attributed to one platform tenant."""

    id: UUID
    tenant_id: UUID
    currency: str
    unit: str
    """What the tenant card prices."""
    price_millicents_per_unit: int
    included_units_per_month: int
    """Free units per tenant per UTC calendar month."""
    effective_from: datetime.datetime
    created_at: datetime.datetime
    base_fee_millicents_per_month: int | Unset = UNSET
    """The card's fixed monthly charge per tenant."""
    minimum_millicents_per_month: int | Unset = UNSET
    """The card's minimum monthly usage charge per tenant."""
    prorate_monthly_fees: bool | Unset = UNSET
    """Whether the first month's fees are prorated."""
    tiers: list[APIConsumerRateCardTier] | Unset = UNSET
    """Ladder counted per tenant per UTC calendar month; absent for flat cards."""
    tier_mode: PlatformTenantRateCardResponseTierMode | Unset = UNSET
    """How the tenant ladder prices a month."""
    route_weights: PlatformTenantRateCardResponseRouteWeights | Unset = UNSET
    """Units per request on a route; absent when every request counts 1."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        tenant_id = str(self.tenant_id)

        currency = self.currency

        unit = self.unit

        price_millicents_per_unit = self.price_millicents_per_unit

        included_units_per_month = self.included_units_per_month

        effective_from = self.effective_from.isoformat()

        created_at = self.created_at.isoformat()

        base_fee_millicents_per_month = self.base_fee_millicents_per_month

        minimum_millicents_per_month = self.minimum_millicents_per_month

        prorate_monthly_fees = self.prorate_monthly_fees

        tiers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.tiers, Unset):
            tiers = []
            for tiers_item_data in self.tiers:
                tiers_item = tiers_item_data.to_dict()
                tiers.append(tiers_item)

        tier_mode: str | Unset = UNSET
        if not isinstance(self.tier_mode, Unset):
            tier_mode = self.tier_mode

        route_weights: dict[str, Any] | Unset = UNSET
        if not isinstance(self.route_weights, Unset):
            route_weights = self.route_weights.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "tenant_id": tenant_id,
                "currency": currency,
                "unit": unit,
                "price_millicents_per_unit": price_millicents_per_unit,
                "included_units_per_month": included_units_per_month,
                "effective_from": effective_from,
                "created_at": created_at,
            }
        )
        if base_fee_millicents_per_month is not UNSET:
            field_dict["base_fee_millicents_per_month"] = base_fee_millicents_per_month
        if minimum_millicents_per_month is not UNSET:
            field_dict["minimum_millicents_per_month"] = minimum_millicents_per_month
        if prorate_monthly_fees is not UNSET:
            field_dict["prorate_monthly_fees"] = prorate_monthly_fees
        if tiers is not UNSET:
            field_dict["tiers"] = tiers
        if tier_mode is not UNSET:
            field_dict["tier_mode"] = tier_mode
        if route_weights is not UNSET:
            field_dict["route_weights"] = route_weights

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_rate_card_tier import APIConsumerRateCardTier
        from ..models.platform_tenant_rate_card_response_route_weights import PlatformTenantRateCardResponseRouteWeights

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        tenant_id = UUID(d.pop("tenant_id"))

        currency = d.pop("currency")

        unit = d.pop("unit")

        price_millicents_per_unit = d.pop("price_millicents_per_unit")

        included_units_per_month = d.pop("included_units_per_month")

        effective_from = datetime.datetime.fromisoformat(d.pop("effective_from"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        base_fee_millicents_per_month = d.pop("base_fee_millicents_per_month", UNSET)

        minimum_millicents_per_month = d.pop("minimum_millicents_per_month", UNSET)

        prorate_monthly_fees = d.pop("prorate_monthly_fees", UNSET)

        _tiers = d.pop("tiers", UNSET)
        tiers: list[APIConsumerRateCardTier] | Unset = UNSET
        if _tiers is not UNSET:
            tiers = []
            for tiers_item_data in _tiers:
                tiers_item = APIConsumerRateCardTier.from_dict(tiers_item_data)

                tiers.append(tiers_item)

        _tier_mode = d.pop("tier_mode", UNSET)
        tier_mode: PlatformTenantRateCardResponseTierMode | Unset
        if isinstance(_tier_mode, Unset):
            tier_mode = UNSET
        else:
            tier_mode = check_platform_tenant_rate_card_response_tier_mode(_tier_mode)

        _route_weights = d.pop("route_weights", UNSET)
        route_weights: PlatformTenantRateCardResponseRouteWeights | Unset
        if isinstance(_route_weights, Unset):
            route_weights = UNSET
        else:
            route_weights = PlatformTenantRateCardResponseRouteWeights.from_dict(_route_weights)

        platform_tenant_rate_card_response = cls(
            id=id,
            tenant_id=tenant_id,
            currency=currency,
            unit=unit,
            price_millicents_per_unit=price_millicents_per_unit,
            included_units_per_month=included_units_per_month,
            effective_from=effective_from,
            created_at=created_at,
            base_fee_millicents_per_month=base_fee_millicents_per_month,
            minimum_millicents_per_month=minimum_millicents_per_month,
            prorate_monthly_fees=prorate_monthly_fees,
            tiers=tiers,
            tier_mode=tier_mode,
            route_weights=route_weights,
        )

        platform_tenant_rate_card_response.additional_properties = d
        return platform_tenant_rate_card_response

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
