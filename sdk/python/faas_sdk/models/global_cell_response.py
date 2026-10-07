from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="GlobalCellResponse")


@_attrs_define
class GlobalCellResponse:
    """Immutable registered cell identity with mutable management origin, regional apps domain and enabled policy.
    Generation fences verification.

    """

    id: UUID
    region: str
    api_base: str
    """Canonical HTTPS origin without credentials or path."""
    enabled: bool
    generation: int
    apps_domain: str | Unset = UNSET
    """Canonical regional apps DNS suffix; omitted when unconfigured. No port, scheme, literal IP or trailing dot."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        region = self.region

        api_base = self.api_base

        enabled = self.enabled

        generation = self.generation

        apps_domain = self.apps_domain

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "region": region,
                "api_base": api_base,
                "enabled": enabled,
                "generation": generation,
            }
        )
        if apps_domain is not UNSET:
            field_dict["apps_domain"] = apps_domain

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        region = d.pop("region")

        api_base = d.pop("api_base")

        enabled = d.pop("enabled")

        generation = d.pop("generation")

        apps_domain = d.pop("apps_domain", UNSET)

        global_cell_response = cls(
            id=id,
            region=region,
            api_base=api_base,
            enabled=enabled,
            generation=generation,
            apps_domain=apps_domain,
        )

        global_cell_response.additional_properties = d
        return global_cell_response

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
