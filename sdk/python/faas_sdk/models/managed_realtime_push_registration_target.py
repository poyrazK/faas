from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimePushRegistrationTarget")


@_attrs_define
class ManagedRealtimePushRegistrationTarget:
    """FCM/APNs require token; Web Push requires endpoint, p256dh and auth."""

    token: str | Unset = UNSET
    endpoint: str | Unset = UNSET
    p256dh: str | Unset = UNSET
    auth: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        token = self.token

        endpoint = self.endpoint

        p256dh = self.p256dh

        auth = self.auth

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if token is not UNSET:
            field_dict["token"] = token
        if endpoint is not UNSET:
            field_dict["endpoint"] = endpoint
        if p256dh is not UNSET:
            field_dict["p256dh"] = p256dh
        if auth is not UNSET:
            field_dict["auth"] = auth

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        token = d.pop("token", UNSET)

        endpoint = d.pop("endpoint", UNSET)

        p256dh = d.pop("p256dh", UNSET)

        auth = d.pop("auth", UNSET)

        managed_realtime_push_registration_target = cls(
            token=token,
            endpoint=endpoint,
            p256dh=p256dh,
            auth=auth,
        )

        return managed_realtime_push_registration_target
