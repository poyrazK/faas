from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DomainBindingSpec")


@_attrs_define
class DomainBindingSpec:
    """A desired exact or wildcard hostname, app slug and optional project environment. An omitted environment selects
    ordinary default routing.

    """

    domain: str
    app: str
    environment: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        domain = self.domain

        app = self.app

        environment = self.environment

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "domain": domain,
                "app": app,
            }
        )
        if environment is not UNSET:
            field_dict["environment"] = environment

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        domain = d.pop("domain")

        app = d.pop("app")

        environment = d.pop("environment", UNSET)

        domain_binding_spec = cls(
            domain=domain,
            app=app,
            environment=environment,
        )

        return domain_binding_spec
