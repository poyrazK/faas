from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_secrets_for_account_response import ListSecretsForAccountResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    before: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["before"] = before

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/secrets",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListSecretsForAccountResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListSecretsForAccountResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ListSecretsForAccountResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> Response[ListSecretsForAccountResponse | Problem]:
    r"""List every sealed secret across the caller's account.

     Replaces the per-app fan-out from `/v1/apps/{slug}/secrets`
    with one account-scoped read (issue #393). Each row carries
    the owning app's `app_id` and `app_slug` so the dashboard
    can render \"foo-app / DATABASE_URL\" without a parallel
    `/v1/apps` round-trip.

    **Plaintext never appears here** — only the age-sealed
    envelope (base64). The plaintext value lives transiently in
    the PUT handler and never crosses the apid wire.

    Cursor: `?before=<slug>|<key>` — the (app_slug, key) pair,
    pipe-separated. The SQL splits it back via `split_part`.
    Sort order is (app_slug ASC, key ASC). Default limit 25,
    max 100 (strict 400 on bad input, matching `/v1/invoices`).

    Args:
        before (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListSecretsForAccountResponse | Problem]
    """

    kwargs = _get_kwargs(
        before=before,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> ListSecretsForAccountResponse | Problem | None:
    r"""List every sealed secret across the caller's account.

     Replaces the per-app fan-out from `/v1/apps/{slug}/secrets`
    with one account-scoped read (issue #393). Each row carries
    the owning app's `app_id` and `app_slug` so the dashboard
    can render \"foo-app / DATABASE_URL\" without a parallel
    `/v1/apps` round-trip.

    **Plaintext never appears here** — only the age-sealed
    envelope (base64). The plaintext value lives transiently in
    the PUT handler and never crosses the apid wire.

    Cursor: `?before=<slug>|<key>` — the (app_slug, key) pair,
    pipe-separated. The SQL splits it back via `split_part`.
    Sort order is (app_slug ASC, key ASC). Default limit 25,
    max 100 (strict 400 on bad input, matching `/v1/invoices`).

    Args:
        before (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListSecretsForAccountResponse | Problem
    """

    return sync_detailed(
        client=client,
        before=before,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> Response[ListSecretsForAccountResponse | Problem]:
    r"""List every sealed secret across the caller's account.

     Replaces the per-app fan-out from `/v1/apps/{slug}/secrets`
    with one account-scoped read (issue #393). Each row carries
    the owning app's `app_id` and `app_slug` so the dashboard
    can render \"foo-app / DATABASE_URL\" without a parallel
    `/v1/apps` round-trip.

    **Plaintext never appears here** — only the age-sealed
    envelope (base64). The plaintext value lives transiently in
    the PUT handler and never crosses the apid wire.

    Cursor: `?before=<slug>|<key>` — the (app_slug, key) pair,
    pipe-separated. The SQL splits it back via `split_part`.
    Sort order is (app_slug ASC, key ASC). Default limit 25,
    max 100 (strict 400 on bad input, matching `/v1/invoices`).

    Args:
        before (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListSecretsForAccountResponse | Problem]
    """

    kwargs = _get_kwargs(
        before=before,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> ListSecretsForAccountResponse | Problem | None:
    r"""List every sealed secret across the caller's account.

     Replaces the per-app fan-out from `/v1/apps/{slug}/secrets`
    with one account-scoped read (issue #393). Each row carries
    the owning app's `app_id` and `app_slug` so the dashboard
    can render \"foo-app / DATABASE_URL\" without a parallel
    `/v1/apps` round-trip.

    **Plaintext never appears here** — only the age-sealed
    envelope (base64). The plaintext value lives transiently in
    the PUT handler and never crosses the apid wire.

    Cursor: `?before=<slug>|<key>` — the (app_slug, key) pair,
    pipe-separated. The SQL splits it back via `split_part`.
    Sort order is (app_slug ASC, key ASC). Default limit 25,
    max 100 (strict 400 on bad input, matching `/v1/invoices`).

    Args:
        before (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListSecretsForAccountResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            before=before,
            limit=limit,
        )
    ).parsed
