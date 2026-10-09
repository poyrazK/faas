from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.durable_entity_alarm_inspection import DurableEntityAlarmInspection
    from ..models.durable_entity_outbox_inspection import DurableEntityOutboxInspection
    from ..models.durable_entity_scope import DurableEntityScope


T = TypeVar("T", bound="DurableEntityInspectResponse")


@_attrs_define
class DurableEntityInspectResponse:
    recovery_revision: str
    """Opaque comparison value that changes on any manifest write; not ownership authority."""
    entity: DurableEntityScope
    version: int
    """Business state uint64 version; zero means no transition has committed."""
    state_committed: bool
    """Whether a business transition has committed; does not imply delivery."""
    alarm: DurableEntityAlarmInspection
    outbox: DurableEntityOutboxInspection
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        recovery_revision = self.recovery_revision

        entity = self.entity.to_dict()

        version = self.version

        state_committed = self.state_committed

        alarm = self.alarm.to_dict()

        outbox = self.outbox.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "recovery_revision": recovery_revision,
                "entity": entity,
                "version": version,
                "state_committed": state_committed,
                "alarm": alarm,
                "outbox": outbox,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.durable_entity_alarm_inspection import DurableEntityAlarmInspection
        from ..models.durable_entity_outbox_inspection import DurableEntityOutboxInspection
        from ..models.durable_entity_scope import DurableEntityScope

        d = dict(src_dict)
        recovery_revision = d.pop("recovery_revision")

        entity = DurableEntityScope.from_dict(d.pop("entity"))

        version = d.pop("version")

        state_committed = d.pop("state_committed")

        alarm = DurableEntityAlarmInspection.from_dict(d.pop("alarm"))

        outbox = DurableEntityOutboxInspection.from_dict(d.pop("outbox"))

        durable_entity_inspect_response = cls(
            recovery_revision=recovery_revision,
            entity=entity,
            version=version,
            state_committed=state_committed,
            alarm=alarm,
            outbox=outbox,
        )

        durable_entity_inspect_response.additional_properties = d
        return durable_entity_inspect_response

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
