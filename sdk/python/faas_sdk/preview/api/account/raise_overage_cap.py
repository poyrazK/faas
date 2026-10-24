from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_response import AccountResponse
from ...models.problem import Problem
from ...models.raise_overage_cap_request import RaiseOverageCapRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: RaiseOverageCapRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/overage-cap",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AccountResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AccountResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: RaiseOverageCapRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[AccountResponse | Problem]:
    r"""Set or clear the account's spend cap (issue

     Sets accounts.overage_cap_cents in integer cents. Body shape:

        {\"overage_cap_cents\": <int|null>}

    Pass `null` (or omit the field) to clear the cap (NULL).
    Pass 0 to set \"no overage allowed.\" Passing a positive integer
    sets the monthly ceiling. The migration CHECK constraint at
    `migrations/00054_account_credits.sql` pins
    `overage_cap_cents IS NULL OR overage_cap_cents >= 0`; a
    negative value is rejected at the apid validator before the
    store ever sees it, returning 400 `validation_failed`.

    Once current-month overage meets/exceeds the cap, schedd refuses
    new wakes with `code: admission_refused` (HTTP 402). The cap is
    account-self-scoped (no admin scope required) and the response
    is the post-update account state. Audit row
    `overage.cap_changed` is emitted on every successful call.

    Args:
        idempotency_key (str | Unset):
        body (RaiseOverageCapRequest): Spend-cap payload (issue #561). *int64 so a missing/null
            field round-trips as NULL (no cap). 0 is a valid write and
            means "no overage allowed". Negative values are rejected at
            the validator before reaching the store (the migration CHECK
            at accounts/00049 is the storage-layer enforcement).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: RaiseOverageCapRequest,
    idempotency_key: str | Unset = UNSET,
) -> AccountResponse | Problem | None:
    r"""Set or clear the account's spend cap (issue

     Sets accounts.overage_cap_cents in integer cents. Body shape:

        {\"overage_cap_cents\": <int|null>}

    Pass `null` (or omit the field) to clear the cap (NULL).
    Pass 0 to set \"no overage allowed.\" Passing a positive integer
    sets the monthly ceiling. The migration CHECK constraint at
    `migrations/00054_account_credits.sql` pins
    `overage_cap_cents IS NULL OR overage_cap_cents >= 0`; a
    negative value is rejected at the apid validator before the
    store ever sees it, returning 400 `validation_failed`.

    Once current-month overage meets/exceeds the cap, schedd refuses
    new wakes with `code: admission_refused` (HTTP 402). The cap is
    account-self-scoped (no admin scope required) and the response
    is the post-update account state. Audit row
    `overage.cap_changed` is emitted on every successful call.

    Args:
        idempotency_key (str | Unset):
        body (RaiseOverageCapRequest): Spend-cap payload (issue #561). *int64 so a missing/null
            field round-trips as NULL (no cap). 0 is a valid write and
            means "no overage allowed". Negative values are rejected at
            the validator before reaching the store (the migration CHECK
            at accounts/00049 is the storage-layer enforcement).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: RaiseOverageCapRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[AccountResponse | Problem]:
    r"""Set or clear the account's spend cap (issue

     Sets accounts.overage_cap_cents in integer cents. Body shape:

        {\"overage_cap_cents\": <int|null>}

    Pass `null` (or omit the field) to clear the cap (NULL).
    Pass 0 to set \"no overage allowed.\" Passing a positive integer
    sets the monthly ceiling. The migration CHECK constraint at
    `migrations/00054_account_credits.sql` pins
    `overage_cap_cents IS NULL OR overage_cap_cents >= 0`; a
    negative value is rejected at the apid validator before the
    store ever sees it, returning 400 `validation_failed`.

    Once current-month overage meets/exceeds the cap, schedd refuses
    new wakes with `code: admission_refused` (HTTP 402). The cap is
    account-self-scoped (no admin scope required) and the response
    is the post-update account state. Audit row
    `overage.cap_changed` is emitted on every successful call.

    Args:
        idempotency_key (str | Unset):
        body (RaiseOverageCapRequest): Spend-cap payload (issue #561). *int64 so a missing/null
            field round-trips as NULL (no cap). 0 is a valid write and
            means "no overage allowed". Negative values are rejected at
            the validator before reaching the store (the migration CHECK
            at accounts/00049 is the storage-layer enforcement).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: RaiseOverageCapRequest,
    idempotency_key: str | Unset = UNSET,
) -> AccountResponse | Problem | None:
    r"""Set or clear the account's spend cap (issue

     Sets accounts.overage_cap_cents in integer cents. Body shape:

        {\"overage_cap_cents\": <int|null>}

    Pass `null` (or omit the field) to clear the cap (NULL).
    Pass 0 to set \"no overage allowed.\" Passing a positive integer
    sets the monthly ceiling. The migration CHECK constraint at
    `migrations/00054_account_credits.sql` pins
    `overage_cap_cents IS NULL OR overage_cap_cents >= 0`; a
    negative value is rejected at the apid validator before the
    store ever sees it, returning 400 `validation_failed`.

    Once current-month overage meets/exceeds the cap, schedd refuses
    new wakes with `code: admission_refused` (HTTP 402). The cap is
    account-self-scoped (no admin scope required) and the response
    is the post-update account state. Audit row
    `overage.cap_changed` is emitted on every successful call.

    Args:
        idempotency_key (str | Unset):
        body (RaiseOverageCapRequest): Spend-cap payload (issue #561). *int64 so a missing/null
            field round-trips as NULL (no cap). 0 is a valid write and
            means "no overage allowed". Negative values are rejected at
            the validator before reaching the store (the migration CHECK
            at accounts/00049 is the storage-layer enforcement).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
