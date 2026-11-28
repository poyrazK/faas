from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AppStaticEgressIPResponse")


@_attrs_define
class AppStaticEgressIPResponse:
    """GET /v1/apps/{slug}/static-egress-ip response body.
    IP / SetAt are pointers so the wire shape is stable: a Scale
    customer with no pin yet sees `ip=null`, `set_at=null`,
    `plan_cap=1`, `plan_allowed=true`. PlanCap is the
    Limits.StaticEgressIPsPerApp value (1 in v1) so the dashboard
    can render "you can use 1 static IP per app" without the CLI
    round-tripping the plan table.

    """

    ip: None | str
    """The pinned IPv4 (dotted-quad). `null` when the customer
    has not pinned an IP yet. The DB family=4 CHECK
    guarantees this is never IPv6.
    """
    set_at: datetime.datetime | None
    """RFC 3339 timestamp for when the customer pinned the IP.
    `null` when IP is `null`.
    """
    plan_cap: int
    """Per-app quota cap (Limits.StaticEgressIPsPerApp). 1 in v1
    for Scale; 0 for plans that don't allow static egress IPs.
    """
    plan_allowed: bool
    """Whether the account's plan permits static egress IPs
    (Plan.StaticEgressIPAllowed). `true` for Scale; `false`
    for Free / Hobby / Pro so the CLI can render the upsell.
    """
    active: bool
    """Whether the pin takes effect (ADR-20261128090000): `true`
    when an IP is pinned and the plan allows static egress.
    A pin kept after a downgrade is inactive; the app leaves
    through shared egress until the account upgrades.
    """
    available_ips: list[str]
    """The static egress addresses assigned to the account; only
    these can be pinned. Empty until Gregale support assigns one.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ip: None | str
        ip = self.ip

        set_at: None | str
        if isinstance(self.set_at, datetime.datetime):
            set_at = self.set_at.isoformat()
        else:
            set_at = self.set_at

        plan_cap = self.plan_cap

        plan_allowed = self.plan_allowed

        active = self.active

        available_ips = self.available_ips

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ip": ip,
                "set_at": set_at,
                "plan_cap": plan_cap,
                "plan_allowed": plan_allowed,
                "active": active,
                "available_ips": available_ips,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_ip(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        ip = _parse_ip(d.pop("ip"))

        def _parse_set_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                set_at_type_0 = datetime.datetime.fromisoformat(data)

                return set_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        set_at = _parse_set_at(d.pop("set_at"))

        plan_cap = d.pop("plan_cap")

        plan_allowed = d.pop("plan_allowed")

        active = d.pop("active")

        available_ips = cast(list[str], d.pop("available_ips"))

        app_static_egress_ip_response = cls(
            ip=ip,
            set_at=set_at,
            plan_cap=plan_cap,
            plan_allowed=plan_allowed,
            active=active,
            available_ips=available_ips,
        )

        app_static_egress_ip_response.additional_properties = d
        return app_static_egress_ip_response

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
