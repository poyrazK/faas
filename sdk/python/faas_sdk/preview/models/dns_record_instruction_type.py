from typing import Literal

DNSRecordInstructionType = Literal["A", "AAAA", "CNAME", "TXT"]

DNS_RECORD_INSTRUCTION_TYPE_VALUES: set[DNSRecordInstructionType] = {
    "A",
    "AAAA",
    "CNAME",
    "TXT",
}


def check_dns_record_instruction_type(value: str) -> DNSRecordInstructionType:
    if value in DNS_RECORD_INSTRUCTION_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DNS_RECORD_INSTRUCTION_TYPE_VALUES!r}")
