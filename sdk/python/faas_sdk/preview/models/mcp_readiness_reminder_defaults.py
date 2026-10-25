from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.mcp_readiness_reminder_policy import MCPReadinessReminderPolicy


T = TypeVar("T", bound="MCPReadinessReminderDefaults")


@_attrs_define
class MCPReadinessReminderDefaults:
    """Versioned reminder policy captured when a new readiness webhook incident opens."""

    version: int
    """Zero means no defaults have been configured for this rule."""
    reminder_policy: MCPReadinessReminderPolicy
    """Per-incident lifetime creation budget. Both zero disables; otherwise interval is 60–86400 seconds and budget
    is 1–10. Reconfiguration never resets the count."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        reminder_policy = self.reminder_policy.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "reminder_policy": reminder_policy,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_reminder_policy import MCPReadinessReminderPolicy

        d = dict(src_dict)
        version = d.pop("version")

        reminder_policy = MCPReadinessReminderPolicy.from_dict(d.pop("reminder_policy"))

        mcp_readiness_reminder_defaults = cls(
            version=version,
            reminder_policy=reminder_policy,
        )

        mcp_readiness_reminder_defaults.additional_properties = d
        return mcp_readiness_reminder_defaults

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
