from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_recurrence_percentile_method import (
    ObsAlertRecurrencePercentileMethod,
    check_obs_alert_recurrence_percentile_method,
)
from ..models.obs_alert_recurrence_scope import ObsAlertRecurrenceScope, check_obs_alert_recurrence_scope
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_alert_collection_coverage import ObsAlertCollectionCoverage
    from ..models.obs_alert_collection_health import ObsAlertCollectionHealth
    from ..models.obs_alert_recurrence_series import ObsAlertRecurrenceSeries
    from ..models.obs_alert_recurrence_totals import ObsAlertRecurrenceTotals
    from ..models.obs_alert_retention_health import ObsAlertRetentionHealth


T = TypeVar("T", bound="ObsAlertRecurrence")


@_attrs_define
class ObsAlertRecurrence:
    """Retained activation-cohort recurrence evidence with complete totals and a ranked preview."""

    as_of: datetime.datetime
    since: datetime.datetime
    scope: ObsAlertRecurrenceScope
    percentile_method: ObsAlertRecurrencePercentileMethod
    limitations: list[str]
    collection: ObsAlertCollectionHealth
    """Durable collection receipt and explicit producer configuration or health state."""
    retention: ObsAlertRetentionHealth
    """Independent retention receipt and exact expired-episode backlog at the snapshot cutoff."""
    totals: None | ObsAlertRecurrenceTotals
    """Null before the first successful collection; a known quiet retained cohort has zero counts and null
    statistics."""
    items: list[ObsAlertRecurrenceSeries]
    truncated: bool
    """Whether the ranked preview omits series included in complete totals."""
    coverage: ObsAlertCollectionCoverage | Unset = UNSET
    """Exact prospective cadence-evidence partition of a requested retained window; no continuous-availability
    claim."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.obs_alert_recurrence_totals import ObsAlertRecurrenceTotals

        as_of = self.as_of.isoformat()

        since = self.since.isoformat()

        scope: str = self.scope

        percentile_method: str = self.percentile_method

        limitations = self.limitations

        collection = self.collection.to_dict()

        retention = self.retention.to_dict()

        totals: dict[str, Any] | None
        if isinstance(self.totals, ObsAlertRecurrenceTotals):
            totals = self.totals.to_dict()
        else:
            totals = self.totals

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        truncated = self.truncated

        coverage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.coverage, Unset):
            coverage = self.coverage.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "as_of": as_of,
                "since": since,
                "scope": scope,
                "percentile_method": percentile_method,
                "limitations": limitations,
                "collection": collection,
                "retention": retention,
                "totals": totals,
                "items": items,
                "truncated": truncated,
            }
        )
        if coverage is not UNSET:
            field_dict["coverage"] = coverage

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_alert_collection_coverage import ObsAlertCollectionCoverage
        from ..models.obs_alert_collection_health import ObsAlertCollectionHealth
        from ..models.obs_alert_recurrence_series import ObsAlertRecurrenceSeries
        from ..models.obs_alert_recurrence_totals import ObsAlertRecurrenceTotals
        from ..models.obs_alert_retention_health import ObsAlertRetentionHealth

        d = dict(src_dict)
        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        since = datetime.datetime.fromisoformat(d.pop("since"))

        scope = check_obs_alert_recurrence_scope(d.pop("scope"))

        percentile_method = check_obs_alert_recurrence_percentile_method(d.pop("percentile_method"))

        limitations = cast(list[str], d.pop("limitations"))

        collection = ObsAlertCollectionHealth.from_dict(d.pop("collection"))

        retention = ObsAlertRetentionHealth.from_dict(d.pop("retention"))

        def _parse_totals(data: object) -> None | ObsAlertRecurrenceTotals:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                totals_type_0 = ObsAlertRecurrenceTotals.from_dict(data)

                return totals_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ObsAlertRecurrenceTotals, data)

        totals = _parse_totals(d.pop("totals"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsAlertRecurrenceSeries.from_dict(items_item_data)

            items.append(items_item)

        truncated = d.pop("truncated")

        _coverage = d.pop("coverage", UNSET)
        coverage: ObsAlertCollectionCoverage | Unset
        if isinstance(_coverage, Unset):
            coverage = UNSET
        else:
            coverage = ObsAlertCollectionCoverage.from_dict(_coverage)

        obs_alert_recurrence = cls(
            as_of=as_of,
            since=since,
            scope=scope,
            percentile_method=percentile_method,
            limitations=limitations,
            collection=collection,
            retention=retention,
            totals=totals,
            items=items,
            truncated=truncated,
            coverage=coverage,
        )

        obs_alert_recurrence.additional_properties = d
        return obs_alert_recurrence

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
