from typing import Literal

DNSRecordInstructionPurpose = Literal["routing", "verification"]

DNS_RECORD_INSTRUCTION_PURPOSE_VALUES: set[DNSRecordInstructionPurpose] = {
    "routing",
    "verification",
}


def check_dns_record_instruction_purpose(value: str) -> DNSRecordInstructionPurpose:
    if value in DNS_RECORD_INSTRUCTION_PURPOSE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DNS_RECORD_INSTRUCTION_PURPOSE_VALUES!r}")
