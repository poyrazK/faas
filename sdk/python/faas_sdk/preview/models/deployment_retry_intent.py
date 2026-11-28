from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deployment_retry_intent_version import DeploymentRetryIntentVersion, check_deployment_retry_intent_version

T = TypeVar("T", bound="DeploymentRetryIntent")


@_attrs_define
class DeploymentRetryIntent:
    """Admission-time release intent retained across routing, failure and stage changes. Absent on legacy deployments."""

    version: DeploymentRetryIntentVersion
    traffic_percent: int
    traffic_percent_explicit: bool
    service_rollout: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: str = self.version

        traffic_percent = self.traffic_percent

        traffic_percent_explicit = self.traffic_percent_explicit

        service_rollout = self.service_rollout

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "traffic_percent": traffic_percent,
                "traffic_percent_explicit": traffic_percent_explicit,
                "service_rollout": service_rollout,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = check_deployment_retry_intent_version(d.pop("version"))

        traffic_percent = d.pop("traffic_percent")

        traffic_percent_explicit = d.pop("traffic_percent_explicit")

        service_rollout = d.pop("service_rollout")

        deployment_retry_intent = cls(
            version=version,
            traffic_percent=traffic_percent,
            traffic_percent_explicit=traffic_percent_explicit,
            service_rollout=service_rollout,
        )

        deployment_retry_intent.additional_properties = d
        return deployment_retry_intent

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
