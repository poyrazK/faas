from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_workload_summary_age_basis import (
    ObsAlertWorkloadSummaryAgeBasis,
    check_obs_alert_workload_summary_age_basis,
)
from ..models.obs_alert_workload_summary_scope import (
    ObsAlertWorkloadSummaryScope,
    check_obs_alert_workload_summary_scope,
)

if TYPE_CHECKING:
    from ..models.obs_alert_collection_health import ObsAlertCollectionHealth
    from ..models.obs_alert_retention_health import ObsAlertRetentionHealth
    from ..models.obs_alert_workload_counts import ObsAlertWorkloadCounts
    from ..models.obs_alert_workload_group import ObsAlertWorkloadGroup
    from ..models.obs_alert_workload_oldest import ObsAlertWorkloadOldest


T = TypeVar("T", bound="ObsAlertWorkloadSummary")


@_attrs_define
class ObsAlertWorkloadSummary:
    """Atomic retained-alert workload snapshot with explicit collection and cleanup coverage."""

    as_of: datetime.datetime
    scope: ObsAlertWorkloadSummaryScope
    age_basis: ObsAlertWorkloadSummaryAgeBasis
    collection: ObsAlertCollectionHealth
    """Durable collection receipt and explicit producer configuration or health state."""
    retention: ObsAlertRetentionHealth
    """Independent retention receipt and exact expired-episode backlog at the snapshot cutoff."""
    counts: None | ObsAlertWorkloadCounts
    """Null until a successful collection has established a retained observation scope."""
    groups: list[ObsAlertWorkloadGroup]
    oldest_unacknowledged: None | ObsAlertWorkloadOldest
    """Oldest unsnoozed unresolved unacknowledged episode; null when absent or scope is unknown."""
    oldest_unowned: None | ObsAlertWorkloadOldest
    """Oldest unsnoozed unresolved unowned episode; null when absent or scope is unknown."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.obs_alert_workload_counts import ObsAlertWorkloadCounts
        from ..models.obs_alert_workload_oldest import ObsAlertWorkloadOldest

        as_of = self.as_of.isoformat()

        scope: str = self.scope

        age_basis: str = self.age_basis

        collection = self.collection.to_dict()

        retention = self.retention.to_dict()

        counts: dict[str, Any] | None
        if isinstance(self.counts, ObsAlertWorkloadCounts):
            counts = self.counts.to_dict()
        else:
            counts = self.counts

        groups = []
        for groups_item_data in self.groups:
            groups_item = groups_item_data.to_dict()
            groups.append(groups_item)

        oldest_unacknowledged: dict[str, Any] | None
        if isinstance(self.oldest_unacknowledged, ObsAlertWorkloadOldest):
            oldest_unacknowledged = self.oldest_unacknowledged.to_dict()
        else:
            oldest_unacknowledged = self.oldest_unacknowledged

        oldest_unowned: dict[str, Any] | None
        if isinstance(self.oldest_unowned, ObsAlertWorkloadOldest):
            oldest_unowned = self.oldest_unowned.to_dict()
        else:
            oldest_unowned = self.oldest_unowned

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "as_of": as_of,
                "scope": scope,
                "age_basis": age_basis,
                "collection": collection,
                "retention": retention,
                "counts": counts,
                "groups": groups,
                "oldest_unacknowledged": oldest_unacknowledged,
                "oldest_unowned": oldest_unowned,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_alert_collection_health import ObsAlertCollectionHealth
        from ..models.obs_alert_retention_health import ObsAlertRetentionHealth
        from ..models.obs_alert_workload_counts import ObsAlertWorkloadCounts
        from ..models.obs_alert_workload_group import ObsAlertWorkloadGroup
        from ..models.obs_alert_workload_oldest import ObsAlertWorkloadOldest

        d = dict(src_dict)
        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        scope = check_obs_alert_workload_summary_scope(d.pop("scope"))

        age_basis = check_obs_alert_workload_summary_age_basis(d.pop("age_basis"))

        collection = ObsAlertCollectionHealth.from_dict(d.pop("collection"))

        retention = ObsAlertRetentionHealth.from_dict(d.pop("retention"))

        def _parse_counts(data: object) -> None | ObsAlertWorkloadCounts:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                counts_type_0 = ObsAlertWorkloadCounts.from_dict(data)

                return counts_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ObsAlertWorkloadCounts, data)

        counts = _parse_counts(d.pop("counts"))

        groups = []
        _groups = d.pop("groups")
        for groups_item_data in _groups:
            groups_item = ObsAlertWorkloadGroup.from_dict(groups_item_data)

            groups.append(groups_item)

        def _parse_oldest_unacknowledged(data: object) -> None | ObsAlertWorkloadOldest:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                oldest_unacknowledged_type_0 = ObsAlertWorkloadOldest.from_dict(data)

                return oldest_unacknowledged_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ObsAlertWorkloadOldest, data)

        oldest_unacknowledged = _parse_oldest_unacknowledged(d.pop("oldest_unacknowledged"))

        def _parse_oldest_unowned(data: object) -> None | ObsAlertWorkloadOldest:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                oldest_unowned_type_0 = ObsAlertWorkloadOldest.from_dict(data)

                return oldest_unowned_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ObsAlertWorkloadOldest, data)

        oldest_unowned = _parse_oldest_unowned(d.pop("oldest_unowned"))

        obs_alert_workload_summary = cls(
            as_of=as_of,
            scope=scope,
            age_basis=age_basis,
            collection=collection,
            retention=retention,
            counts=counts,
            groups=groups,
            oldest_unacknowledged=oldest_unacknowledged,
            oldest_unowned=oldest_unowned,
        )

        obs_alert_workload_summary.additional_properties = d
        return obs_alert_workload_summary

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
