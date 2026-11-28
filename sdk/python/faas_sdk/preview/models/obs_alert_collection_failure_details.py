from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.obs_alert_collection_failure import ObsAlertCollectionFailure


T = TypeVar("T", bound="ObsAlertCollectionFailureDetails")


@_attrs_define
class ObsAlertCollectionFailureDetails:
    """Failed attempts within the inclusive requested window, independent of cadence carry-in and coalesced intervals."""

    items: list[ObsAlertCollectionFailure]
    """Newest failed attempts first; diagnostics may be unknown on any item."""
    total_failures: int
    """Complete failed-attempt count matching coverage.failures before preview truncation."""
    limit: Literal[20]
    truncated: bool
    """Whether older failed attempts are omitted from the details preview."""
    limitations: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        total_failures = self.total_failures

        limit = self.limit

        truncated = self.truncated

        limitations = self.limitations

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
                "total_failures": total_failures,
                "limit": limit,
                "truncated": truncated,
                "limitations": limitations,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_alert_collection_failure import ObsAlertCollectionFailure

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsAlertCollectionFailure.from_dict(items_item_data)

            items.append(items_item)

        total_failures = d.pop("total_failures")

        limit = cast(Literal[20], d.pop("limit"))
        if limit != 20:
            raise ValueError(f"limit must match const 20, got '{limit}'")

        truncated = d.pop("truncated")

        limitations = cast(list[str], d.pop("limitations"))

        obs_alert_collection_failure_details = cls(
            items=items,
            total_failures=total_failures,
            limit=limit,
            truncated=truncated,
            limitations=limitations,
        )

        obs_alert_collection_failure_details.additional_properties = d
        return obs_alert_collection_failure_details

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
