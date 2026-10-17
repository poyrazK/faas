from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_platform_tenant_rate_card_request_tier_mode import (
    CreatePlatformTenantRateCardRequestTierMode,
    check_create_platform_tenant_rate_card_request_tier_mode,
)
from ..models.create_platform_tenant_rate_card_request_unit import (
    CreatePlatformTenantRateCardRequestUnit,
    check_create_platform_tenant_rate_card_request_unit,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_consumer_rate_card_tier import APIConsumerRateCardTier
    from ..models.create_platform_tenant_rate_card_request_route_weights import (
        CreatePlatformTenantRateCardRequestRouteWeights,
    )


T = TypeVar("T", bound="CreatePlatformTenantRateCardRequest")


@_attrs_define
class CreatePlatformTenantRateCardRequest:
    """Immutable tenant-wide price per attributed request unit. Currency defaults to EUR and effective_from defaults to the
    next UTC minute.

    """

    price_millicents_per_unit: int
    currency: str | Unset = "EUR"
    included_units_per_month: int | Unset = UNSET
    """Free request units per tenant per UTC calendar month across every attributed app, consumed in minute order
    (ADR-975). Statements for periods it prices must cover one calendar month."""
    tiers: list[APIConsumerRateCardTier] | Unset = UNSET
    """Optional graduated ladder counted per tenant per UTC calendar month across every attributed app (ADR-975).
    Replaces price_millicents_per_unit and included_units_per_month; the last step's up_to is null."""
    route_weights: CreatePlatformTenantRateCardRequestRouteWeights | Unset = UNSET
    """Counts each request on a listed "METHOD /template" route as that many units (1..1000, at most 50 routes;
    ADR-1307), including surface and JWT-rule requests (ADR-1310). Unlisted routes count 1. Statements for periods
    it prices must cover one calendar month."""
    effective_from: datetime.datetime | None | Unset = UNSET
    """UTC minute at which the version starts; omitted means the next UTC minute. Cannot be in the past once any
    tenant card has included units, tiers, route weights, or a compute or response-size unit."""
    unit: CreatePlatformTenantRateCardRequestUnit | Unset = "request"
    """What the card prices (ADR-1310): requests, or the compute seconds or response megabytes of billable
    requests, rounded per subject minute. Compute and response-size cards take no route weights, are month-priced,
    cannot be backdated, and a change of unit starts on the 1st of a month at 00:00 UTC."""
    tier_mode: CreatePlatformTenantRateCardRequestTierMode | Unset = "graduated"
    """How the tenant ladder prices a month (ADR-1309): graduated by each unit's position, or volume, every unit of
    the month at the step the tenant's total reaches. volume needs tiers."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        price_millicents_per_unit = self.price_millicents_per_unit

        currency = self.currency

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

        effective_from: None | str | Unset
        if isinstance(self.effective_from, Unset):
            effective_from = UNSET
        elif isinstance(self.effective_from, datetime.datetime):
            effective_from = self.effective_from.isoformat()
        else:
            effective_from = self.effective_from

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
                "price_millicents_per_unit": price_millicents_per_unit,
            }
        )
        if currency is not UNSET:
            field_dict["currency"] = currency
        if included_units_per_month is not UNSET:
            field_dict["included_units_per_month"] = included_units_per_month
        if tiers is not UNSET:
            field_dict["tiers"] = tiers
        if route_weights is not UNSET:
            field_dict["route_weights"] = route_weights
        if effective_from is not UNSET:
            field_dict["effective_from"] = effective_from
        if unit is not UNSET:
            field_dict["unit"] = unit
        if tier_mode is not UNSET:
            field_dict["tier_mode"] = tier_mode

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_rate_card_tier import APIConsumerRateCardTier
        from ..models.create_platform_tenant_rate_card_request_route_weights import (
            CreatePlatformTenantRateCardRequestRouteWeights,
        )

        d = dict(src_dict)
        price_millicents_per_unit = d.pop("price_millicents_per_unit")

        currency = d.pop("currency", UNSET)

        included_units_per_month = d.pop("included_units_per_month", UNSET)

        _tiers = d.pop("tiers", UNSET)
        tiers: list[APIConsumerRateCardTier] | Unset = UNSET
        if _tiers is not UNSET:
            tiers = []
            for tiers_item_data in _tiers:
                tiers_item = APIConsumerRateCardTier.from_dict(tiers_item_data)

                tiers.append(tiers_item)

        _route_weights = d.pop("route_weights", UNSET)
        route_weights: CreatePlatformTenantRateCardRequestRouteWeights | Unset
        if isinstance(_route_weights, Unset):
            route_weights = UNSET
        else:
            route_weights = CreatePlatformTenantRateCardRequestRouteWeights.from_dict(_route_weights)

        def _parse_effective_from(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                effective_from_type_0 = datetime.datetime.fromisoformat(data)

                return effective_from_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        effective_from = _parse_effective_from(d.pop("effective_from", UNSET))

        _unit = d.pop("unit", UNSET)
        unit: CreatePlatformTenantRateCardRequestUnit | Unset
        if isinstance(_unit, Unset):
            unit = UNSET
        else:
            unit = check_create_platform_tenant_rate_card_request_unit(_unit)

        _tier_mode = d.pop("tier_mode", UNSET)
        tier_mode: CreatePlatformTenantRateCardRequestTierMode | Unset
        if isinstance(_tier_mode, Unset):
            tier_mode = UNSET
        else:
            tier_mode = check_create_platform_tenant_rate_card_request_tier_mode(_tier_mode)

        create_platform_tenant_rate_card_request = cls(
            price_millicents_per_unit=price_millicents_per_unit,
            currency=currency,
            included_units_per_month=included_units_per_month,
            tiers=tiers,
            route_weights=route_weights,
            effective_from=effective_from,
            unit=unit,
            tier_mode=tier_mode,
        )

        create_platform_tenant_rate_card_request.additional_properties = d
        return create_platform_tenant_rate_card_request

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
