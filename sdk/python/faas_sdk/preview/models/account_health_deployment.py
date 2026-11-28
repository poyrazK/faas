from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AccountHealthDeployment")


@_attrs_define
class AccountHealthDeployment:
    """A failed deployment with the error and fix hint the build or rollout reported."""

    deployment_id: str
    """Failed deployment identifier."""
    app_id: str
    """Identifier of the deployment's app."""
    created_at: datetime.datetime
    """When the deployment was created."""
    app_slug: str | Unset = UNSET
    """Slug of the deployment's app."""
    error_code: str | Unset = UNSET
    """Stable failure code."""
    error: str | Unset = UNSET
    """Failure message, at most 500 bytes."""
    hint: str | Unset = UNSET
    """Suggested fix, at most 500 bytes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = self.deployment_id

        app_id = self.app_id

        created_at = self.created_at.isoformat()

        app_slug = self.app_slug

        error_code = self.error_code

        error = self.error

        hint = self.hint

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "app_id": app_id,
                "created_at": created_at,
            }
        )
        if app_slug is not UNSET:
            field_dict["app_slug"] = app_slug
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if error is not UNSET:
            field_dict["error"] = error
        if hint is not UNSET:
            field_dict["hint"] = hint

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = d.pop("deployment_id")

        app_id = d.pop("app_id")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        app_slug = d.pop("app_slug", UNSET)

        error_code = d.pop("error_code", UNSET)

        error = d.pop("error", UNSET)

        hint = d.pop("hint", UNSET)

        account_health_deployment = cls(
            deployment_id=deployment_id,
            app_id=app_id,
            created_at=created_at,
            app_slug=app_slug,
            error_code=error_code,
            error=error,
            hint=hint,
        )

        account_health_deployment.additional_properties = d
        return account_health_deployment

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
