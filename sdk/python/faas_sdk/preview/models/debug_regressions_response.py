from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.debug_regression_item import DebugRegressionItem


T = TypeVar("T", bound="DebugRegressionsResponse")


@_attrs_define
class DebugRegressionsResponse:
    """Response from GET /v1/apps/{slug}/debug/regressions (ADR-127 / PR-B).
    `since` echoes the effective window applied.

    """

    since: str
    regressions: list[DebugRegressionItem]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        since = self.since

        regressions = []
        for regressions_item_data in self.regressions:
            regressions_item = regressions_item_data.to_dict()
            regressions.append(regressions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "since": since,
                "regressions": regressions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_regression_item import DebugRegressionItem

        d = dict(src_dict)
        since = d.pop("since")

        regressions = []
        _regressions = d.pop("regressions")
        for regressions_item_data in _regressions:
            regressions_item = DebugRegressionItem.from_dict(regressions_item_data)

            regressions.append(regressions_item)

        debug_regressions_response = cls(
            since=since,
            regressions=regressions,
        )

        debug_regressions_response.additional_properties = d
        return debug_regressions_response

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
