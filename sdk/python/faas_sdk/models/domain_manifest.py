from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.domain_manifest_version import DomainManifestVersion, check_domain_manifest_version

if TYPE_CHECKING:
    from ..models.domain_binding_spec import DomainBindingSpec


T = TypeVar("T", bound="DomainManifest")


@_attrs_define
class DomainManifest:
    """Versioned desired bindings. Only listed domains change; omission never removes a binding. At most 20 bindings in a
    128 KiB request. Use a separate file from the application deployment manifest.

    """

    version: DomainManifestVersion
    domains: list[DomainBindingSpec]

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        domains = []
        for domains_item_data in self.domains:
            domains_item = domains_item_data.to_dict()
            domains.append(domains_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "domains": domains,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.domain_binding_spec import DomainBindingSpec

        d = dict(src_dict)
        version = check_domain_manifest_version(d.pop("version"))

        domains = []
        _domains = d.pop("domains")
        for domains_item_data in _domains:
            domains_item = DomainBindingSpec.from_dict(domains_item_data)

            domains.append(domains_item)

        domain_manifest = cls(
            version=version,
            domains=domains,
        )

        return domain_manifest
