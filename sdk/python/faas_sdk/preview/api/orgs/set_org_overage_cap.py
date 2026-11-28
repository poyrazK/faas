from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_overage_cap_response import AccountOverageCapResponse
from ...models.problem import Problem
from ...models.raise_overage_cap_request import RaiseOverageCapRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: RaiseOverageCapRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{slug}/overage-cap".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountOverageCapResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AccountOverageCapResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AccountOverageCapResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RaiseOverageCapRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[AccountOverageCapResponse | Problem]:
    r"""Set or clear the organization's monthly spend cap.

     Requires the organization owner, admin credential scope, verified email,
    MFA and recent step-up. `{\"overage_cap_cents\": <int|null>}`: null clears
    the cap, 0 allows no overage. Once the organization's overage this month
    meets the cap, the scheduler stops its apps' wakes (HTTP 402
    `admission_refused`) until the cap is raised or the month ends. A paid
    organization starts with its plan's default cap.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (RaiseOverageCapRequest): Spend-cap payload. *int64 so a missing/null
            field round-trips as NULL (no cap). 0 is a valid write and
            means "no overage allowed". Negative values are rejected at
            the validator before reaching the store (the migration CHECK
            at accounts/00049 is the storage-layer enforcement).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountOverageCapResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RaiseOverageCapRequest,
    idempotency_key: str | Unset = UNSET,
) -> AccountOverageCapResponse | Problem | None:
    r"""Set or clear the organization's monthly spend cap.

     Requires the organization owner, admin credential scope, verified email,
    MFA and recent step-up. `{\"overage_cap_cents\": <int|null>}`: null clears
    the cap, 0 allows no overage. Once the organization's overage this month
    meets the cap, the scheduler stops its apps' wakes (HTTP 402
    `admission_refused`) until the cap is raised or the month ends. A paid
    organization starts with its plan's default cap.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (RaiseOverageCapRequest): Spend-cap payload. *int64 so a missing/null
            field round-trips as NULL (no cap). 0 is a valid write and
            means "no overage allowed". Negative values are rejected at
            the validator before reaching the store (the migration CHECK
            at accounts/00049 is the storage-layer enforcement).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountOverageCapResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RaiseOverageCapRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[AccountOverageCapResponse | Problem]:
    r"""Set or clear the organization's monthly spend cap.

     Requires the organization owner, admin credential scope, verified email,
    MFA and recent step-up. `{\"overage_cap_cents\": <int|null>}`: null clears
    the cap, 0 allows no overage. Once the organization's overage this month
    meets the cap, the scheduler stops its apps' wakes (HTTP 402
    `admission_refused`) until the cap is raised or the month ends. A paid
    organization starts with its plan's default cap.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (RaiseOverageCapRequest): Spend-cap payload. *int64 so a missing/null
            field round-trips as NULL (no cap). 0 is a valid write and
            means "no overage allowed". Negative values are rejected at
            the validator before reaching the store (the migration CHECK
            at accounts/00049 is the storage-layer enforcement).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountOverageCapResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RaiseOverageCapRequest,
    idempotency_key: str | Unset = UNSET,
) -> AccountOverageCapResponse | Problem | None:
    r"""Set or clear the organization's monthly spend cap.

     Requires the organization owner, admin credential scope, verified email,
    MFA and recent step-up. `{\"overage_cap_cents\": <int|null>}`: null clears
    the cap, 0 allows no overage. Once the organization's overage this month
    meets the cap, the scheduler stops its apps' wakes (HTTP 402
    `admission_refused`) until the cap is raised or the month ends. A paid
    organization starts with its plan's default cap.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (RaiseOverageCapRequest): Spend-cap payload. *int64 so a missing/null
            field round-trips as NULL (no cap). 0 is a valid write and
            means "no overage allowed". Negative values are rejected at
            the validator before reaching the store (the migration CHECK
            at accounts/00049 is the storage-layer enforcement).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountOverageCapResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
