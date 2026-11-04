from typing import Literal

ApplicationStandardEnrollmentMaterializedFieldsItem = Literal[
    "egress_cidrs", "egress_extra_ports", "log_destinations", "require_signed", "security_policy", "trusted_publishers"
]

APPLICATION_STANDARD_ENROLLMENT_MATERIALIZED_FIELDS_ITEM_VALUES: set[
    ApplicationStandardEnrollmentMaterializedFieldsItem
] = {
    "egress_cidrs",
    "egress_extra_ports",
    "log_destinations",
    "require_signed",
    "security_policy",
    "trusted_publishers",
}


def check_application_standard_enrollment_materialized_fields_item(
    value: str,
) -> ApplicationStandardEnrollmentMaterializedFieldsItem:
    if value in APPLICATION_STANDARD_ENROLLMENT_MATERIALIZED_FIELDS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_ENROLLMENT_MATERIALIZED_FIELDS_ITEM_VALUES!r}"
    )
