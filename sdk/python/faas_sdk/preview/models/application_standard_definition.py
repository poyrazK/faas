from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.application_standard_cidr_rule import ApplicationStandardCIDRRule
    from ..models.application_standard_extra_port_rule import ApplicationStandardExtraPortRule
    from ..models.application_standard_log_destination_rule import ApplicationStandardLogDestinationRule
    from ..models.application_standard_publisher_rule import ApplicationStandardPublisherRule
    from ..models.application_standard_security_policy_rule import ApplicationStandardSecurityPolicyRule
    from ..models.application_standard_signature_rule import ApplicationStandardSignatureRule


T = TypeVar("T", bound="ApplicationStandardDefinition")


@_attrs_define
class ApplicationStandardDefinition:
    """Supported versioned application requirements. Resource UUIDs must belong to the organization.
    Enforced destination, publisher and CIDR sets cannot be empty.
    Empty local CIDRs mean unrestricted access and cannot satisfy a restriction.

    """

    log_destinations: ApplicationStandardLogDestinationRule | Unset = UNSET
    """Organization-owned logging destination references. Modes and override combinations are validated on
    publication."""
    require_signed: ApplicationStandardSignatureRule | Unset = UNSET
    """Image signature requirement. Modes and override combinations are validated on publication."""
    security_policy: ApplicationStandardSecurityPolicyRule | Unset = UNSET
    """Deploy-time security posture requirement. Modes and override combinations are validated on publication."""
    trusted_publishers: ApplicationStandardPublisherRule | Unset = UNSET
    """Permitted organization-owned signing keys. Modes and override combinations are validated on publication."""
    egress_cidrs: ApplicationStandardCIDRRule | Unset = UNSET
    """Approved outbound CIDR ranges. Modes and override combinations are validated on publication."""
    egress_extra_ports: ApplicationStandardExtraPortRule | Unset = UNSET
    """Approved extra outbound TCP ports. Modes and override combinations are validated on publication."""

    def to_dict(self) -> dict[str, Any]:
        log_destinations: dict[str, Any] | Unset = UNSET
        if not isinstance(self.log_destinations, Unset):
            log_destinations = self.log_destinations.to_dict()

        require_signed: dict[str, Any] | Unset = UNSET
        if not isinstance(self.require_signed, Unset):
            require_signed = self.require_signed.to_dict()

        security_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.security_policy, Unset):
            security_policy = self.security_policy.to_dict()

        trusted_publishers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.trusted_publishers, Unset):
            trusted_publishers = self.trusted_publishers.to_dict()

        egress_cidrs: dict[str, Any] | Unset = UNSET
        if not isinstance(self.egress_cidrs, Unset):
            egress_cidrs = self.egress_cidrs.to_dict()

        egress_extra_ports: dict[str, Any] | Unset = UNSET
        if not isinstance(self.egress_extra_ports, Unset):
            egress_extra_ports = self.egress_extra_ports.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if log_destinations is not UNSET:
            field_dict["log_destinations"] = log_destinations
        if require_signed is not UNSET:
            field_dict["require_signed"] = require_signed
        if security_policy is not UNSET:
            field_dict["security_policy"] = security_policy
        if trusted_publishers is not UNSET:
            field_dict["trusted_publishers"] = trusted_publishers
        if egress_cidrs is not UNSET:
            field_dict["egress_cidrs"] = egress_cidrs
        if egress_extra_ports is not UNSET:
            field_dict["egress_extra_ports"] = egress_extra_ports

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_cidr_rule import ApplicationStandardCIDRRule
        from ..models.application_standard_extra_port_rule import ApplicationStandardExtraPortRule
        from ..models.application_standard_log_destination_rule import ApplicationStandardLogDestinationRule
        from ..models.application_standard_publisher_rule import ApplicationStandardPublisherRule
        from ..models.application_standard_security_policy_rule import ApplicationStandardSecurityPolicyRule
        from ..models.application_standard_signature_rule import ApplicationStandardSignatureRule

        d = dict(src_dict)
        _log_destinations = d.pop("log_destinations", UNSET)
        log_destinations: ApplicationStandardLogDestinationRule | Unset
        if isinstance(_log_destinations, Unset):
            log_destinations = UNSET
        else:
            log_destinations = ApplicationStandardLogDestinationRule.from_dict(_log_destinations)

        _require_signed = d.pop("require_signed", UNSET)
        require_signed: ApplicationStandardSignatureRule | Unset
        if isinstance(_require_signed, Unset):
            require_signed = UNSET
        else:
            require_signed = ApplicationStandardSignatureRule.from_dict(_require_signed)

        _security_policy = d.pop("security_policy", UNSET)
        security_policy: ApplicationStandardSecurityPolicyRule | Unset
        if isinstance(_security_policy, Unset):
            security_policy = UNSET
        else:
            security_policy = ApplicationStandardSecurityPolicyRule.from_dict(_security_policy)

        _trusted_publishers = d.pop("trusted_publishers", UNSET)
        trusted_publishers: ApplicationStandardPublisherRule | Unset
        if isinstance(_trusted_publishers, Unset):
            trusted_publishers = UNSET
        else:
            trusted_publishers = ApplicationStandardPublisherRule.from_dict(_trusted_publishers)

        _egress_cidrs = d.pop("egress_cidrs", UNSET)
        egress_cidrs: ApplicationStandardCIDRRule | Unset
        if isinstance(_egress_cidrs, Unset):
            egress_cidrs = UNSET
        else:
            egress_cidrs = ApplicationStandardCIDRRule.from_dict(_egress_cidrs)

        _egress_extra_ports = d.pop("egress_extra_ports", UNSET)
        egress_extra_ports: ApplicationStandardExtraPortRule | Unset
        if isinstance(_egress_extra_ports, Unset):
            egress_extra_ports = UNSET
        else:
            egress_extra_ports = ApplicationStandardExtraPortRule.from_dict(_egress_extra_ports)

        application_standard_definition = cls(
            log_destinations=log_destinations,
            require_signed=require_signed,
            security_policy=security_policy,
            trusted_publishers=trusted_publishers,
            egress_cidrs=egress_cidrs,
            egress_extra_ports=egress_extra_ports,
        )

        return application_standard_definition
