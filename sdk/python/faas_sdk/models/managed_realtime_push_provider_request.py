from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_realtime_push_provider_request_config import ManagedRealtimePushProviderRequestConfig


T = TypeVar("T", bound="ManagedRealtimePushProviderRequest")


@_attrs_define
class ManagedRealtimePushProviderRequest:
    config: ManagedRealtimePushProviderRequestConfig
    enabled: bool | Unset = True

    def to_dict(self) -> dict[str, Any]:
        config = self.config.to_dict()

        enabled = self.enabled

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "config": config,
            }
        )
        if enabled is not UNSET:
            field_dict["enabled"] = enabled

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_push_provider_request_config import ManagedRealtimePushProviderRequestConfig

        d = dict(src_dict)
        config = ManagedRealtimePushProviderRequestConfig.from_dict(d.pop("config"))

        enabled = d.pop("enabled", UNSET)

        managed_realtime_push_provider_request = cls(
            config=config,
            enabled=enabled,
        )

        return managed_realtime_push_provider_request
