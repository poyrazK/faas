from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.domain_manifest import DomainManifest


T = TypeVar("T", bound="DomainApplyRequest")


@_attrs_define
class DomainApplyRequest:
    """Apply exactly the reviewed bindings. Confirmation and the fingerprint from planDomains are required."""

    manifest: DomainManifest
    """Versioned desired bindings. Only listed domains change; omission never removes a binding. At most 20
    bindings in a 128 KiB request. Use a separate file from the application deployment manifest."""
    expected_plan_sha256: str
    confirm: bool

    def to_dict(self) -> dict[str, Any]:
        manifest = self.manifest.to_dict()

        expected_plan_sha256 = self.expected_plan_sha256

        confirm = self.confirm

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "manifest": manifest,
                "expected_plan_sha256": expected_plan_sha256,
                "confirm": confirm,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.domain_manifest import DomainManifest

        d = dict(src_dict)
        manifest = DomainManifest.from_dict(d.pop("manifest"))

        expected_plan_sha256 = d.pop("expected_plan_sha256")

        confirm = d.pop("confirm")

        domain_apply_request = cls(
            manifest=manifest,
            expected_plan_sha256=expected_plan_sha256,
            confirm=confirm,
        )

        return domain_apply_request
