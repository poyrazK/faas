from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateCustomDomainRequest")


@_attrs_define
class CreateCustomDomainRequest:
    """Bind a custom domain to an app, optionally routing it to one project environment."""

    domain: str
    app_id: str
    environment: str | Unset = UNSET
    """Optional project environment slug. When set, traffic follows only that environment's active release graph."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        domain = self.domain

        app_id = self.app_id

        environment = self.environment

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "domain": domain,
                "app_id": app_id,
            }
        )
        if environment is not UNSET:
            field_dict["environment"] = environment

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        domain = d.pop("domain")

        app_id = d.pop("app_id")

        environment = d.pop("environment", UNSET)

        create_custom_domain_request = cls(
            domain=domain,
            app_id=app_id,
            environment=environment,
        )

        create_custom_domain_request.additional_properties = d
        return create_custom_domain_request

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
