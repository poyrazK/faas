from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.deployment_audit_response import DeploymentAuditResponse


T = TypeVar("T", bound="ListDeploymentAuditResponse")


@_attrs_define
class ListDeploymentAuditResponse:
    """Paginated wrapper for `GET /v1/deployments/{id}/audit`
    (issue #976 / ADR-122 / SAFE-RELEASES-E.2 + production-
    leveling Stream A). Limit is echoed back so a paging
    consumer can distinguish "limit was clamped" from "no
    more rows" — both yield Items of length < limit, but the
    clamping is observable via this field.

    """

    items: list[DeploymentAuditResponse]
    limit: int
    """Echo of the server-applied limit (query param ?limit= clamps here)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        limit = self.limit

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
                "limit": limit,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deployment_audit_response import DeploymentAuditResponse

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = DeploymentAuditResponse.from_dict(items_item_data)

            items.append(items_item)

        limit = d.pop("limit")

        list_deployment_audit_response = cls(
            items=items,
            limit=limit,
        )

        list_deployment_audit_response.additional_properties = d
        return list_deployment_audit_response

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
