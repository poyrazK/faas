from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_response_times_duration_basis import (
    ObsAlertResponseTimesDurationBasis,
    check_obs_alert_response_times_duration_basis,
)
from ..models.obs_alert_response_times_percentile_method import (
    ObsAlertResponseTimesPercentileMethod,
    check_obs_alert_response_times_percentile_method,
)
from ..models.obs_alert_response_times_scope import ObsAlertResponseTimesScope, check_obs_alert_response_times_scope
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_alert_collection_coverage import ObsAlertCollectionCoverage
    from ..models.obs_alert_collection_health import ObsAlertCollectionHealth
    from ..models.obs_alert_response_aggregate import ObsAlertResponseAggregate
    from ..models.obs_alert_response_group import ObsAlertResponseGroup
    from ..models.obs_alert_retention_health import ObsAlertRetentionHealth


T = TypeVar("T", bound="ObsAlertResponseTimes")


@_attrs_define
class ObsAlertResponseTimes:
    """Atomic retained activation-cohort timing with explicit evidence and sample limitations."""

    as_of: datetime.datetime
    since: datetime.datetime
    scope: ObsAlertResponseTimesScope
    duration_basis: ObsAlertResponseTimesDurationBasis
    percentile_method: ObsAlertResponseTimesPercentileMethod
    limitations: list[str]
    collection: ObsAlertCollectionHealth
    """Durable collection receipt and explicit producer configuration or health state."""
    retention: ObsAlertRetentionHealth
    """Independent retention receipt and exact expired-episode backlog at the snapshot cutoff."""
    totals: None | ObsAlertResponseAggregate
    """Null before any successful collection; a known quiet retained cohort has zero counts and null percentiles."""
    groups: list[ObsAlertResponseGroup]
    coverage: ObsAlertCollectionCoverage | Unset = UNSET
    """Exact prospective cadence-evidence partition of a requested retained window; no continuous-availability
    claim."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.obs_alert_response_aggregate import ObsAlertResponseAggregate

        as_of = self.as_of.isoformat()

        since = self.since.isoformat()

        scope: str = self.scope

        duration_basis: str = self.duration_basis

        percentile_method: str = self.percentile_method

        limitations = self.limitations

        collection = self.collection.to_dict()

        retention = self.retention.to_dict()

        totals: dict[str, Any] | None
        if isinstance(self.totals, ObsAlertResponseAggregate):
            totals = self.totals.to_dict()
        else:
            totals = self.totals

        groups = []
        for groups_item_data in self.groups:
            groups_item = groups_item_data.to_dict()
            groups.append(groups_item)

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
                "duration_basis": duration_basis,
                "percentile_method": percentile_method,
                "limitations": limitations,
                "collection": collection,
                "retention": retention,
                "totals": totals,
                "groups": groups,
            }
        )
        if coverage is not UNSET:
            field_dict["coverage"] = coverage

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_alert_collection_coverage import ObsAlertCollectionCoverage
        from ..models.obs_alert_collection_health import ObsAlertCollectionHealth
        from ..models.obs_alert_response_aggregate import ObsAlertResponseAggregate
        from ..models.obs_alert_response_group import ObsAlertResponseGroup
        from ..models.obs_alert_retention_health import ObsAlertRetentionHealth

        d = dict(src_dict)
        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        since = datetime.datetime.fromisoformat(d.pop("since"))

        scope = check_obs_alert_response_times_scope(d.pop("scope"))

        duration_basis = check_obs_alert_response_times_duration_basis(d.pop("duration_basis"))

        percentile_method = check_obs_alert_response_times_percentile_method(d.pop("percentile_method"))

        limitations = cast(list[str], d.pop("limitations"))

        collection = ObsAlertCollectionHealth.from_dict(d.pop("collection"))

        retention = ObsAlertRetentionHealth.from_dict(d.pop("retention"))

        def _parse_totals(data: object) -> None | ObsAlertResponseAggregate:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                totals_type_0 = ObsAlertResponseAggregate.from_dict(data)

                return totals_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ObsAlertResponseAggregate, data)

        totals = _parse_totals(d.pop("totals"))

        groups = []
        _groups = d.pop("groups")
        for groups_item_data in _groups:
            groups_item = ObsAlertResponseGroup.from_dict(groups_item_data)

            groups.append(groups_item)

        _coverage = d.pop("coverage", UNSET)
        coverage: ObsAlertCollectionCoverage | Unset
        if isinstance(_coverage, Unset):
            coverage = UNSET
        else:
            coverage = ObsAlertCollectionCoverage.from_dict(_coverage)

        obs_alert_response_times = cls(
            as_of=as_of,
            since=since,
            scope=scope,
            duration_basis=duration_basis,
            percentile_method=percentile_method,
            limitations=limitations,
            collection=collection,
            retention=retention,
            totals=totals,
            groups=groups,
            coverage=coverage,
        )

        obs_alert_response_times.additional_properties = d
        return obs_alert_response_times

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
