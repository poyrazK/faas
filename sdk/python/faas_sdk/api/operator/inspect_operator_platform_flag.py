import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operator_platform_flag_decision import OperatorPlatformFlagDecision
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    key: str,
    *,
    account_id: UUID,
    org_id: UUID | Unset = UNSET,
    at: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_account_id = str(account_id)
    params["account_id"] = json_account_id

    json_org_id: str | Unset = UNSET
    if not isinstance(org_id, Unset):
        json_org_id = str(org_id)
    params["org_id"] = json_org_id

    json_at: str | Unset = UNSET
    if not isinstance(at, Unset):
        json_at = at.isoformat()
    params["at"] = json_at

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/flags/{key}/decision".format(
            key=quote(str(key), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperatorPlatformFlagDecision | Problem | None:
    if response.status_code == 200:
        response_200 = OperatorPlatformFlagDecision.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperatorPlatformFlagDecision | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    key: str,
    *,
    client: AuthenticatedClient | Client,
    account_id: UUID,
    org_id: UUID | Unset = UNSET,
    at: datetime.datetime | Unset = UNSET,
) -> Response[OperatorPlatformFlagDecision | Problem]:
    """Explain a platform flag for an account

     Evaluates the flag for one account against apid's live definition
    and reports the matching rule, the reason, and whether the account's
    plan is entitled to the capability the flag releases. Records
    nothing. Operator-only.

    Args:
        key (str):
        account_id (UUID):
        org_id (UUID | Unset):
        at (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperatorPlatformFlagDecision | Problem]
    """

    kwargs = _get_kwargs(
        key=key,
        account_id=account_id,
        org_id=org_id,
        at=at,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    key: str,
    *,
    client: AuthenticatedClient | Client,
    account_id: UUID,
    org_id: UUID | Unset = UNSET,
    at: datetime.datetime | Unset = UNSET,
) -> OperatorPlatformFlagDecision | Problem | None:
    """Explain a platform flag for an account

     Evaluates the flag for one account against apid's live definition
    and reports the matching rule, the reason, and whether the account's
    plan is entitled to the capability the flag releases. Records
    nothing. Operator-only.

    Args:
        key (str):
        account_id (UUID):
        org_id (UUID | Unset):
        at (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperatorPlatformFlagDecision | Problem
    """

    return sync_detailed(
        key=key,
        client=client,
        account_id=account_id,
        org_id=org_id,
        at=at,
    ).parsed


async def asyncio_detailed(
    key: str,
    *,
    client: AuthenticatedClient | Client,
    account_id: UUID,
    org_id: UUID | Unset = UNSET,
    at: datetime.datetime | Unset = UNSET,
) -> Response[OperatorPlatformFlagDecision | Problem]:
    """Explain a platform flag for an account

     Evaluates the flag for one account against apid's live definition
    and reports the matching rule, the reason, and whether the account's
    plan is entitled to the capability the flag releases. Records
    nothing. Operator-only.

    Args:
        key (str):
        account_id (UUID):
        org_id (UUID | Unset):
        at (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperatorPlatformFlagDecision | Problem]
    """

    kwargs = _get_kwargs(
        key=key,
        account_id=account_id,
        org_id=org_id,
        at=at,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    key: str,
    *,
    client: AuthenticatedClient | Client,
    account_id: UUID,
    org_id: UUID | Unset = UNSET,
    at: datetime.datetime | Unset = UNSET,
) -> OperatorPlatformFlagDecision | Problem | None:
    """Explain a platform flag for an account

     Evaluates the flag for one account against apid's live definition
    and reports the matching rule, the reason, and whether the account's
    plan is entitled to the capability the flag releases. Records
    nothing. Operator-only.

    Args:
        key (str):
        account_id (UUID):
        org_id (UUID | Unset):
        at (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperatorPlatformFlagDecision | Problem
    """

    return (
        await asyncio_detailed(
            key=key,
            client=client,
            account_id=account_id,
            org_id=org_id,
            at=at,
        )
    ).parsed
