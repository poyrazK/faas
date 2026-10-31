from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_alert_collection_coverage import ObsAlertCollectionCoverage
    from ..models.obs_alert_collection_failure_details import ObsAlertCollectionFailureDetails
    from ..models.obs_alert_collection_health import ObsAlertCollectionHealth
    from ..models.obs_alert_collection_history_interval import ObsAlertCollectionHistoryInterval


T = TypeVar("T", bound="ObsAlertCollectionHistory")


@_attrs_define
class ObsAlertCollectionHistory:
    """Complete bounded collection cadence totals with a newest-first interval preview and current collection metadata."""

    coverage: ObsAlertCollectionCoverage
    """Exact prospective cadence-evidence partition of a requested retained window; no continuous-availability
    claim."""
    collection: ObsAlertCollectionHealth
    """Durable collection receipt and explicit producer configuration or health state."""
    items: list[ObsAlertCollectionHistoryInterval]
    total_intervals: int
    limit: Literal[200]
    truncated: bool
    """Whether older coalesced intervals are omitted from this preview while coverage totals remain complete."""
    failure_details: ObsAlertCollectionFailureDetails | Unset = UNSET
    """Failed attempts within the inclusive requested window, independent of cadence carry-in and coalesced
    intervals."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        coverage = self.coverage.to_dict()

        collection = self.collection.to_dict()

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        total_intervals = self.total_intervals

        limit = self.limit

        truncated = self.truncated

        failure_details: dict[str, Any] | Unset = UNSET
        if not isinstance(self.failure_details, Unset):
            failure_details = self.failure_details.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "coverage": coverage,
                "collection": collection,
                "items": items,
                "total_intervals": total_intervals,
                "limit": limit,
                "truncated": truncated,
            }
        )
        if failure_details is not UNSET:
            field_dict["failure_details"] = failure_details

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_alert_collection_coverage import ObsAlertCollectionCoverage
        from ..models.obs_alert_collection_failure_details import ObsAlertCollectionFailureDetails
        from ..models.obs_alert_collection_health import ObsAlertCollectionHealth
        from ..models.obs_alert_collection_history_interval import ObsAlertCollectionHistoryInterval

        d = dict(src_dict)
        coverage = ObsAlertCollectionCoverage.from_dict(d.pop("coverage"))

        collection = ObsAlertCollectionHealth.from_dict(d.pop("collection"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsAlertCollectionHistoryInterval.from_dict(items_item_data)

            items.append(items_item)

        total_intervals = d.pop("total_intervals")

        limit = cast(Literal[200], d.pop("limit"))
        if limit != 200:
            raise ValueError(f"limit must match const 200, got '{limit}'")

        truncated = d.pop("truncated")

        _failure_details = d.pop("failure_details", UNSET)
        failure_details: ObsAlertCollectionFailureDetails | Unset
        if isinstance(_failure_details, Unset):
            failure_details = UNSET
        else:
            failure_details = ObsAlertCollectionFailureDetails.from_dict(_failure_details)

        obs_alert_collection_history = cls(
            coverage=coverage,
            collection=collection,
            items=items,
            total_intervals=total_intervals,
            limit=limit,
            truncated=truncated,
            failure_details=failure_details,
        )

        obs_alert_collection_history.additional_properties = d
        return obs_alert_collection_history

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
