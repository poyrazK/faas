from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_catalog_observation_request_observed import MCPCatalogObservationRequestObserved


T = TypeVar("T", bound="MCPCatalogObservationRequest")


@_attrs_define
class MCPCatalogObservationRequest:
    """Complete role catalog or an unavailable observation bound to its reviewed revision."""

    revision: UUID
    unavailable: bool
    role: str | Unset = "default"
    observed: MCPCatalogObservationRequestObserved | Unset = UNSET
    """Complete observed format-2 contract; omit when unavailable is true"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        revision = str(self.revision)

        unavailable = self.unavailable

        role = self.role

        observed: dict[str, Any] | Unset = UNSET
        if not isinstance(self.observed, Unset):
            observed = self.observed.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "revision": revision,
                "unavailable": unavailable,
            }
        )
        if role is not UNSET:
            field_dict["role"] = role
        if observed is not UNSET:
            field_dict["observed"] = observed

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_catalog_observation_request_observed import MCPCatalogObservationRequestObserved

        d = dict(src_dict)
        revision = UUID(d.pop("revision"))

        unavailable = d.pop("unavailable")

        role = d.pop("role", UNSET)

        _observed = d.pop("observed", UNSET)
        observed: MCPCatalogObservationRequestObserved | Unset
        if isinstance(_observed, Unset):
            observed = UNSET
        else:
            observed = MCPCatalogObservationRequestObserved.from_dict(_observed)

        mcp_catalog_observation_request = cls(
            revision=revision,
            unavailable=unavailable,
            role=role,
            observed=observed,
        )

        mcp_catalog_observation_request.additional_properties = d
        return mcp_catalog_observation_request

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
