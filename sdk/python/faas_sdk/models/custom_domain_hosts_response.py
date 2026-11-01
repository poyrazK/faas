from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="CustomDomainHostsResponse")


@_attrs_define
class CustomDomainHostsResponse:
    """A wildcard custom domain's host allowlist. Names are the parts of hosts before the wildcard's suffix.

    """

    domain: str
    names: list[str]
    restricted: bool
    """False when no allowlist is set: every name below the wildcard may hold a certificate."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        domain = self.domain

        names = self.names

        restricted = self.restricted

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "domain": domain,
                "names": names,
                "restricted": restricted,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        domain = d.pop("domain")

        names = cast(list[str], d.pop("names"))

        restricted = d.pop("restricted")

        custom_domain_hosts_response = cls(
            domain=domain,
            names=names,
            restricted=restricted,
        )

        custom_domain_hosts_response.additional_properties = d
        return custom_domain_hosts_response

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
