from typing import Literal

DomainDoctorCheckName = Literal["caa_permits", "dns_record", "ipv6_conflict", "points_to_gregale", "tls_certificate"]

DOMAIN_DOCTOR_CHECK_NAME_VALUES: set[DomainDoctorCheckName] = {
    "caa_permits",
    "dns_record",
    "ipv6_conflict",
    "points_to_gregale",
    "tls_certificate",
}


def check_domain_doctor_check_name(value: str) -> DomainDoctorCheckName:
    if value in DOMAIN_DOCTOR_CHECK_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DOMAIN_DOCTOR_CHECK_NAME_VALUES!r}")
