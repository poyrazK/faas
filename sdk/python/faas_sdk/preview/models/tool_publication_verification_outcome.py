from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.tool_publication_role_result import ToolPublicationRoleResult
    from ..models.tool_publication_verification_outcome_checks_item import ToolPublicationVerificationOutcomeChecksItem


T = TypeVar("T", bound="ToolPublicationVerificationOutcome")


@_attrs_define
class ToolPublicationVerificationOutcome:
    """Platform-observed catalog and named test results without credentials or upstream response data."""

    ok: bool
    protocol_verified: bool
    checks: list[ToolPublicationVerificationOutcomeChecksItem]
    catalog_base64: str | Unset = UNSET
    """Canonical verified catalog; absent on failure"""
    readiness: list[ToolPublicationRoleResult] | Unset = UNSET
    code: str | Unset = UNSET
    """Stable verification failure stage without upstream response data"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ok = self.ok

        protocol_verified = self.protocol_verified

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        catalog_base64 = self.catalog_base64

        readiness: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.readiness, Unset):
            readiness = []
            for readiness_item_data in self.readiness:
                readiness_item = readiness_item_data.to_dict()
                readiness.append(readiness_item)

        code = self.code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ok": ok,
                "protocol_verified": protocol_verified,
                "checks": checks,
            }
        )
        if catalog_base64 is not UNSET:
            field_dict["catalog_base64"] = catalog_base64
        if readiness is not UNSET:
            field_dict["readiness"] = readiness
        if code is not UNSET:
            field_dict["code"] = code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tool_publication_role_result import ToolPublicationRoleResult
        from ..models.tool_publication_verification_outcome_checks_item import (
            ToolPublicationVerificationOutcomeChecksItem,
        )

        d = dict(src_dict)
        ok = d.pop("ok")

        protocol_verified = d.pop("protocol_verified")

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = ToolPublicationVerificationOutcomeChecksItem.from_dict(checks_item_data)

            checks.append(checks_item)

        catalog_base64 = d.pop("catalog_base64", UNSET)

        _readiness = d.pop("readiness", UNSET)
        readiness: list[ToolPublicationRoleResult] | Unset = UNSET
        if _readiness is not UNSET:
            readiness = []
            for readiness_item_data in _readiness:
                readiness_item = ToolPublicationRoleResult.from_dict(readiness_item_data)

                readiness.append(readiness_item)

        code = d.pop("code", UNSET)

        tool_publication_verification_outcome = cls(
            ok=ok,
            protocol_verified=protocol_verified,
            checks=checks,
            catalog_base64=catalog_base64,
            readiness=readiness,
            code=code,
        )

        tool_publication_verification_outcome.additional_properties = d
        return tool_publication_verification_outcome

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
