from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_runtime_deployment import BindingRuntimeDeployment


T = TypeVar("T", bound="BindingRuntimeFreshness")


@_attrs_define
class BindingRuntimeFreshness:
    """App-wide timestamp-based configuration freshness, independent of canary verification. This is not guest
    acknowledgement, readiness or proof of credential use. Scope filtering selects deployments; task guests, jobs and
    mirrors are excluded.

    """

    source: str
    """Currently instance_started_at."""
    observed_at: datetime.datetime
    deployments: list[BindingRuntimeDeployment]
    config_changed_at: datetime.datetime | Unset = UNSET
    """Latest app-wide environment or secret change. Without a stamp, resident freshness is unknown."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source = self.source

        observed_at = self.observed_at.isoformat()

        deployments = []
        for deployments_item_data in self.deployments:
            deployments_item = deployments_item_data.to_dict()
            deployments.append(deployments_item)

        config_changed_at: str | Unset = UNSET
        if not isinstance(self.config_changed_at, Unset):
            config_changed_at = self.config_changed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "observed_at": observed_at,
                "deployments": deployments,
            }
        )
        if config_changed_at is not UNSET:
            field_dict["config_changed_at"] = config_changed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_runtime_deployment import BindingRuntimeDeployment

        d = dict(src_dict)
        source = d.pop("source")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        deployments = []
        _deployments = d.pop("deployments")
        for deployments_item_data in _deployments:
            deployments_item = BindingRuntimeDeployment.from_dict(deployments_item_data)

            deployments.append(deployments_item)

        _config_changed_at = d.pop("config_changed_at", UNSET)
        config_changed_at: datetime.datetime | Unset
        if isinstance(_config_changed_at, Unset):
            config_changed_at = UNSET
        else:
            config_changed_at = datetime.datetime.fromisoformat(_config_changed_at)

        binding_runtime_freshness = cls(
            source=source,
            observed_at=observed_at,
            deployments=deployments,
            config_changed_at=config_changed_at,
        )

        binding_runtime_freshness.additional_properties = d
        return binding_runtime_freshness

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
