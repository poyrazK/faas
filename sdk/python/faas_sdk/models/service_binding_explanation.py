from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.service_binding_explanation_policy_source import (
    ServiceBindingExplanationPolicySource,
    check_service_binding_explanation_policy_source,
)
from ..models.service_binding_explanation_result import (
    ServiceBindingExplanationResult,
    check_service_binding_explanation_result,
)
from ..models.service_binding_transport import ServiceBindingTransport, check_service_binding_transport
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.service_authorization_check import ServiceAuthorizationCheck


T = TypeVar("T", bound="ServiceBindingExplanation")


@_attrs_define
class ServiceBindingExplanation:
    """Separate-read policy and routing observation, not live identity, guest adoption or connectivity evidence."""

    caller: str
    target: str
    """Requested service name, which may differ from the resolved physical app slug."""
    method: str
    path: str
    """Decoded request path without query values."""
    result: ServiceBindingExplanationResult
    scope: str
    """Environment of the exact caller deployment."""
    checked_at: datetime.datetime
    checks: list[ServiceAuthorizationCheck]
    limitations: str
    caller_fingerprint: str | Unset = UNSET
    """Observed app settings fingerprint; frozen environment policy may override these settings."""
    target_fingerprint: str | Unset = UNSET
    """Observed target app settings fingerprint; not a frozen release policy digest."""
    caller_deployment_id: UUID | Unset = UNSET
    target_app_id: UUID | Unset = UNSET
    target_deployment_id: UUID | Unset = UNSET
    """Absent when the service route is unpinned or unavailable."""
    release_id: UUID | Unset = UNSET
    """Resolved release, including retained project releases when permitted by the gateway."""
    policy_source: ServiceBindingExplanationPolicySource | Unset = UNSET
    canonical_transport: ServiceBindingTransport | Unset = UNSET
    """Scheme used by the canonical GREGALE_SERVICE_<NAME>_URL environment variable. `https` selects the private
    `.internal` alias; `http` preserves the legacy `.svc.gregale` endpoint."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        caller = self.caller

        target = self.target

        method = self.method

        path = self.path

        result: str = self.result

        scope = self.scope

        checked_at = self.checked_at.isoformat()

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        limitations = self.limitations

        caller_fingerprint = self.caller_fingerprint

        target_fingerprint = self.target_fingerprint

        caller_deployment_id: str | Unset = UNSET
        if not isinstance(self.caller_deployment_id, Unset):
            caller_deployment_id = str(self.caller_deployment_id)

        target_app_id: str | Unset = UNSET
        if not isinstance(self.target_app_id, Unset):
            target_app_id = str(self.target_app_id)

        target_deployment_id: str | Unset = UNSET
        if not isinstance(self.target_deployment_id, Unset):
            target_deployment_id = str(self.target_deployment_id)

        release_id: str | Unset = UNSET
        if not isinstance(self.release_id, Unset):
            release_id = str(self.release_id)

        policy_source: str | Unset = UNSET
        if not isinstance(self.policy_source, Unset):
            policy_source = self.policy_source

        canonical_transport: str | Unset = UNSET
        if not isinstance(self.canonical_transport, Unset):
            canonical_transport = self.canonical_transport

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "caller": caller,
                "target": target,
                "method": method,
                "path": path,
                "result": result,
                "scope": scope,
                "checked_at": checked_at,
                "checks": checks,
                "limitations": limitations,
            }
        )
        if caller_fingerprint is not UNSET:
            field_dict["caller_fingerprint"] = caller_fingerprint
        if target_fingerprint is not UNSET:
            field_dict["target_fingerprint"] = target_fingerprint
        if caller_deployment_id is not UNSET:
            field_dict["caller_deployment_id"] = caller_deployment_id
        if target_app_id is not UNSET:
            field_dict["target_app_id"] = target_app_id
        if target_deployment_id is not UNSET:
            field_dict["target_deployment_id"] = target_deployment_id
        if release_id is not UNSET:
            field_dict["release_id"] = release_id
        if policy_source is not UNSET:
            field_dict["policy_source"] = policy_source
        if canonical_transport is not UNSET:
            field_dict["canonical_transport"] = canonical_transport

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.service_authorization_check import ServiceAuthorizationCheck

        d = dict(src_dict)
        caller = d.pop("caller")

        target = d.pop("target")

        method = d.pop("method")

        path = d.pop("path")

        result = check_service_binding_explanation_result(d.pop("result"))

        scope = d.pop("scope")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = ServiceAuthorizationCheck.from_dict(checks_item_data)

            checks.append(checks_item)

        limitations = d.pop("limitations")

        caller_fingerprint = d.pop("caller_fingerprint", UNSET)

        target_fingerprint = d.pop("target_fingerprint", UNSET)

        _caller_deployment_id = d.pop("caller_deployment_id", UNSET)
        caller_deployment_id: UUID | Unset
        if isinstance(_caller_deployment_id, Unset):
            caller_deployment_id = UNSET
        else:
            caller_deployment_id = UUID(_caller_deployment_id)

        _target_app_id = d.pop("target_app_id", UNSET)
        target_app_id: UUID | Unset
        if isinstance(_target_app_id, Unset):
            target_app_id = UNSET
        else:
            target_app_id = UUID(_target_app_id)

        _target_deployment_id = d.pop("target_deployment_id", UNSET)
        target_deployment_id: UUID | Unset
        if isinstance(_target_deployment_id, Unset):
            target_deployment_id = UNSET
        else:
            target_deployment_id = UUID(_target_deployment_id)

        _release_id = d.pop("release_id", UNSET)
        release_id: UUID | Unset
        if isinstance(_release_id, Unset):
            release_id = UNSET
        else:
            release_id = UUID(_release_id)

        _policy_source = d.pop("policy_source", UNSET)
        policy_source: ServiceBindingExplanationPolicySource | Unset
        if isinstance(_policy_source, Unset):
            policy_source = UNSET
        else:
            policy_source = check_service_binding_explanation_policy_source(_policy_source)

        _canonical_transport = d.pop("canonical_transport", UNSET)
        canonical_transport: ServiceBindingTransport | Unset
        if isinstance(_canonical_transport, Unset):
            canonical_transport = UNSET
        else:
            canonical_transport = check_service_binding_transport(_canonical_transport)

        service_binding_explanation = cls(
            caller=caller,
            target=target,
            method=method,
            path=path,
            result=result,
            scope=scope,
            checked_at=checked_at,
            checks=checks,
            limitations=limitations,
            caller_fingerprint=caller_fingerprint,
            target_fingerprint=target_fingerprint,
            caller_deployment_id=caller_deployment_id,
            target_app_id=target_app_id,
            target_deployment_id=target_deployment_id,
            release_id=release_id,
            policy_source=policy_source,
            canonical_transport=canonical_transport,
        )

        service_binding_explanation.additional_properties = d
        return service_binding_explanation

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
