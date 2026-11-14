from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.account_health_deployment import AccountHealthDeployment
    from ..models.account_health_domain import AccountHealthDomain
    from ..models.account_health_requests import AccountHealthRequests
    from ..models.account_health_restriction import AccountHealthRestriction


T = TypeVar("T", bound="AccountHealth")


@_attrs_define
class AccountHealth:
    """What may stop the calling account from working: restrictions with the next step, failed deployments, custom-domain
    problems and refused API requests.

    """

    generated_at: datetime.datetime
    """When the checks ran."""
    attention: int
    """Number of restrictions, failed deployments and domain issues; zero means nothing needs action."""
    restrictions: list[AccountHealthRestriction]
    """Restrictions currently applied to the account, one per kind."""
    failed_deployments: list[AccountHealthDeployment]
    """Failed deployments from the last 7 days, newest first, at most 10."""
    domain_issues: list[AccountHealthDomain]
    """Custom domains that are unverified or whose certificate is not serving."""
    refused_requests: AccountHealthRequests
    """API requests Gregale refused for the account in the recent window."""
    unavailable: list[str] | Unset = UNSET
    """Checks that could not be loaded; their lists are empty and mean unknown, not none."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        generated_at = self.generated_at.isoformat()

        attention = self.attention

        restrictions = []
        for restrictions_item_data in self.restrictions:
            restrictions_item = restrictions_item_data.to_dict()
            restrictions.append(restrictions_item)

        failed_deployments = []
        for failed_deployments_item_data in self.failed_deployments:
            failed_deployments_item = failed_deployments_item_data.to_dict()
            failed_deployments.append(failed_deployments_item)

        domain_issues = []
        for domain_issues_item_data in self.domain_issues:
            domain_issues_item = domain_issues_item_data.to_dict()
            domain_issues.append(domain_issues_item)

        refused_requests = self.refused_requests.to_dict()

        unavailable: list[str] | Unset = UNSET
        if not isinstance(self.unavailable, Unset):
            unavailable = self.unavailable

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "generated_at": generated_at,
                "attention": attention,
                "restrictions": restrictions,
                "failed_deployments": failed_deployments,
                "domain_issues": domain_issues,
                "refused_requests": refused_requests,
            }
        )
        if unavailable is not UNSET:
            field_dict["unavailable"] = unavailable

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.account_health_deployment import AccountHealthDeployment
        from ..models.account_health_domain import AccountHealthDomain
        from ..models.account_health_requests import AccountHealthRequests
        from ..models.account_health_restriction import AccountHealthRestriction

        d = dict(src_dict)
        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        attention = d.pop("attention")

        restrictions = []
        _restrictions = d.pop("restrictions")
        for restrictions_item_data in _restrictions:
            restrictions_item = AccountHealthRestriction.from_dict(restrictions_item_data)

            restrictions.append(restrictions_item)

        failed_deployments = []
        _failed_deployments = d.pop("failed_deployments")
        for failed_deployments_item_data in _failed_deployments:
            failed_deployments_item = AccountHealthDeployment.from_dict(failed_deployments_item_data)

            failed_deployments.append(failed_deployments_item)

        domain_issues = []
        _domain_issues = d.pop("domain_issues")
        for domain_issues_item_data in _domain_issues:
            domain_issues_item = AccountHealthDomain.from_dict(domain_issues_item_data)

            domain_issues.append(domain_issues_item)

        refused_requests = AccountHealthRequests.from_dict(d.pop("refused_requests"))

        unavailable = cast(list[str], d.pop("unavailable", UNSET))

        account_health = cls(
            generated_at=generated_at,
            attention=attention,
            restrictions=restrictions,
            failed_deployments=failed_deployments,
            domain_issues=domain_issues,
            refused_requests=refused_requests,
            unavailable=unavailable,
        )

        account_health.additional_properties = d
        return account_health

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
