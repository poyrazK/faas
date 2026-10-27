from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.profile_deployment_policy_config import ProfileDeploymentPolicyConfig


T = TypeVar("T", bound="SaveProfileDeploymentPolicyRequest")


@_attrs_define
class SaveProfileDeploymentPolicyRequest:
    """Full replacement of the CPU rollout-check settings. Changing or disabling settings cancels queued and leased work
    for earlier policy revisions.

    """

    expected_revision: int
    config: ProfileDeploymentPolicyConfig
    """Opt-in background comparison policy. Collection must already be enabled on applications. Runtime filters
    apply to both deployments; capture coverage is not instrumentation completeness."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        config = self.config.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected_revision": expected_revision,
                "config": config,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_deployment_policy_config import ProfileDeploymentPolicyConfig

        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        config = ProfileDeploymentPolicyConfig.from_dict(d.pop("config"))

        save_profile_deployment_policy_request = cls(
            expected_revision=expected_revision,
            config=config,
        )

        save_profile_deployment_policy_request.additional_properties = d
        return save_profile_deployment_policy_request

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
