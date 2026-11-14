from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.domain_readiness_ownership import DomainReadinessOwnership, check_domain_readiness_ownership
from ..models.domain_readiness_routing import DomainReadinessRouting, check_domain_readiness_routing
from ..models.domain_readiness_status import DomainReadinessStatus, check_domain_readiness_status

T = TypeVar("T", bound="DomainReadiness")


@_attrs_define
class DomainReadiness:
    """Independent ownership, DNS routing, and TLS activation state."""

    status: DomainReadinessStatus
    ownership: DomainReadinessOwnership
    routing: DomainReadinessRouting
    tls: str
    """Certificate state, independently of ownership and routing."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        ownership: str = self.ownership

        routing: str = self.routing

        tls = self.tls

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "ownership": ownership,
                "routing": routing,
                "tls": tls,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_domain_readiness_status(d.pop("status"))

        ownership = check_domain_readiness_ownership(d.pop("ownership"))

        routing = check_domain_readiness_routing(d.pop("routing"))

        tls = d.pop("tls")

        domain_readiness = cls(
            status=status,
            ownership=ownership,
            routing=routing,
            tls=tls,
        )

        domain_readiness.additional_properties = d
        return domain_readiness

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
