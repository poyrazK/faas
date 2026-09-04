from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.deployment_response import DeploymentResponse


T = TypeVar("T", bound="CanaryAdvanceResponse")


@_attrs_define
class CanaryAdvanceResponse:
    """The atomic canary transition result and the deployment_audit row id."""

    deployment: DeploymentResponse
    """One deployment: id, app, source ref, build status, commit SHA, and lifecycle timestamps. The optional
    `has_overrides` and `override_*` fields are the persisted echo of the create-time overrides object (issue #460 /
    ADR-053); they round-trip via `GET /v1/apps/{slug}/deployments/{id}` so a customer can audit what their last
    deploy pinned. Env values are NEVER echoed — only the keys (`override_env_keys`); env_secrets refs ARE echoed
    because the ref shape is non-secret by design."""
    audit_id: str
    """The deployment_audit row id, stringified for SDK portability."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment = self.deployment.to_dict()

        audit_id = self.audit_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment": deployment,
                "audit_id": audit_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deployment_response import DeploymentResponse

        d = dict(src_dict)
        deployment = DeploymentResponse.from_dict(d.pop("deployment"))

        audit_id = d.pop("audit_id")

        canary_advance_response = cls(
            deployment=deployment,
            audit_id=audit_id,
        )

        canary_advance_response.additional_properties = d
        return canary_advance_response

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
