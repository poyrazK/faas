from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.flag_evidence_page import FlagEvidencePage
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    environment: str,
    key: str,
    *,
    customer_id: UUID | Unset = UNSET,
    value: bool | Unset = UNSET,
    used: bool | Unset = UNSET,
    since: str | Unset = "24h",
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_customer_id: str | Unset = UNSET
    if not isinstance(customer_id, Unset):
        json_customer_id = str(customer_id)
    params["customer_id"] = json_customer_id

    params["value"] = value

    params["used"] = used

    params["since"] = since

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/flags/{key}/requests".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
            key=quote(str(key), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FlagEvidencePage | Problem | None:
    if response.status_code == 200:
        response_200 = FlagEvidencePage.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[FlagEvidencePage | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    customer_id: UUID | Unset = UNSET,
    value: bool | Unset = UNSET,
    used: bool | Unset = UNSET,
    since: str | Unset = "24h",
    cursor: str | Unset = UNSET,
) -> Response[FlagEvidencePage | Problem]:
    """Inspect retained requests with application-reported flag evidence.

     Debugger entitlement and retention apply. Counts weight collapsed telemetry rows; rows with
    different decisions remain separate. These are operational observations, not exactly-once exposure
    counts or causal experiment results.

    Args:
        slug (str):
        environment (str):
        key (str):
        customer_id (UUID | Unset):
        value (bool | Unset):
        used (bool | Unset):
        since (str | Unset):  Default: '24h'.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagEvidencePage | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        key=key,
        customer_id=customer_id,
        value=value,
        used=used,
        since=since,
        cursor=cursor,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    customer_id: UUID | Unset = UNSET,
    value: bool | Unset = UNSET,
    used: bool | Unset = UNSET,
    since: str | Unset = "24h",
    cursor: str | Unset = UNSET,
) -> FlagEvidencePage | Problem | None:
    """Inspect retained requests with application-reported flag evidence.

     Debugger entitlement and retention apply. Counts weight collapsed telemetry rows; rows with
    different decisions remain separate. These are operational observations, not exactly-once exposure
    counts or causal experiment results.

    Args:
        slug (str):
        environment (str):
        key (str):
        customer_id (UUID | Unset):
        value (bool | Unset):
        used (bool | Unset):
        since (str | Unset):  Default: '24h'.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagEvidencePage | Problem
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        key=key,
        client=client,
        customer_id=customer_id,
        value=value,
        used=used,
        since=since,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    customer_id: UUID | Unset = UNSET,
    value: bool | Unset = UNSET,
    used: bool | Unset = UNSET,
    since: str | Unset = "24h",
    cursor: str | Unset = UNSET,
) -> Response[FlagEvidencePage | Problem]:
    """Inspect retained requests with application-reported flag evidence.

     Debugger entitlement and retention apply. Counts weight collapsed telemetry rows; rows with
    different decisions remain separate. These are operational observations, not exactly-once exposure
    counts or causal experiment results.

    Args:
        slug (str):
        environment (str):
        key (str):
        customer_id (UUID | Unset):
        value (bool | Unset):
        used (bool | Unset):
        since (str | Unset):  Default: '24h'.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagEvidencePage | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        key=key,
        customer_id=customer_id,
        value=value,
        used=used,
        since=since,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    customer_id: UUID | Unset = UNSET,
    value: bool | Unset = UNSET,
    used: bool | Unset = UNSET,
    since: str | Unset = "24h",
    cursor: str | Unset = UNSET,
) -> FlagEvidencePage | Problem | None:
    """Inspect retained requests with application-reported flag evidence.

     Debugger entitlement and retention apply. Counts weight collapsed telemetry rows; rows with
    different decisions remain separate. These are operational observations, not exactly-once exposure
    counts or causal experiment results.

    Args:
        slug (str):
        environment (str):
        key (str):
        customer_id (UUID | Unset):
        value (bool | Unset):
        used (bool | Unset):
        since (str | Unset):  Default: '24h'.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagEvidencePage | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            key=key,
            client=client,
            customer_id=customer_id,
            value=value,
            used=used,
            since=since,
            cursor=cursor,
        )
    ).parsed
