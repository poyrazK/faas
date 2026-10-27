from typing import Literal

DomainDoctorCheckStatus = Literal["fail", "na", "ok", "pending"]

DOMAIN_DOCTOR_CHECK_STATUS_VALUES: set[DomainDoctorCheckStatus] = {
    "fail",
    "na",
    "ok",
    "pending",
}


def check_domain_doctor_check_status(value: str) -> DomainDoctorCheckStatus:
    if value in DOMAIN_DOCTOR_CHECK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DOMAIN_DOCTOR_CHECK_STATUS_VALUES!r}")
