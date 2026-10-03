from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_investigation_selection_customer_group_by import (
    RouteHealthInvestigationSelectionCustomerGroupBy,
    check_route_health_investigation_selection_customer_group_by,
)
from ..models.route_health_investigation_selection_status_code import (
    RouteHealthInvestigationSelectionStatusCode,
    check_route_health_investigation_selection_status_code,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteHealthInvestigationSelection")


@_attrs_define
class RouteHealthInvestigationSelection:
    """Exact configured route and signal, optionally scoped to a recorded customer identity."""

    method: str
    path: str
    status_code: RouteHealthInvestigationSelectionStatusCode
    customer_group_by: RouteHealthInvestigationSelectionCustomerGroupBy | Unset = UNSET
    customer_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method = self.method

        path = self.path

        status_code: int = self.status_code

        customer_group_by: str | Unset = UNSET
        if not isinstance(self.customer_group_by, Unset):
            customer_group_by = self.customer_group_by

        customer_id: str | Unset = UNSET
        if not isinstance(self.customer_id, Unset):
            customer_id = str(self.customer_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
                "status_code": status_code,
            }
        )
        if customer_group_by is not UNSET:
            field_dict["customer_group_by"] = customer_group_by
        if customer_id is not UNSET:
            field_dict["customer_id"] = customer_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        method = d.pop("method")

        path = d.pop("path")

        status_code = check_route_health_investigation_selection_status_code(d.pop("status_code"))

        _customer_group_by = d.pop("customer_group_by", UNSET)
        customer_group_by: RouteHealthInvestigationSelectionCustomerGroupBy | Unset
        if isinstance(_customer_group_by, Unset):
            customer_group_by = UNSET
        else:
            customer_group_by = check_route_health_investigation_selection_customer_group_by(_customer_group_by)

        _customer_id = d.pop("customer_id", UNSET)
        customer_id: UUID | Unset
        if isinstance(_customer_id, Unset):
            customer_id = UNSET
        else:
            customer_id = UUID(_customer_id)

        route_health_investigation_selection = cls(
            method=method,
            path=path,
            status_code=status_code,
            customer_group_by=customer_group_by,
            customer_id=customer_id,
        )

        route_health_investigation_selection.additional_properties = d
        return route_health_investigation_selection

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
