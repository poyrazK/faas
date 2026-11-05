from __future__ import annotations

from collections.abc import Mapping
from typing import Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="GitHubActivityRetryResponse")


@_attrs_define
class GitHubActivityRetryResponse:
    """Customer-safe confirmation that recent failed GitHub activity was queued."""

    ok: bool
    retried_webhooks: int
    retried_checks: int
    status: Literal["pending"]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ok = self.ok

        retried_webhooks = self.retried_webhooks

        retried_checks = self.retried_checks

        status = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ok": ok,
                "retried_webhooks": retried_webhooks,
                "retried_checks": retried_checks,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ok = d.pop("ok")

        retried_webhooks = d.pop("retried_webhooks")

        retried_checks = d.pop("retried_checks")

        status = cast(Literal["pending"], d.pop("status"))
        if status != "pending":
            raise ValueError(f"status must match const 'pending', got '{status}'")

        git_hub_activity_retry_response = cls(
            ok=ok,
            retried_webhooks=retried_webhooks,
            retried_checks=retried_checks,
            status=status,
        )

        git_hub_activity_retry_response.additional_properties = d
        return git_hub_activity_retry_response

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
