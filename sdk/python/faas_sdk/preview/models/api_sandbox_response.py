from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_sandbox_response_status import APISandboxResponseStatus, check_api_sandbox_response_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_sandbox_failure import APISandboxFailure
    from ..models.api_sandbox_partner_response import APISandboxPartnerResponse
    from ..models.api_sandbox_scenario_receipt import APISandboxScenarioReceipt
    from ..models.api_sandbox_verification import APISandboxVerification


T = TypeVar("T", bound="APISandboxResponse")


@_attrs_define
class APISandboxResponse:
    """Status and access details for a retained release's isolated API copy."""

    id: UUID
    source_deployment_id: UUID
    status: APISandboxResponseStatus
    url: str
    openapi_url: str
    expires_at: datetime.datetime
    partner_step_count: int | Unset = UNSET
    """The leading requests executed by the partner before the platform checks final state."""
    deployment_id: UUID | Unset = UNSET
    smoke_path: str | Unset = UNSET
    failure: APISandboxFailure | Unset = UNSET
    """Setup failure code and guidance for recovering with a new sandbox."""
    verification: APISandboxVerification | Unset = UNSET
    """Readiness evidence from probing the sandbox API and its OpenAPI document."""
    scenario: APISandboxScenarioReceipt | Unset = UNSET
    """Shareable scenario guide and revision without its database seed."""
    partner: APISandboxPartnerResponse | Unset = UNSET
    """Owner-only partner progress."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        source_deployment_id = str(self.source_deployment_id)

        status: str = self.status

        url = self.url

        openapi_url = self.openapi_url

        expires_at = self.expires_at.isoformat()

        partner_step_count = self.partner_step_count

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        smoke_path = self.smoke_path

        failure: dict[str, Any] | Unset = UNSET
        if not isinstance(self.failure, Unset):
            failure = self.failure.to_dict()

        verification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.verification, Unset):
            verification = self.verification.to_dict()

        scenario: dict[str, Any] | Unset = UNSET
        if not isinstance(self.scenario, Unset):
            scenario = self.scenario.to_dict()

        partner: dict[str, Any] | Unset = UNSET
        if not isinstance(self.partner, Unset):
            partner = self.partner.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "source_deployment_id": source_deployment_id,
                "status": status,
                "url": url,
                "openapi_url": openapi_url,
                "expires_at": expires_at,
            }
        )
        if partner_step_count is not UNSET:
            field_dict["partner_step_count"] = partner_step_count
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if smoke_path is not UNSET:
            field_dict["smoke_path"] = smoke_path
        if failure is not UNSET:
            field_dict["failure"] = failure
        if verification is not UNSET:
            field_dict["verification"] = verification
        if scenario is not UNSET:
            field_dict["scenario"] = scenario
        if partner is not UNSET:
            field_dict["partner"] = partner

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_failure import APISandboxFailure
        from ..models.api_sandbox_partner_response import APISandboxPartnerResponse
        from ..models.api_sandbox_scenario_receipt import APISandboxScenarioReceipt
        from ..models.api_sandbox_verification import APISandboxVerification

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        source_deployment_id = UUID(d.pop("source_deployment_id"))

        status = check_api_sandbox_response_status(d.pop("status"))

        url = d.pop("url")

        openapi_url = d.pop("openapi_url")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        partner_step_count = d.pop("partner_step_count", UNSET)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        smoke_path = d.pop("smoke_path", UNSET)

        _failure = d.pop("failure", UNSET)
        failure: APISandboxFailure | Unset
        if isinstance(_failure, Unset):
            failure = UNSET
        else:
            failure = APISandboxFailure.from_dict(_failure)

        _verification = d.pop("verification", UNSET)
        verification: APISandboxVerification | Unset
        if isinstance(_verification, Unset):
            verification = UNSET
        else:
            verification = APISandboxVerification.from_dict(_verification)

        _scenario = d.pop("scenario", UNSET)
        scenario: APISandboxScenarioReceipt | Unset
        if isinstance(_scenario, Unset):
            scenario = UNSET
        else:
            scenario = APISandboxScenarioReceipt.from_dict(_scenario)

        _partner = d.pop("partner", UNSET)
        partner: APISandboxPartnerResponse | Unset
        if isinstance(_partner, Unset):
            partner = UNSET
        else:
            partner = APISandboxPartnerResponse.from_dict(_partner)

        api_sandbox_response = cls(
            id=id,
            source_deployment_id=source_deployment_id,
            status=status,
            url=url,
            openapi_url=openapi_url,
            expires_at=expires_at,
            partner_step_count=partner_step_count,
            deployment_id=deployment_id,
            smoke_path=smoke_path,
            failure=failure,
            verification=verification,
            scenario=scenario,
            partner=partner,
        )

        api_sandbox_response.additional_properties = d
        return api_sandbox_response

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
