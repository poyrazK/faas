from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.domain_apply_request import DomainApplyRequest
from ...models.domain_apply_response import DomainApplyResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: DomainApplyRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/domains/apply",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DomainApplyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DomainApplyResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DomainApplyResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: DomainApplyRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[DomainApplyResponse | Problem]:
    """Atomically apply reviewed domain bindings.

     Reauthorizes every binding, captures its current claim and serving
    deployment set under routing and billing-subject locks, and requires the
    exact reviewed fingerprint. A stale plan returns 409 without changes.
    Moves retain TXT proof, verification, certificate state, wildcard host
    allowlists and issuance budgets; source default-domain selections are
    cleared. New claims follow normal DNS verification and pending quotas.
    No provider DNS, deployment traffic, or application policy is changed.
    Use the returned rollback manifest to review a move back. Rollback does
    not detach new claims or restore default selections. Reuse the plan
    fingerprint as Idempotency-Key for request-bound response replay.

    Args:
        idempotency_key (str | Unset):
        body (DomainApplyRequest): Apply exactly the reviewed bindings. Confirmation and the
            fingerprint from planDomains are required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DomainApplyResponse | Problem]
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
    body: DomainApplyRequest,
    idempotency_key: str | Unset = UNSET,
) -> DomainApplyResponse | Problem | None:
    """Atomically apply reviewed domain bindings.

     Reauthorizes every binding, captures its current claim and serving
    deployment set under routing and billing-subject locks, and requires the
    exact reviewed fingerprint. A stale plan returns 409 without changes.
    Moves retain TXT proof, verification, certificate state, wildcard host
    allowlists and issuance budgets; source default-domain selections are
    cleared. New claims follow normal DNS verification and pending quotas.
    No provider DNS, deployment traffic, or application policy is changed.
    Use the returned rollback manifest to review a move back. Rollback does
    not detach new claims or restore default selections. Reuse the plan
    fingerprint as Idempotency-Key for request-bound response replay.

    Args:
        idempotency_key (str | Unset):
        body (DomainApplyRequest): Apply exactly the reviewed bindings. Confirmation and the
            fingerprint from planDomains are required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DomainApplyResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: DomainApplyRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[DomainApplyResponse | Problem]:
    """Atomically apply reviewed domain bindings.

     Reauthorizes every binding, captures its current claim and serving
    deployment set under routing and billing-subject locks, and requires the
    exact reviewed fingerprint. A stale plan returns 409 without changes.
    Moves retain TXT proof, verification, certificate state, wildcard host
    allowlists and issuance budgets; source default-domain selections are
    cleared. New claims follow normal DNS verification and pending quotas.
    No provider DNS, deployment traffic, or application policy is changed.
    Use the returned rollback manifest to review a move back. Rollback does
    not detach new claims or restore default selections. Reuse the plan
    fingerprint as Idempotency-Key for request-bound response replay.

    Args:
        idempotency_key (str | Unset):
        body (DomainApplyRequest): Apply exactly the reviewed bindings. Confirmation and the
            fingerprint from planDomains are required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DomainApplyResponse | Problem]
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
    body: DomainApplyRequest,
    idempotency_key: str | Unset = UNSET,
) -> DomainApplyResponse | Problem | None:
    """Atomically apply reviewed domain bindings.

     Reauthorizes every binding, captures its current claim and serving
    deployment set under routing and billing-subject locks, and requires the
    exact reviewed fingerprint. A stale plan returns 409 without changes.
    Moves retain TXT proof, verification, certificate state, wildcard host
    allowlists and issuance budgets; source default-domain selections are
    cleared. New claims follow normal DNS verification and pending quotas.
    No provider DNS, deployment traffic, or application policy is changed.
    Use the returned rollback manifest to review a move back. Rollback does
    not detach new claims or restore default selections. Reuse the plan
    fingerprint as Idempotency-Key for request-bound response replay.

    Args:
        idempotency_key (str | Unset):
        body (DomainApplyRequest): Apply exactly the reviewed bindings. Confirmation and the
            fingerprint from planDomains are required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DomainApplyResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
