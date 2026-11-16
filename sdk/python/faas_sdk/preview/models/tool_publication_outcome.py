from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tool_publication_outcome_status import ToolPublicationOutcomeStatus, check_tool_publication_outcome_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.tool_publication_check import ToolPublicationCheck


T = TypeVar("T", bound="ToolPublicationOutcome")


@_attrs_define
class ToolPublicationOutcome:
    """Immutable publisher-attested evidence bounded to 512 KiB. Completed outcomes require the complete observed catalog
    and exact passing tests. Failed or uncertain outcomes contain only status and protocol_verified=false.

    """

    status: ToolPublicationOutcomeStatus
    protocol_verified: bool
    deployment_id: str | Unset = UNSET
    catalog_sha256: str | Unset = UNSET
    """Runtime integrity pin; separate from the caller-visible contract digest"""
    catalog_base64: str | Unset = UNSET
    """Canonical format-2 MCP contract bytes, at most 256 KiB decoded; base64 preserves byte identity through JSONB"""
    checks: list[ToolPublicationCheck] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        protocol_verified = self.protocol_verified

        deployment_id = self.deployment_id

        catalog_sha256 = self.catalog_sha256

        catalog_base64 = self.catalog_base64

        checks: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.checks, Unset):
            checks = []
            for checks_item_data in self.checks:
                checks_item = checks_item_data.to_dict()
                checks.append(checks_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "protocol_verified": protocol_verified,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if catalog_sha256 is not UNSET:
            field_dict["catalog_sha256"] = catalog_sha256
        if catalog_base64 is not UNSET:
            field_dict["catalog_base64"] = catalog_base64
        if checks is not UNSET:
            field_dict["checks"] = checks

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tool_publication_check import ToolPublicationCheck

        d = dict(src_dict)
        status = check_tool_publication_outcome_status(d.pop("status"))

        protocol_verified = d.pop("protocol_verified")

        deployment_id = d.pop("deployment_id", UNSET)

        catalog_sha256 = d.pop("catalog_sha256", UNSET)

        catalog_base64 = d.pop("catalog_base64", UNSET)

        _checks = d.pop("checks", UNSET)
        checks: list[ToolPublicationCheck] | Unset = UNSET
        if _checks is not UNSET:
            checks = []
            for checks_item_data in _checks:
                checks_item = ToolPublicationCheck.from_dict(checks_item_data)

                checks.append(checks_item)

        tool_publication_outcome = cls(
            status=status,
            protocol_verified=protocol_verified,
            deployment_id=deployment_id,
            catalog_sha256=catalog_sha256,
            catalog_base64=catalog_base64,
            checks=checks,
        )

        tool_publication_outcome.additional_properties = d
        return tool_publication_outcome

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
