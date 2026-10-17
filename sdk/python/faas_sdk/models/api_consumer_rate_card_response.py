from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_consumer_rate_card_response_tier_mode import (
    APIConsumerRateCardResponseTierMode,
    check_api_consumer_rate_card_response_tier_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_consumer_rate_card_response_route_weights import APIConsumerRateCardResponseRouteWeights
    from ..models.api_consumer_rate_card_tier import APIConsumerRateCardTier


T = TypeVar("T", bound="APIConsumerRateCardResponse")


@_attrs_define
class APIConsumerRateCardResponse:
    """Immutable, versioned app-level price for one API request unit."""

    id: UUID
    app_id: UUID
    currency: str
    unit: str
    """What the card prices (ADR-1308, ADR-1318): billable requests, their compute seconds or response megabytes,
    or the units of an app meter."""
    price_millicents_per_unit: int
    included_units_per_month: int
    effective_from: datetime.datetime
    created_at: datetime.datetime
    base_fee_millicents_per_month: int | Unset = UNSET
    """The card's fixed monthly charge per consumer (ADR-1316)."""
    minimum_millicents_per_month: int | Unset = UNSET
    """The card's minimum monthly usage charge per consumer (ADR-1316)."""
    tiers: list[APIConsumerRateCardTier] | Unset = UNSET
    tier_mode: APIConsumerRateCardResponseTierMode | Unset = UNSET
    """How the ladder prices a consumer's month (ADR-1309)."""
    route_weights: APIConsumerRateCardResponseRouteWeights | Unset = UNSET
    """Units charged per request on a listed route; unlisted routes count 1."""
    plan_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        currency = self.currency

        unit = self.unit

        price_millicents_per_unit = self.price_millicents_per_unit

        included_units_per_month = self.included_units_per_month

        effective_from = self.effective_from.isoformat()

        created_at = self.created_at.isoformat()

        base_fee_millicents_per_month = self.base_fee_millicents_per_month

        minimum_millicents_per_month = self.minimum_millicents_per_month

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

        plan_id: str | Unset = UNSET
        if not isinstance(self.plan_id, Unset):
            plan_id = str(self.plan_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
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
        if tiers is not UNSET:
            field_dict["tiers"] = tiers
        if tier_mode is not UNSET:
            field_dict["tier_mode"] = tier_mode
        if route_weights is not UNSET:
            field_dict["route_weights"] = route_weights
        if plan_id is not UNSET:
            field_dict["plan_id"] = plan_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_rate_card_response_route_weights import APIConsumerRateCardResponseRouteWeights
        from ..models.api_consumer_rate_card_tier import APIConsumerRateCardTier

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        currency = d.pop("currency")

        unit = d.pop("unit")

        price_millicents_per_unit = d.pop("price_millicents_per_unit")

        included_units_per_month = d.pop("included_units_per_month")

        effective_from = datetime.datetime.fromisoformat(d.pop("effective_from"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        base_fee_millicents_per_month = d.pop("base_fee_millicents_per_month", UNSET)

        minimum_millicents_per_month = d.pop("minimum_millicents_per_month", UNSET)

        _tiers = d.pop("tiers", UNSET)
        tiers: list[APIConsumerRateCardTier] | Unset = UNSET
        if _tiers is not UNSET:
            tiers = []
            for tiers_item_data in _tiers:
                tiers_item = APIConsumerRateCardTier.from_dict(tiers_item_data)

                tiers.append(tiers_item)

        _tier_mode = d.pop("tier_mode", UNSET)
        tier_mode: APIConsumerRateCardResponseTierMode | Unset
        if isinstance(_tier_mode, Unset):
            tier_mode = UNSET
        else:
            tier_mode = check_api_consumer_rate_card_response_tier_mode(_tier_mode)

        _route_weights = d.pop("route_weights", UNSET)
        route_weights: APIConsumerRateCardResponseRouteWeights | Unset
        if isinstance(_route_weights, Unset):
            route_weights = UNSET
        else:
            route_weights = APIConsumerRateCardResponseRouteWeights.from_dict(_route_weights)

        _plan_id = d.pop("plan_id", UNSET)
        plan_id: UUID | Unset
        if isinstance(_plan_id, Unset):
            plan_id = UNSET
        else:
            plan_id = UUID(_plan_id)

        api_consumer_rate_card_response = cls(
            id=id,
            app_id=app_id,
            currency=currency,
            unit=unit,
            price_millicents_per_unit=price_millicents_per_unit,
            included_units_per_month=included_units_per_month,
            effective_from=effective_from,
            created_at=created_at,
            base_fee_millicents_per_month=base_fee_millicents_per_month,
            minimum_millicents_per_month=minimum_millicents_per_month,
            tiers=tiers,
            tier_mode=tier_mode,
            route_weights=route_weights,
            plan_id=plan_id,
        )

        api_consumer_rate_card_response.additional_properties = d
        return api_consumer_rate_card_response

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
