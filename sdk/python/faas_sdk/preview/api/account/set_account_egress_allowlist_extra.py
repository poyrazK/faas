from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_egress_allowlist_extra_response import AccountEgressAllowlistExtraResponse
from ...models.problem import Problem
from ...models.set_account_egress_allowlist_extra_request import SetAccountEgressAllowlistExtraRequest
from ...types import Response


def _get_kwargs(
    *,
    body: SetAccountEgressAllowlistExtraRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/account/egress_allowlist_extra",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountEgressAllowlistExtraResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AccountEgressAllowlistExtraResponse.from_dict(response.json())

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
    body: SetAccountEgressAllowlistExtraRequest,
) -> Response[AccountEgressAllowlistExtraResponse | Problem]:
    """Set the per-account egress allowlist extra budget.

     Writes the per-account additive budget. `extra=0` clears
    the override (the plan cap is authoritative again);
    negative values or values above `max_extra` (1024) are
    rejected with `account_egress_allowlist_extra_out_of_range`.
    The PATCH emits an `account.egress_allowlist_extra_set`
    audit row carrying `old_extra`, `new_extra`, `plan_cap`,
    and `max_extra` so the dashboard can render the toggle
    history.

    Admin scope + MFA are required.

    Args:
        body (SetAccountEgressAllowlistExtraRequest): Body of PATCH
            /v1/account/egress_allowlist_extra (issue #679 / PR-B / ADR-082). `extra` is the per-
            account additive budget on top of the plan's `apps.egress_allowlist` cap. `extra=0` clears
            the override (the plan cap is authoritative again); negative values or values above
            `max_extra` (1024) are rejected with `account_egress_allowlist_extra_out_of_range`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountEgressAllowlistExtraResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: SetAccountEgressAllowlistExtraRequest,
) -> AccountEgressAllowlistExtraResponse | Problem | None:
    """Set the per-account egress allowlist extra budget.

     Writes the per-account additive budget. `extra=0` clears
    the override (the plan cap is authoritative again);
    negative values or values above `max_extra` (1024) are
    rejected with `account_egress_allowlist_extra_out_of_range`.
    The PATCH emits an `account.egress_allowlist_extra_set`
    audit row carrying `old_extra`, `new_extra`, `plan_cap`,
    and `max_extra` so the dashboard can render the toggle
    history.

    Admin scope + MFA are required.

    Args:
        body (SetAccountEgressAllowlistExtraRequest): Body of PATCH
            /v1/account/egress_allowlist_extra (issue #679 / PR-B / ADR-082). `extra` is the per-
            account additive budget on top of the plan's `apps.egress_allowlist` cap. `extra=0` clears
            the override (the plan cap is authoritative again); negative values or values above
            `max_extra` (1024) are rejected with `account_egress_allowlist_extra_out_of_range`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountEgressAllowlistExtraResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: SetAccountEgressAllowlistExtraRequest,
) -> Response[AccountEgressAllowlistExtraResponse | Problem]:
    """Set the per-account egress allowlist extra budget.

     Writes the per-account additive budget. `extra=0` clears
    the override (the plan cap is authoritative again);
    negative values or values above `max_extra` (1024) are
    rejected with `account_egress_allowlist_extra_out_of_range`.
    The PATCH emits an `account.egress_allowlist_extra_set`
    audit row carrying `old_extra`, `new_extra`, `plan_cap`,
    and `max_extra` so the dashboard can render the toggle
    history.

    Admin scope + MFA are required.

    Args:
        body (SetAccountEgressAllowlistExtraRequest): Body of PATCH
            /v1/account/egress_allowlist_extra (issue #679 / PR-B / ADR-082). `extra` is the per-
            account additive budget on top of the plan's `apps.egress_allowlist` cap. `extra=0` clears
            the override (the plan cap is authoritative again); negative values or values above
            `max_extra` (1024) are rejected with `account_egress_allowlist_extra_out_of_range`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountEgressAllowlistExtraResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: SetAccountEgressAllowlistExtraRequest,
) -> AccountEgressAllowlistExtraResponse | Problem | None:
    """Set the per-account egress allowlist extra budget.

     Writes the per-account additive budget. `extra=0` clears
    the override (the plan cap is authoritative again);
    negative values or values above `max_extra` (1024) are
    rejected with `account_egress_allowlist_extra_out_of_range`.
    The PATCH emits an `account.egress_allowlist_extra_set`
    audit row carrying `old_extra`, `new_extra`, `plan_cap`,
    and `max_extra` so the dashboard can render the toggle
    history.

    Admin scope + MFA are required.

    Args:
        body (SetAccountEgressAllowlistExtraRequest): Body of PATCH
            /v1/account/egress_allowlist_extra (issue #679 / PR-B / ADR-082). `extra` is the per-
            account additive budget on top of the plan's `apps.egress_allowlist` cap. `extra=0` clears
            the override (the plan cap is authoritative again); negative values or values above
            `max_extra` (1024) are rejected with `account_egress_allowlist_extra_out_of_range`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountEgressAllowlistExtraResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
