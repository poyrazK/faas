from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_health_finding import RouteHealthFinding


T = TypeVar("T", bound="RouteCustomerHealthCohort")


@_attrs_define
class RouteCustomerHealthCohort:
    """One observed tenant or API consumer, compared independently with the same route thresholds and windows. Sparse or
    one-sided evidence is unknown.

    """

    health: RouteHealthFinding
    """Combined verdict and both closed-window evidence records for one selected critical route."""
    customer_id: UUID | Unset = UNSET
    """Present only when customer_details=true. Identity names and external references are excluded."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        health = self.health.to_dict()

        customer_id: str | Unset = UNSET
        if not isinstance(self.customer_id, Unset):
            customer_id = str(self.customer_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "health": health,
            }
        )
        if customer_id is not UNSET:
            field_dict["customer_id"] = customer_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_finding import RouteHealthFinding

        d = dict(src_dict)
        health = RouteHealthFinding.from_dict(d.pop("health"))

        _customer_id = d.pop("customer_id", UNSET)
        customer_id: UUID | Unset
        if isinstance(_customer_id, Unset):
            customer_id = UNSET
        else:
            customer_id = UUID(_customer_id)

        route_customer_health_cohort = cls(
            health=health,
            customer_id=customer_id,
        )

        route_customer_health_cohort.additional_properties = d
        return route_customer_health_cohort

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
