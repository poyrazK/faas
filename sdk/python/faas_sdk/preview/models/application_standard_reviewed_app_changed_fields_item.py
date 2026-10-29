from typing import Literal

ApplicationStandardReviewedAppChangedFieldsItem = Literal[
    "egress_cidrs", "egress_extra_ports", "log_destinations", "require_signed", "security_policy", "trusted_publishers"
]

APPLICATION_STANDARD_REVIEWED_APP_CHANGED_FIELDS_ITEM_VALUES: set[ApplicationStandardReviewedAppChangedFieldsItem] = {
    "egress_cidrs",
    "egress_extra_ports",
    "log_destinations",
    "require_signed",
    "security_policy",
    "trusted_publishers",
}


def check_application_standard_reviewed_app_changed_fields_item(
    value: str,
) -> ApplicationStandardReviewedAppChangedFieldsItem:
    if value in APPLICATION_STANDARD_REVIEWED_APP_CHANGED_FIELDS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_REVIEWED_APP_CHANGED_FIELDS_ITEM_VALUES!r}"
    )
