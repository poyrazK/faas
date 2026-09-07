from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DeploymentFailedWebhookPayload")


@_attrs_define
class DeploymentFailedWebhookPayload:
    """Deployment failure details delivered with deployment.failed."""

    app_id: str
    deployment_id: str
    error_code: str | Unset = UNSET
    error_hint: str | Unset = UNSET
    error_why: str | Unset = UNSET
    error_fix: str | Unset = UNSET
    relevant_logs: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        deployment_id = self.deployment_id

        error_code = self.error_code

        error_hint = self.error_hint

        error_why = self.error_why

        error_fix = self.error_fix

        relevant_logs: list[str] | Unset = UNSET
        if not isinstance(self.relevant_logs, Unset):
            relevant_logs = self.relevant_logs

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "deployment_id": deployment_id,
            }
        )
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if error_hint is not UNSET:
            field_dict["error_hint"] = error_hint
        if error_why is not UNSET:
            field_dict["error_why"] = error_why
        if error_fix is not UNSET:
            field_dict["error_fix"] = error_fix
        if relevant_logs is not UNSET:
            field_dict["relevant_logs"] = relevant_logs

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        deployment_id = d.pop("deployment_id")

        error_code = d.pop("error_code", UNSET)

        error_hint = d.pop("error_hint", UNSET)

        error_why = d.pop("error_why", UNSET)

        error_fix = d.pop("error_fix", UNSET)

        relevant_logs = cast(list[str], d.pop("relevant_logs", UNSET))

        deployment_failed_webhook_payload = cls(
            app_id=app_id,
            deployment_id=deployment_id,
            error_code=error_code,
            error_hint=error_hint,
            error_why=error_why,
            error_fix=error_fix,
            relevant_logs=relevant_logs,
        )

        deployment_failed_webhook_payload.additional_properties = d
        return deployment_failed_webhook_payload

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
