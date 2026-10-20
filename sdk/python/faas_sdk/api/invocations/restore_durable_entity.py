from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.durable_entity_restore_request import DurableEntityRestoreRequest
from ...models.durable_entity_restore_response import DurableEntityRestoreResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: DurableEntityRestoreRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/entities/restore".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DurableEntityRestoreResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DurableEntityRestoreResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DurableEntityRestoreResponse | Problem]:
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
    body: DurableEntityRestoreRequest,
) -> Response[DurableEntityRestoreResponse | Problem]:
    """Restore exported application data with a fenced expected-version check.

     Owner-only mutation preview requiring deploy:write or admin and MFA where
    applicable. Applies app enablement, execution plan, account hold and active
    tenant rules. Export identity must exactly match resolved target scope.
    Requires an existing committed entity, positive expected_version and stable
    request_id. Commits only application data, preserving current receipts,
    alarms, outbox and delivery attempts. Advances business version once; invokes
    no guest unless application restore validation is enabled. With
    FAAS_DURABLE_ENTITY_RESTORE_VALIDATION_ENABLED=1, every new restore requires
    validation_deployment_id and a fresh pure validator verdict under the claim.
    The chosen deployment is resolved before and after validation; publication
    still uses the entity ownership/version CAS. Deployment routing and bucket
    publication are not one atomic transaction. Receipt replay skips validation.
    Receipt replay precedes expected-version comparison. Retry uncertain
    outcomes with the identical request body and ID. Check application schema
    compatibility before restoring. Audit is best effort after acknowledged
    success, not atomic with the object-store commit. Responses are private,
    no-store; request/response state must not be logged.

    Args:
        slug (str):
        body (DurableEntityRestoreRequest): Candidate export and comparison identity used for
            restore preview, validation or fenced publication.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DurableEntityRestoreResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DurableEntityRestoreRequest,
) -> DurableEntityRestoreResponse | Problem | None:
    """Restore exported application data with a fenced expected-version check.

     Owner-only mutation preview requiring deploy:write or admin and MFA where
    applicable. Applies app enablement, execution plan, account hold and active
    tenant rules. Export identity must exactly match resolved target scope.
    Requires an existing committed entity, positive expected_version and stable
    request_id. Commits only application data, preserving current receipts,
    alarms, outbox and delivery attempts. Advances business version once; invokes
    no guest unless application restore validation is enabled. With
    FAAS_DURABLE_ENTITY_RESTORE_VALIDATION_ENABLED=1, every new restore requires
    validation_deployment_id and a fresh pure validator verdict under the claim.
    The chosen deployment is resolved before and after validation; publication
    still uses the entity ownership/version CAS. Deployment routing and bucket
    publication are not one atomic transaction. Receipt replay skips validation.
    Receipt replay precedes expected-version comparison. Retry uncertain
    outcomes with the identical request body and ID. Check application schema
    compatibility before restoring. Audit is best effort after acknowledged
    success, not atomic with the object-store commit. Responses are private,
    no-store; request/response state must not be logged.

    Args:
        slug (str):
        body (DurableEntityRestoreRequest): Candidate export and comparison identity used for
            restore preview, validation or fenced publication.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DurableEntityRestoreResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DurableEntityRestoreRequest,
) -> Response[DurableEntityRestoreResponse | Problem]:
    """Restore exported application data with a fenced expected-version check.

     Owner-only mutation preview requiring deploy:write or admin and MFA where
    applicable. Applies app enablement, execution plan, account hold and active
    tenant rules. Export identity must exactly match resolved target scope.
    Requires an existing committed entity, positive expected_version and stable
    request_id. Commits only application data, preserving current receipts,
    alarms, outbox and delivery attempts. Advances business version once; invokes
    no guest unless application restore validation is enabled. With
    FAAS_DURABLE_ENTITY_RESTORE_VALIDATION_ENABLED=1, every new restore requires
    validation_deployment_id and a fresh pure validator verdict under the claim.
    The chosen deployment is resolved before and after validation; publication
    still uses the entity ownership/version CAS. Deployment routing and bucket
    publication are not one atomic transaction. Receipt replay skips validation.
    Receipt replay precedes expected-version comparison. Retry uncertain
    outcomes with the identical request body and ID. Check application schema
    compatibility before restoring. Audit is best effort after acknowledged
    success, not atomic with the object-store commit. Responses are private,
    no-store; request/response state must not be logged.

    Args:
        slug (str):
        body (DurableEntityRestoreRequest): Candidate export and comparison identity used for
            restore preview, validation or fenced publication.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DurableEntityRestoreResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DurableEntityRestoreRequest,
) -> DurableEntityRestoreResponse | Problem | None:
    """Restore exported application data with a fenced expected-version check.

     Owner-only mutation preview requiring deploy:write or admin and MFA where
    applicable. Applies app enablement, execution plan, account hold and active
    tenant rules. Export identity must exactly match resolved target scope.
    Requires an existing committed entity, positive expected_version and stable
    request_id. Commits only application data, preserving current receipts,
    alarms, outbox and delivery attempts. Advances business version once; invokes
    no guest unless application restore validation is enabled. With
    FAAS_DURABLE_ENTITY_RESTORE_VALIDATION_ENABLED=1, every new restore requires
    validation_deployment_id and a fresh pure validator verdict under the claim.
    The chosen deployment is resolved before and after validation; publication
    still uses the entity ownership/version CAS. Deployment routing and bucket
    publication are not one atomic transaction. Receipt replay skips validation.
    Receipt replay precedes expected-version comparison. Retry uncertain
    outcomes with the identical request body and ID. Check application schema
    compatibility before restoring. Audit is best effort after acknowledged
    success, not atomic with the object-store commit. Responses are private,
    no-store; request/response state must not be logged.

    Args:
        slug (str):
        body (DurableEntityRestoreRequest): Candidate export and comparison identity used for
            restore preview, validation or fenced publication.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DurableEntityRestoreResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
