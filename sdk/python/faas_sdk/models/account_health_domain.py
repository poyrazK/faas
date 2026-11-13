from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AccountHealthDomain")


@_attrs_define
class AccountHealthDomain:
    """A custom domain that is not serving correctly."""

    domain: str
    """The custom hostname."""
    problem: str
    """What is wrong with the hostname."""
    app_slug: str | Unset = UNSET
    """Slug of the app the hostname routes to."""
    cert_error: str | Unset = UNSET
    """The certificate authority's last error, when issuance failed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        domain = self.domain

        problem = self.problem

        app_slug = self.app_slug

        cert_error = self.cert_error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "domain": domain,
                "problem": problem,
            }
        )
        if app_slug is not UNSET:
            field_dict["app_slug"] = app_slug
        if cert_error is not UNSET:
            field_dict["cert_error"] = cert_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        domain = d.pop("domain")

        problem = d.pop("problem")

        app_slug = d.pop("app_slug", UNSET)

        cert_error = d.pop("cert_error", UNSET)

        account_health_domain = cls(
            domain=domain,
            problem=problem,
            app_slug=app_slug,
            cert_error=cert_error,
        )

        account_health_domain.additional_properties = d
        return account_health_domain

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
