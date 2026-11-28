import datetime
from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.egress_flow_log_response import EgressFlowLogResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    remote: str | Unset = UNSET,
    account_id: str | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    to: datetime.datetime | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["remote"] = remote

    params["account_id"] = account_id

    json_from_: str | Unset = UNSET
    if not isinstance(from_, Unset):
        json_from_ = from_.isoformat()
    params["from"] = json_from_

    json_to: str | Unset = UNSET
    if not isinstance(to, Unset):
        json_to = to.isoformat()
    params["to"] = json_to

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/egress-flows",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EgressFlowLogResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EgressFlowLogResponse.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EgressFlowLogResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    remote: str | Unset = UNSET,
    account_id: str | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    to: datetime.datetime | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[EgressFlowLogResponse | Problem]:
    """Search the egress flow log (operator-only).

     Destination addresses and TCP ports tenant guests opened new flows to,
    newest first. Filter by remote address or CIDR, account, and a
    [from, to) window of at most the 30-day retention. Defaults to the
    last 24 hours and 200 rows.

    Args:
        remote (str | Unset):
        account_id (str | Unset):
        from_ (datetime.datetime | Unset):
        to (datetime.datetime | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EgressFlowLogResponse | Problem]
    """

    kwargs = _get_kwargs(
        remote=remote,
        account_id=account_id,
        from_=from_,
        to=to,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    remote: str | Unset = UNSET,
    account_id: str | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    to: datetime.datetime | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> EgressFlowLogResponse | Problem | None:
    """Search the egress flow log (operator-only).

     Destination addresses and TCP ports tenant guests opened new flows to,
    newest first. Filter by remote address or CIDR, account, and a
    [from, to) window of at most the 30-day retention. Defaults to the
    last 24 hours and 200 rows.

    Args:
        remote (str | Unset):
        account_id (str | Unset):
        from_ (datetime.datetime | Unset):
        to (datetime.datetime | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EgressFlowLogResponse | Problem
    """

    return sync_detailed(
        client=client,
        remote=remote,
        account_id=account_id,
        from_=from_,
        to=to,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    remote: str | Unset = UNSET,
    account_id: str | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    to: datetime.datetime | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[EgressFlowLogResponse | Problem]:
    """Search the egress flow log (operator-only).

     Destination addresses and TCP ports tenant guests opened new flows to,
    newest first. Filter by remote address or CIDR, account, and a
    [from, to) window of at most the 30-day retention. Defaults to the
    last 24 hours and 200 rows.

    Args:
        remote (str | Unset):
        account_id (str | Unset):
        from_ (datetime.datetime | Unset):
        to (datetime.datetime | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EgressFlowLogResponse | Problem]
    """

    kwargs = _get_kwargs(
        remote=remote,
        account_id=account_id,
        from_=from_,
        to=to,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    remote: str | Unset = UNSET,
    account_id: str | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    to: datetime.datetime | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> EgressFlowLogResponse | Problem | None:
    """Search the egress flow log (operator-only).

     Destination addresses and TCP ports tenant guests opened new flows to,
    newest first. Filter by remote address or CIDR, account, and a
    [from, to) window of at most the 30-day retention. Defaults to the
    last 24 hours and 200 rows.

    Args:
        remote (str | Unset):
        account_id (str | Unset):
        from_ (datetime.datetime | Unset):
        to (datetime.datetime | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EgressFlowLogResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            remote=remote,
            account_id=account_id,
            from_=from_,
            to=to,
            limit=limit,
        )
    ).parsed
