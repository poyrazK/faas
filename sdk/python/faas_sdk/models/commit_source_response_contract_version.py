from typing import Literal

CommitSourceResponseContractVersion = Literal[1, 2]

COMMIT_SOURCE_RESPONSE_CONTRACT_VERSION_VALUES: set[CommitSourceResponseContractVersion] = {
    1,
    2,
}


def check_commit_source_response_contract_version(value: int) -> CommitSourceResponseContractVersion:
    if value in COMMIT_SOURCE_RESPONSE_CONTRACT_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {COMMIT_SOURCE_RESPONSE_CONTRACT_VERSION_VALUES!r}")
