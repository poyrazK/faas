from typing import Literal

BindingVerificationQualificationConditionCode = Literal[
    "application_ack_candidate_unobserved",
    "application_ack_stale",
    "application_adoption_unknown",
    "queue_consumer_stale",
    "queue_consumer_unobserved",
    "runtime_stale",
    "runtime_updating",
]

BINDING_VERIFICATION_QUALIFICATION_CONDITION_CODE_VALUES: set[BindingVerificationQualificationConditionCode] = {
    "application_ack_candidate_unobserved",
    "application_ack_stale",
    "application_adoption_unknown",
    "queue_consumer_stale",
    "queue_consumer_unobserved",
    "runtime_stale",
    "runtime_updating",
}


def check_binding_verification_qualification_condition_code(
    value: str,
) -> BindingVerificationQualificationConditionCode:
    if value in BINDING_VERIFICATION_QUALIFICATION_CONDITION_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {BINDING_VERIFICATION_QUALIFICATION_CONDITION_CODE_VALUES!r}"
    )
