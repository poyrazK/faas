from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.account_export_response_schema_version import (
    AccountExportResponseSchemaVersion,
    check_account_export_response_schema_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.account_response import AccountResponse
    from ..models.api_key_export_response import APIKeyExportResponse
    from ..models.api_key_response import APIKeyResponse
    from ..models.app_response import AppResponse
    from ..models.app_secret_export_response import AppSecretExportResponse
    from ..models.build_export_response import BuildExportResponse
    from ..models.cron_response import CronResponse
    from ..models.custom_domain_response import CustomDomainResponse
    from ..models.deployment_response import DeploymentResponse
    from ..models.gdpr_audit_export_response import GdprAuditExportResponse
    from ..models.instance_response import InstanceResponse
    from ..models.org_invitation_response import OrgInvitationResponse
    from ..models.org_membership_export_response import OrgMembershipExportResponse
    from ..models.org_response import OrgResponse
    from ..models.usage_export_response import UsageExportResponse


T = TypeVar("T", bound="AccountExportResponse")


@_attrs_define
class AccountExportResponse:
    """Versioned GDPR export bundle: the account and organization identity graph, owned runtime resources, redacted key
    metadata, sealed-secret envelopes, and audit trail.

    """

    schema_version: AccountExportResponseSchemaVersion
    """Export schema version used by restore and portability tooling."""
    exported_at: datetime.datetime
    account: AccountResponse
    """Account profile: id, email verification state, plan, status, limits snapshot, current-month usage, deployed-
    app count, and developer-environment count."""
    organizations: list[OrgResponse]
    org_memberships: list[OrgMembershipExportResponse]
    org_invitations: list[OrgInvitationResponse]
    org_api_keys: list[APIKeyResponse]
    """Org-bound API key metadata; plaintext and hashes are never exported."""
    apps: list[AppResponse]
    deployments: list[DeploymentResponse]
    builds: list[BuildExportResponse]
    instances: list[InstanceResponse]
    usage: list[UsageExportResponse]
    domains: list[CustomDomainResponse]
    crons: list[CronResponse]
    api_keys: list[APIKeyExportResponse]
    app_secrets: list[AppSecretExportResponse]
    audit_trail: list[GdprAuditExportResponse] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        schema_version: int = self.schema_version

        exported_at = self.exported_at.isoformat()

        account = self.account.to_dict()

        organizations = []
        for organizations_item_data in self.organizations:
            organizations_item = organizations_item_data.to_dict()
            organizations.append(organizations_item)

        org_memberships = []
        for org_memberships_item_data in self.org_memberships:
            org_memberships_item = org_memberships_item_data.to_dict()
            org_memberships.append(org_memberships_item)

        org_invitations = []
        for org_invitations_item_data in self.org_invitations:
            org_invitations_item = org_invitations_item_data.to_dict()
            org_invitations.append(org_invitations_item)

        org_api_keys = []
        for org_api_keys_item_data in self.org_api_keys:
            org_api_keys_item = org_api_keys_item_data.to_dict()
            org_api_keys.append(org_api_keys_item)

        apps = []
        for apps_item_data in self.apps:
            apps_item = apps_item_data.to_dict()
            apps.append(apps_item)

        deployments = []
        for deployments_item_data in self.deployments:
            deployments_item = deployments_item_data.to_dict()
            deployments.append(deployments_item)

        builds = []
        for builds_item_data in self.builds:
            builds_item = builds_item_data.to_dict()
            builds.append(builds_item)

        instances = []
        for instances_item_data in self.instances:
            instances_item = instances_item_data.to_dict()
            instances.append(instances_item)

        usage = []
        for usage_item_data in self.usage:
            usage_item = usage_item_data.to_dict()
            usage.append(usage_item)

        domains = []
        for domains_item_data in self.domains:
            domains_item = domains_item_data.to_dict()
            domains.append(domains_item)

        crons = []
        for crons_item_data in self.crons:
            crons_item = crons_item_data.to_dict()
            crons.append(crons_item)

        api_keys = []
        for api_keys_item_data in self.api_keys:
            api_keys_item = api_keys_item_data.to_dict()
            api_keys.append(api_keys_item)

        app_secrets = []
        for app_secrets_item_data in self.app_secrets:
            app_secrets_item = app_secrets_item_data.to_dict()
            app_secrets.append(app_secrets_item)

        audit_trail: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.audit_trail, Unset):
            audit_trail = []
            for audit_trail_item_data in self.audit_trail:
                audit_trail_item = audit_trail_item_data.to_dict()
                audit_trail.append(audit_trail_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "schema_version": schema_version,
                "exported_at": exported_at,
                "account": account,
                "organizations": organizations,
                "org_memberships": org_memberships,
                "org_invitations": org_invitations,
                "org_api_keys": org_api_keys,
                "apps": apps,
                "deployments": deployments,
                "builds": builds,
                "instances": instances,
                "usage": usage,
                "domains": domains,
                "crons": crons,
                "api_keys": api_keys,
                "app_secrets": app_secrets,
            }
        )
        if audit_trail is not UNSET:
            field_dict["audit_trail"] = audit_trail

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.account_response import AccountResponse
        from ..models.api_key_export_response import APIKeyExportResponse
        from ..models.api_key_response import APIKeyResponse
        from ..models.app_response import AppResponse
        from ..models.app_secret_export_response import AppSecretExportResponse
        from ..models.build_export_response import BuildExportResponse
        from ..models.cron_response import CronResponse
        from ..models.custom_domain_response import CustomDomainResponse
        from ..models.deployment_response import DeploymentResponse
        from ..models.gdpr_audit_export_response import GdprAuditExportResponse
        from ..models.instance_response import InstanceResponse
        from ..models.org_invitation_response import OrgInvitationResponse
        from ..models.org_membership_export_response import OrgMembershipExportResponse
        from ..models.org_response import OrgResponse
        from ..models.usage_export_response import UsageExportResponse

        d = dict(src_dict)
        schema_version = check_account_export_response_schema_version(d.pop("schema_version"))

        exported_at = datetime.datetime.fromisoformat(d.pop("exported_at"))

        account = AccountResponse.from_dict(d.pop("account"))

        organizations = []
        _organizations = d.pop("organizations")
        for organizations_item_data in _organizations:
            organizations_item = OrgResponse.from_dict(organizations_item_data)

            organizations.append(organizations_item)

        org_memberships = []
        _org_memberships = d.pop("org_memberships")
        for org_memberships_item_data in _org_memberships:
            org_memberships_item = OrgMembershipExportResponse.from_dict(org_memberships_item_data)

            org_memberships.append(org_memberships_item)

        org_invitations = []
        _org_invitations = d.pop("org_invitations")
        for org_invitations_item_data in _org_invitations:
            org_invitations_item = OrgInvitationResponse.from_dict(org_invitations_item_data)

            org_invitations.append(org_invitations_item)

        org_api_keys = []
        _org_api_keys = d.pop("org_api_keys")
        for org_api_keys_item_data in _org_api_keys:
            org_api_keys_item = APIKeyResponse.from_dict(org_api_keys_item_data)

            org_api_keys.append(org_api_keys_item)

        apps = []
        _apps = d.pop("apps")
        for apps_item_data in _apps:
            apps_item = AppResponse.from_dict(apps_item_data)

            apps.append(apps_item)

        deployments = []
        _deployments = d.pop("deployments")
        for deployments_item_data in _deployments:
            deployments_item = DeploymentResponse.from_dict(deployments_item_data)

            deployments.append(deployments_item)

        builds = []
        _builds = d.pop("builds")
        for builds_item_data in _builds:
            builds_item = BuildExportResponse.from_dict(builds_item_data)

            builds.append(builds_item)

        instances = []
        _instances = d.pop("instances")
        for instances_item_data in _instances:
            instances_item = InstanceResponse.from_dict(instances_item_data)

            instances.append(instances_item)

        usage = []
        _usage = d.pop("usage")
        for usage_item_data in _usage:
            usage_item = UsageExportResponse.from_dict(usage_item_data)

            usage.append(usage_item)

        domains = []
        _domains = d.pop("domains")
        for domains_item_data in _domains:
            domains_item = CustomDomainResponse.from_dict(domains_item_data)

            domains.append(domains_item)

        crons = []
        _crons = d.pop("crons")
        for crons_item_data in _crons:
            crons_item = CronResponse.from_dict(crons_item_data)

            crons.append(crons_item)

        api_keys = []
        _api_keys = d.pop("api_keys")
        for api_keys_item_data in _api_keys:
            api_keys_item = APIKeyExportResponse.from_dict(api_keys_item_data)

            api_keys.append(api_keys_item)

        app_secrets = []
        _app_secrets = d.pop("app_secrets")
        for app_secrets_item_data in _app_secrets:
            app_secrets_item = AppSecretExportResponse.from_dict(app_secrets_item_data)

            app_secrets.append(app_secrets_item)

        _audit_trail = d.pop("audit_trail", UNSET)
        audit_trail: list[GdprAuditExportResponse] | Unset = UNSET
        if _audit_trail is not UNSET:
            audit_trail = []
            for audit_trail_item_data in _audit_trail:
                audit_trail_item = GdprAuditExportResponse.from_dict(audit_trail_item_data)

                audit_trail.append(audit_trail_item)

        account_export_response = cls(
            schema_version=schema_version,
            exported_at=exported_at,
            account=account,
            organizations=organizations,
            org_memberships=org_memberships,
            org_invitations=org_invitations,
            org_api_keys=org_api_keys,
            apps=apps,
            deployments=deployments,
            builds=builds,
            instances=instances,
            usage=usage,
            domains=domains,
            crons=crons,
            api_keys=api_keys,
            app_secrets=app_secrets,
            audit_trail=audit_trail,
        )

        account_export_response.additional_properties = d
        return account_export_response

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
