from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_api_consumer_rate_card_request_tier_mode import (
    CreateAPIConsumerRateCardRequestTierMode,
    check_create_api_consumer_rate_card_request_tier_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_consumer_rate_card_tier import APIConsumerRateCardTier
    from ..models.create_api_consumer_rate_card_request_route_weights import (
        CreateAPIConsumerRateCardRequestRouteWeights,
    )


T = TypeVar("T", bound="CreateAPIConsumerRateCardRequest")


@_attrs_define
class CreateAPIConsumerRateCardRequest:
    """Immutable app-level request price. Currency defaults to EUR and effective_from defaults to the next UTC minute."""

    price_millicents_per_unit: int
    currency: str | Unset = "EUR"
    included_units_per_month: int | Unset = 0
    """Free request units per consumer per UTC calendar month while this card is effective, consumed in minute
    order. Once any card includes units, effective_from cannot be in the past."""
    tiers: list[APIConsumerRateCardTier] | Unset = UNSET
    """Optional graduated ladder that replaces price_millicents_per_unit and included_units_per_month. Each
    consumer's units are counted per UTC calendar month in minute order and priced by the step their position falls
    in. Bounds increase strictly, only the last step is unbounded, and only the first step may be free. Statements
    of periods priced by a tiered card must cover exactly one UTC calendar month."""
    route_weights: CreateAPIConsumerRateCardRequestRouteWeights | Unset = UNSET
    """Counts each request on a listed "METHOD /template" route as that many units (1..1000, at most 50 routes);
    unlisted routes count 1. Weighted units feed included units, tiers, and statements. Route labels match the app's
    declared or discovered route templates."""
    plan_id: UUID | Unset = UNSET
    """Adds the version to a consumer plan's price history; omitted prices the app default plan. Plan cards cannot
    be backdated."""
    unit: str | Unset = "request"
    """What the card prices: requests; compute_second or response_megabyte, rounded per consumer minute; or
    app:<meter>, the units the app reports in its Gregale-Usage header. Cards of different units price side by side;
    non-request cards take no route weights and cannot be backdated."""
    base_fee_millicents_per_month: int | Unset = UNSET
    """Fixed charge per consumer per UTC month while this card is in force when the consumer's month starts."""
    minimum_millicents_per_month: int | Unset = UNSET
    """Minimum usage charge per consumer per UTC month; statements top up the difference."""
    prorate_monthly_fees: bool | Unset = False
    """Scale the base fee and minimum in a consumer's first month to the share of it left after the consumer was
    created."""
    tier_mode: CreateAPIConsumerRateCardRequestTierMode | Unset = "graduated"
    """How tiers price a consumer's month: graduated prices each unit at the step its position falls in; volume
    prices every unit of the month at the step the month's total reaches. volume needs tiers."""
    effective_from: datetime.datetime | None | Unset = UNSET
    """UTC minute at which this version starts; omitted means the next UTC minute."""
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

        plan_id: str | Unset = UNSET
        if not isinstance(self.plan_id, Unset):
            plan_id = str(self.plan_id)

        unit = self.unit

        base_fee_millicents_per_month = self.base_fee_millicents_per_month

        minimum_millicents_per_month = self.minimum_millicents_per_month

        prorate_monthly_fees = self.prorate_monthly_fees

        tier_mode: str | Unset = UNSET
        if not isinstance(self.tier_mode, Unset):
            tier_mode = self.tier_mode

        effective_from: None | str | Unset
        if isinstance(self.effective_from, Unset):
            effective_from = UNSET
        elif isinstance(self.effective_from, datetime.datetime):
            effective_from = self.effective_from.isoformat()
        else:
            effective_from = self.effective_from

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
        if plan_id is not UNSET:
            field_dict["plan_id"] = plan_id
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
        if effective_from is not UNSET:
            field_dict["effective_from"] = effective_from

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_rate_card_tier import APIConsumerRateCardTier
        from ..models.create_api_consumer_rate_card_request_route_weights import (
            CreateAPIConsumerRateCardRequestRouteWeights,
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
        route_weights: CreateAPIConsumerRateCardRequestRouteWeights | Unset
        if isinstance(_route_weights, Unset):
            route_weights = UNSET
        else:
            route_weights = CreateAPIConsumerRateCardRequestRouteWeights.from_dict(_route_weights)

        _plan_id = d.pop("plan_id", UNSET)
        plan_id: UUID | Unset
        if isinstance(_plan_id, Unset):
            plan_id = UNSET
        else:
            plan_id = UUID(_plan_id)

        unit = d.pop("unit", UNSET)

        base_fee_millicents_per_month = d.pop("base_fee_millicents_per_month", UNSET)

        minimum_millicents_per_month = d.pop("minimum_millicents_per_month", UNSET)

        prorate_monthly_fees = d.pop("prorate_monthly_fees", UNSET)

        _tier_mode = d.pop("tier_mode", UNSET)
        tier_mode: CreateAPIConsumerRateCardRequestTierMode | Unset
        if isinstance(_tier_mode, Unset):
            tier_mode = UNSET
        else:
            tier_mode = check_create_api_consumer_rate_card_request_tier_mode(_tier_mode)

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

        create_api_consumer_rate_card_request = cls(
            price_millicents_per_unit=price_millicents_per_unit,
            currency=currency,
            included_units_per_month=included_units_per_month,
            tiers=tiers,
            route_weights=route_weights,
            plan_id=plan_id,
            unit=unit,
            base_fee_millicents_per_month=base_fee_millicents_per_month,
            minimum_millicents_per_month=minimum_millicents_per_month,
            prorate_monthly_fees=prorate_monthly_fees,
            tier_mode=tier_mode,
            effective_from=effective_from,
        )

        create_api_consumer_rate_card_request.additional_properties = d
        return create_api_consumer_rate_card_request

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
