from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_serving_health_state import ObsServingHealthState, check_obs_serving_health_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_serving_probe import ObsServingProbe
    from ..models.obs_serving_probe_counts import ObsServingProbeCounts
    from ..models.obs_serving_probe_inventory import ObsServingProbeInventory
    from ..models.obs_serving_probe_notification_health import ObsServingProbeNotificationHealth


T = TypeVar("T", bound="ObsServingHealth")


@_attrs_define
class ObsServingHealth:
    """Coverage of expected serving probes from independent deployment intent and current coverage-rule evaluations. No
    direct probes are executed.

    """

    generated_at: datetime.datetime
    scope: Literal["expected_serving_probe_coverage"]
    state: ObsServingHealthState
    max_age_seconds: Literal[120]
    inventory: ObsServingProbeInventory
    """Configuration and freshness of independent serving-probe intent."""
    counts: None | ObsServingProbeCounts
    """Null when inventory is unknown or unconfigured; an explicitly empty configured fleet has zero counts."""
    probes: list[ObsServingProbe]
    limitations: list[str]
    reason: str | Unset = UNSET
    probe_notifications: ObsServingProbeNotificationHealth | Unset = UNSET
    """Optional, independent evidence for direct compute-node webhook and
    heartbeat submissions. Counts cover every expected node/channel; the
    bounded preview orders failures first, then unknowns, successes and
    disabled channels. Available describes evidence completeness and may
    include failed submissions. Historical success does not establish
    current reachability, provider processing or human receipt. This
    section does not change serving or deployment qualification results.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.obs_serving_probe_counts import ObsServingProbeCounts

        generated_at = self.generated_at.isoformat()

        scope = self.scope

        state: str = self.state

        max_age_seconds = self.max_age_seconds

        inventory = self.inventory.to_dict()

        counts: dict[str, Any] | None
        if isinstance(self.counts, ObsServingProbeCounts):
            counts = self.counts.to_dict()
        else:
            counts = self.counts

        probes = []
        for probes_item_data in self.probes:
            probes_item = probes_item_data.to_dict()
            probes.append(probes_item)

        limitations = self.limitations

        reason = self.reason

        probe_notifications: dict[str, Any] | Unset = UNSET
        if not isinstance(self.probe_notifications, Unset):
            probe_notifications = self.probe_notifications.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "generated_at": generated_at,
                "scope": scope,
                "state": state,
                "max_age_seconds": max_age_seconds,
                "inventory": inventory,
                "counts": counts,
                "probes": probes,
                "limitations": limitations,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if probe_notifications is not UNSET:
            field_dict["probe_notifications"] = probe_notifications

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_serving_probe import ObsServingProbe
        from ..models.obs_serving_probe_counts import ObsServingProbeCounts
        from ..models.obs_serving_probe_inventory import ObsServingProbeInventory
        from ..models.obs_serving_probe_notification_health import ObsServingProbeNotificationHealth

        d = dict(src_dict)
        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        scope = cast(Literal["expected_serving_probe_coverage"], d.pop("scope"))
        if scope != "expected_serving_probe_coverage":
            raise ValueError(f"scope must match const 'expected_serving_probe_coverage', got '{scope}'")

        state = check_obs_serving_health_state(d.pop("state"))

        max_age_seconds = cast(Literal[120], d.pop("max_age_seconds"))
        if max_age_seconds != 120:
            raise ValueError(f"max_age_seconds must match const 120, got '{max_age_seconds}'")

        inventory = ObsServingProbeInventory.from_dict(d.pop("inventory"))

        def _parse_counts(data: object) -> None | ObsServingProbeCounts:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                counts_type_0 = ObsServingProbeCounts.from_dict(data)

                return counts_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ObsServingProbeCounts, data)

        counts = _parse_counts(d.pop("counts"))

        probes = []
        _probes = d.pop("probes")
        for probes_item_data in _probes:
            probes_item = ObsServingProbe.from_dict(probes_item_data)

            probes.append(probes_item)

        limitations = cast(list[str], d.pop("limitations"))

        reason = d.pop("reason", UNSET)

        _probe_notifications = d.pop("probe_notifications", UNSET)
        probe_notifications: ObsServingProbeNotificationHealth | Unset
        if isinstance(_probe_notifications, Unset):
            probe_notifications = UNSET
        else:
            probe_notifications = ObsServingProbeNotificationHealth.from_dict(_probe_notifications)

        obs_serving_health = cls(
            generated_at=generated_at,
            scope=scope,
            state=state,
            max_age_seconds=max_age_seconds,
            inventory=inventory,
            counts=counts,
            probes=probes,
            limitations=limitations,
            reason=reason,
            probe_notifications=probe_notifications,
        )

        obs_serving_health.additional_properties = d
        return obs_serving_health

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
