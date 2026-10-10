from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_protection_response_range import EdgeProtectionResponseRange, check_edge_protection_response_range

if TYPE_CHECKING:
    from ..models.edge_protection_response_pre_auth import EdgeProtectionResponsePreAuth
    from ..models.edge_protection_response_rejections_item import EdgeProtectionResponseRejectionsItem
    from ..models.edge_protection_response_validation_failures_item import EdgeProtectionResponseValidationFailuresItem
    from ..models.edge_protection_response_waf import EdgeProtectionResponseWaf


T = TypeVar("T", bound="EdgeProtectionResponse")


@_attrs_define
class EdgeProtectionResponse:
    """Edge rejection counts for one app over a range."""

    app_id: str
    range_: EdgeProtectionResponseRange
    source: str
    """Where the edge counts come from: prometheus, or degraded: <reason> when they are unavailable (counts are
    then zero, not measured)."""
    as_of: datetime.datetime
    pre_auth: EdgeProtectionResponsePreAuth
    validation_failures: list[EdgeProtectionResponseValidationFailuresItem]
    """kind=validate mismatches by validate_mode (block, observe, warn); zero counts are omitted."""
    rejections: list[EdgeProtectionResponseRejectionsItem]
    """Requests answered by an edge gate, largest count first; zero counts are omitted."""
    waf: EdgeProtectionResponseWaf
    """kind=waf inspections (ADR-831). Sampled detections are never blocked; block rules' 403s also appear in
    rejections under gate waf."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        range_: str = self.range_

        source = self.source

        as_of = self.as_of.isoformat()

        pre_auth = self.pre_auth.to_dict()

        validation_failures = []
        for validation_failures_item_data in self.validation_failures:
            validation_failures_item = validation_failures_item_data.to_dict()
            validation_failures.append(validation_failures_item)

        rejections = []
        for rejections_item_data in self.rejections:
            rejections_item = rejections_item_data.to_dict()
            rejections.append(rejections_item)

        waf = self.waf.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "range": range_,
                "source": source,
                "as_of": as_of,
                "pre_auth": pre_auth,
                "validation_failures": validation_failures,
                "rejections": rejections,
                "waf": waf,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_protection_response_pre_auth import EdgeProtectionResponsePreAuth
        from ..models.edge_protection_response_rejections_item import EdgeProtectionResponseRejectionsItem
        from ..models.edge_protection_response_validation_failures_item import (
            EdgeProtectionResponseValidationFailuresItem,
        )
        from ..models.edge_protection_response_waf import EdgeProtectionResponseWaf

        d = dict(src_dict)
        app_id = d.pop("app_id")

        range_ = check_edge_protection_response_range(d.pop("range"))

        source = d.pop("source")

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        pre_auth = EdgeProtectionResponsePreAuth.from_dict(d.pop("pre_auth"))

        validation_failures = []
        _validation_failures = d.pop("validation_failures")
        for validation_failures_item_data in _validation_failures:
            validation_failures_item = EdgeProtectionResponseValidationFailuresItem.from_dict(
                validation_failures_item_data
            )

            validation_failures.append(validation_failures_item)

        rejections = []
        _rejections = d.pop("rejections")
        for rejections_item_data in _rejections:
            rejections_item = EdgeProtectionResponseRejectionsItem.from_dict(rejections_item_data)

            rejections.append(rejections_item)

        waf = EdgeProtectionResponseWaf.from_dict(d.pop("waf"))

        edge_protection_response = cls(
            app_id=app_id,
            range_=range_,
            source=source,
            as_of=as_of,
            pre_auth=pre_auth,
            validation_failures=validation_failures,
            rejections=rejections,
            waf=waf,
        )

        edge_protection_response.additional_properties = d
        return edge_protection_response

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
