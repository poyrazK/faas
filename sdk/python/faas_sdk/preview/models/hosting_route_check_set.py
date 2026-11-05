from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.hosting_route_check_set_source import HostingRouteCheckSetSource, check_hosting_route_check_set_source
from ..models.hosting_route_check_set_status import HostingRouteCheckSetStatus, check_hosting_route_check_set_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.hosting_route_check_result import HostingRouteCheckResult


T = TypeVar("T", bound="HostingRouteCheckSet")


@_attrs_define
class HostingRouteCheckSet:
    """API route checks from the app's imported OpenAPI document. document_sha256 binds them to that exact document."""

    source: HostingRouteCheckSetSource
    status: HostingRouteCheckSetStatus
    checks: list[HostingRouteCheckResult]
    document_sha256: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source: str = self.source

        status: str = self.status

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        document_sha256 = self.document_sha256

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "status": status,
                "checks": checks,
            }
        )
        if document_sha256 is not UNSET:
            field_dict["document_sha256"] = document_sha256

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.hosting_route_check_result import HostingRouteCheckResult

        d = dict(src_dict)
        source = check_hosting_route_check_set_source(d.pop("source"))

        status = check_hosting_route_check_set_status(d.pop("status"))

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = HostingRouteCheckResult.from_dict(checks_item_data)

            checks.append(checks_item)

        document_sha256 = d.pop("document_sha256", UNSET)

        hosting_route_check_set = cls(
            source=source,
            status=status,
            checks=checks,
            document_sha256=document_sha256,
        )

        hosting_route_check_set.additional_properties = d
        return hosting_route_check_set

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
