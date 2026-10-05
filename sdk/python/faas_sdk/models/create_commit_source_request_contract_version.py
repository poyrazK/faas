from typing import Literal

CreateCommitSourceRequestContractVersion = Literal[1, 2]

CREATE_COMMIT_SOURCE_REQUEST_CONTRACT_VERSION_VALUES: set[CreateCommitSourceRequestContractVersion] = {
    1,
    2,
}


def check_create_commit_source_request_contract_version(value: int) -> CreateCommitSourceRequestContractVersion:
    if value in CREATE_COMMIT_SOURCE_REQUEST_CONTRACT_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_COMMIT_SOURCE_REQUEST_CONTRACT_VERSION_VALUES!r}"
    )
