from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_egress_allowlist_extra_response import AccountEgressAllowlistExtraResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/egress_allowlist_extra",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountEgressAllowlistExtraResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AccountEgressAllowlistExtraResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AccountEgressAllowlistExtraResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[AccountEgressAllowlistExtraResponse | Problem]:
    r"""Read the per-account egress allowlist extra budget.

     Returns the per-account additive budget on top of the
    plan's `apps.egress_allowlist` cap (issue #679 / PR-B /
    ADR-082). The plan cap (Pro 16 / Scale 64) is
    authoritative for the default case; the additive budget
    lets admins raise one account's effective cap without
    changing the plan default for everyone. `extra=0`
    means \"no override\" — the plan cap is authoritative.

    Admin scope is required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountEgressAllowlistExtraResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> AccountEgressAllowlistExtraResponse | Problem | None:
    r"""Read the per-account egress allowlist extra budget.

     Returns the per-account additive budget on top of the
    plan's `apps.egress_allowlist` cap (issue #679 / PR-B /
    ADR-082). The plan cap (Pro 16 / Scale 64) is
    authoritative for the default case; the additive budget
    lets admins raise one account's effective cap without
    changing the plan default for everyone. `extra=0`
    means \"no override\" — the plan cap is authoritative.

    Admin scope is required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountEgressAllowlistExtraResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[AccountEgressAllowlistExtraResponse | Problem]:
    r"""Read the per-account egress allowlist extra budget.

     Returns the per-account additive budget on top of the
    plan's `apps.egress_allowlist` cap (issue #679 / PR-B /
    ADR-082). The plan cap (Pro 16 / Scale 64) is
    authoritative for the default case; the additive budget
    lets admins raise one account's effective cap without
    changing the plan default for everyone. `extra=0`
    means \"no override\" — the plan cap is authoritative.

    Admin scope is required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountEgressAllowlistExtraResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> AccountEgressAllowlistExtraResponse | Problem | None:
    r"""Read the per-account egress allowlist extra budget.

     Returns the per-account additive budget on top of the
    plan's `apps.egress_allowlist` cap (issue #679 / PR-B /
    ADR-082). The plan cap (Pro 16 / Scale 64) is
    authoritative for the default case; the additive budget
    lets admins raise one account's effective cap without
    changing the plan default for everyone. `extra=0`
    means \"no override\" — the plan cap is authoritative.

    Admin scope is required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountEgressAllowlistExtraResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
