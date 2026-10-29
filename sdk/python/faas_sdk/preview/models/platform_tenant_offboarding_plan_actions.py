from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PlatformTenantOffboardingPlanActions")


@_attrs_define
class PlatformTenantOffboardingPlanActions:
    """Counts and ownership-scoped changes that would be applied if the offboarding plan is confirmed."""

    suspend_tenant: bool
    revoke_consumer_keys: int
    revoke_access_tokens: int
    detach_managed_consumers: int
    retain_unmanaged_consumers: int
    detach_managed_surfaces: int
    retain_unmanaged_surfaces: int
    remove_managed_hostnames: int
    retain_unmanaged_hostnames: int
    disable_credential_delegation: bool
    disable_customer_provisioning: bool
    disable_hostname_delegation: bool
    preserve_usage_history: bool
    preserve_billing_statements: bool
    preserve_reconciliation_history: bool
    preserve_webhook_subscriptions: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        suspend_tenant = self.suspend_tenant

        revoke_consumer_keys = self.revoke_consumer_keys

        revoke_access_tokens = self.revoke_access_tokens

        detach_managed_consumers = self.detach_managed_consumers

        retain_unmanaged_consumers = self.retain_unmanaged_consumers

        detach_managed_surfaces = self.detach_managed_surfaces

        retain_unmanaged_surfaces = self.retain_unmanaged_surfaces

        remove_managed_hostnames = self.remove_managed_hostnames

        retain_unmanaged_hostnames = self.retain_unmanaged_hostnames

        disable_credential_delegation = self.disable_credential_delegation

        disable_customer_provisioning = self.disable_customer_provisioning

        disable_hostname_delegation = self.disable_hostname_delegation

        preserve_usage_history = self.preserve_usage_history

        preserve_billing_statements = self.preserve_billing_statements

        preserve_reconciliation_history = self.preserve_reconciliation_history

        preserve_webhook_subscriptions = self.preserve_webhook_subscriptions

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "suspend_tenant": suspend_tenant,
                "revoke_consumer_keys": revoke_consumer_keys,
                "revoke_access_tokens": revoke_access_tokens,
                "detach_managed_consumers": detach_managed_consumers,
                "retain_unmanaged_consumers": retain_unmanaged_consumers,
                "detach_managed_surfaces": detach_managed_surfaces,
                "retain_unmanaged_surfaces": retain_unmanaged_surfaces,
                "remove_managed_hostnames": remove_managed_hostnames,
                "retain_unmanaged_hostnames": retain_unmanaged_hostnames,
                "disable_credential_delegation": disable_credential_delegation,
                "disable_customer_provisioning": disable_customer_provisioning,
                "disable_hostname_delegation": disable_hostname_delegation,
                "preserve_usage_history": preserve_usage_history,
                "preserve_billing_statements": preserve_billing_statements,
                "preserve_reconciliation_history": preserve_reconciliation_history,
                "preserve_webhook_subscriptions": preserve_webhook_subscriptions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        suspend_tenant = d.pop("suspend_tenant")

        revoke_consumer_keys = d.pop("revoke_consumer_keys")

        revoke_access_tokens = d.pop("revoke_access_tokens")

        detach_managed_consumers = d.pop("detach_managed_consumers")

        retain_unmanaged_consumers = d.pop("retain_unmanaged_consumers")

        detach_managed_surfaces = d.pop("detach_managed_surfaces")

        retain_unmanaged_surfaces = d.pop("retain_unmanaged_surfaces")

        remove_managed_hostnames = d.pop("remove_managed_hostnames")

        retain_unmanaged_hostnames = d.pop("retain_unmanaged_hostnames")

        disable_credential_delegation = d.pop("disable_credential_delegation")

        disable_customer_provisioning = d.pop("disable_customer_provisioning")

        disable_hostname_delegation = d.pop("disable_hostname_delegation")

        preserve_usage_history = d.pop("preserve_usage_history")

        preserve_billing_statements = d.pop("preserve_billing_statements")

        preserve_reconciliation_history = d.pop("preserve_reconciliation_history")

        preserve_webhook_subscriptions = d.pop("preserve_webhook_subscriptions")

        platform_tenant_offboarding_plan_actions = cls(
            suspend_tenant=suspend_tenant,
            revoke_consumer_keys=revoke_consumer_keys,
            revoke_access_tokens=revoke_access_tokens,
            detach_managed_consumers=detach_managed_consumers,
            retain_unmanaged_consumers=retain_unmanaged_consumers,
            detach_managed_surfaces=detach_managed_surfaces,
            retain_unmanaged_surfaces=retain_unmanaged_surfaces,
            remove_managed_hostnames=remove_managed_hostnames,
            retain_unmanaged_hostnames=retain_unmanaged_hostnames,
            disable_credential_delegation=disable_credential_delegation,
            disable_customer_provisioning=disable_customer_provisioning,
            disable_hostname_delegation=disable_hostname_delegation,
            preserve_usage_history=preserve_usage_history,
            preserve_billing_statements=preserve_billing_statements,
            preserve_reconciliation_history=preserve_reconciliation_history,
            preserve_webhook_subscriptions=preserve_webhook_subscriptions,
        )

        platform_tenant_offboarding_plan_actions.additional_properties = d
        return platform_tenant_offboarding_plan_actions

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
