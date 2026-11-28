from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.app_deploy_settings_condition_expected_settings import AppDeploySettingsConditionExpectedSettings


T = TypeVar("T", bound="AppDeploySettingsCondition")


@_attrs_define
class AppDeploySettingsCondition:
    """Atomic preconditions for single-app lifecycle/scaling recovery. The app must retain this identity and its newest
    deployment must be the expected live deployment. Every targeted field must still match its observed value, including
    targeted fields omitted from the partial update because they already match the desired value. Checked in the same
    transaction as the update. A conflict returns 409 deploy_settings_conflict with field names, without setting values.
    Cannot be combined with environment edits or other app fields. Ordinary updates can omit this object.

    """

    expected_app_id: str
    """Exact app ID returned by the app read."""
    expected_deployment_id: str
    """Exact accepted deployment being recovered."""
    expected_settings: AppDeploySettingsConditionExpectedSettings
    """Observed values from the app read; null represents an unset optional policy. Must cover every mutated
    setting."""

    def to_dict(self) -> dict[str, Any]:
        expected_app_id = self.expected_app_id

        expected_deployment_id = self.expected_deployment_id

        expected_settings = self.expected_settings.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_app_id": expected_app_id,
                "expected_deployment_id": expected_deployment_id,
                "expected_settings": expected_settings,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_deploy_settings_condition_expected_settings import AppDeploySettingsConditionExpectedSettings

        d = dict(src_dict)
        expected_app_id = d.pop("expected_app_id")

        expected_deployment_id = d.pop("expected_deployment_id")

        expected_settings = AppDeploySettingsConditionExpectedSettings.from_dict(d.pop("expected_settings"))

        app_deploy_settings_condition = cls(
            expected_app_id=expected_app_id,
            expected_deployment_id=expected_deployment_id,
            expected_settings=expected_settings,
        )

        return app_deploy_settings_condition
