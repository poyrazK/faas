from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.deployment_response import DeploymentResponse
    from ..models.rollout_recovery_receipt import RolloutRecoveryReceipt
    from ..models.service_rollout_recovery_receipt import ServiceRolloutRecoveryReceipt


T = TypeVar("T", bound="RolloutTransitionResponse")


@_attrs_define
class RolloutTransitionResponse:
    """POST /v1/apps/{slug}/rollouts/recover response. The post-recovery Deployment + the audit row id (so the operator's
    terminal can echo audit_id=…).

    """

    deployment: DeploymentResponse
    """One deployment: id, app, source ref, build status, commit SHA, and lifecycle timestamps. The optional
    `has_overrides` and `override_*` fields are the persisted echo of the create-time overrides object (issue #460 /
    ADR-053); they round-trip via `GET /v1/apps/{slug}/deployments/{id}` so a customer can audit what their last
    deploy pinned. Env values are NEVER echoed — only the keys (`override_env_keys`); env_secrets refs ARE echoed
    because the ref shape is non-secret by design."""
    audit_id: str
    """The deployment_audit row id (stringified so JSON-number → int64 → BigInt drift doesn't poison SDK callers)."""
    recovery: RolloutRecoveryReceipt | Unset = UNSET
    """Committed exact canary abort, including the restored traffic recipient and any binding reports required by
    its stored release policy."""
    service_recovery: ServiceRolloutRecoveryReceipt | Unset = UNSET
    """Durable exact service abort request. Acceptance does not confirm traffic restoration, gateway
    acknowledgement or request drain. Poll the exact deployment's service_rollout_handoff."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment = self.deployment.to_dict()

        audit_id = self.audit_id

        recovery: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recovery, Unset):
            recovery = self.recovery.to_dict()

        service_recovery: dict[str, Any] | Unset = UNSET
        if not isinstance(self.service_recovery, Unset):
            service_recovery = self.service_recovery.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment": deployment,
                "audit_id": audit_id,
            }
        )
        if recovery is not UNSET:
            field_dict["recovery"] = recovery
        if service_recovery is not UNSET:
            field_dict["service_recovery"] = service_recovery

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deployment_response import DeploymentResponse
        from ..models.rollout_recovery_receipt import RolloutRecoveryReceipt
        from ..models.service_rollout_recovery_receipt import ServiceRolloutRecoveryReceipt

        d = dict(src_dict)
        deployment = DeploymentResponse.from_dict(d.pop("deployment"))

        audit_id = d.pop("audit_id")

        _recovery = d.pop("recovery", UNSET)
        recovery: RolloutRecoveryReceipt | Unset
        if isinstance(_recovery, Unset):
            recovery = UNSET
        else:
            recovery = RolloutRecoveryReceipt.from_dict(_recovery)

        _service_recovery = d.pop("service_recovery", UNSET)
        service_recovery: ServiceRolloutRecoveryReceipt | Unset
        if isinstance(_service_recovery, Unset):
            service_recovery = UNSET
        else:
            service_recovery = ServiceRolloutRecoveryReceipt.from_dict(_service_recovery)

        rollout_transition_response = cls(
            deployment=deployment,
            audit_id=audit_id,
            recovery=recovery,
            service_recovery=service_recovery,
        )

        rollout_transition_response.additional_properties = d
        return rollout_transition_response

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
