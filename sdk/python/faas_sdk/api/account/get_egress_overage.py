from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.egress_overage_response import EgressOverageResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/egress-overage",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EgressOverageResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EgressOverageResponse.from_dict(response.json())

        return response_200

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
) -> Response[EgressOverageResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[EgressOverageResponse | Problem]:
    """Read the account's egress overage election.

     Without the election every plan stops at its monthly egress allowance:
    the account's apps are parked (`quota_exceeded_egress`) until the month
    ends or the plan grows. With it, egress above the allowance is billed as
    overage, within the spend cap, while egress billing is live
    (`billing: live`); while `billing` is `off` the allowance still holds.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EgressOverageResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> EgressOverageResponse | Problem | None:
    """Read the account's egress overage election.

     Without the election every plan stops at its monthly egress allowance:
    the account's apps are parked (`quota_exceeded_egress`) until the month
    ends or the plan grows. With it, egress above the allowance is billed as
    overage, within the spend cap, while egress billing is live
    (`billing: live`); while `billing` is `off` the allowance still holds.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EgressOverageResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[EgressOverageResponse | Problem]:
    """Read the account's egress overage election.

     Without the election every plan stops at its monthly egress allowance:
    the account's apps are parked (`quota_exceeded_egress`) until the month
    ends or the plan grows. With it, egress above the allowance is billed as
    overage, within the spend cap, while egress billing is live
    (`billing: live`); while `billing` is `off` the allowance still holds.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EgressOverageResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> EgressOverageResponse | Problem | None:
    """Read the account's egress overage election.

     Without the election every plan stops at its monthly egress allowance:
    the account's apps are parked (`quota_exceeded_egress`) until the month
    ends or the plan grows. With it, egress above the allowance is billed as
    overage, within the spend cap, while egress billing is live
    (`billing: live`); while `billing` is `off` the allowance still holds.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EgressOverageResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
