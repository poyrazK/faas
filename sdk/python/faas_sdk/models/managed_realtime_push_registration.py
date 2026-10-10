from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.managed_realtime_push_registration_provider import (
    ManagedRealtimePushRegistrationProvider,
    check_managed_realtime_push_registration_provider,
)

if TYPE_CHECKING:
    from ..models.managed_realtime_push_registration_target import ManagedRealtimePushRegistrationTarget


T = TypeVar("T", bound="ManagedRealtimePushRegistration")


@_attrs_define
class ManagedRealtimePushRegistration:
    """Device registration used to route push notifications to a principal."""

    provider: ManagedRealtimePushRegistrationProvider
    target: ManagedRealtimePushRegistrationTarget
    """FCM/APNs require token; Web Push requires endpoint, p256dh and auth."""

    def to_dict(self) -> dict[str, Any]:
        provider: str = self.provider

        target = self.target.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "provider": provider,
                "target": target,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_push_registration_target import ManagedRealtimePushRegistrationTarget

        d = dict(src_dict)
        provider = check_managed_realtime_push_registration_provider(d.pop("provider"))

        target = ManagedRealtimePushRegistrationTarget.from_dict(d.pop("target"))

        managed_realtime_push_registration = cls(
            provider=provider,
            target=target,
        )

        return managed_realtime_push_registration
