from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_financial_budget_request import CreateFinancialBudgetRequest
from ...models.financial_budget_response import FinancialBudgetResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: CreateFinancialBudgetRequest,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/billing/budgets",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FinancialBudgetResponse | Problem | None:
    if response.status_code == 201:
        response_201 = FinancialBudgetResponse.from_dict(response.json())

        return response_201

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[FinancialBudgetResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateFinancialBudgetRequest,
    idempotency_key: str,
) -> Response[FinancialBudgetResponse | Problem]:
    """Save a budget policy with atomic revision audit and supported activation checks.

     Requires admin scope and session MFA. Idempotency-Key is required (1..255 bytes).
    It fixes creation identity beyond replay-cache retention. Reusing an operation
    identity cannot overwrite a changed or deleted policy. Disabled policies are drafts.
    Activation supports monitored notify, monitored compute-only stop_previews, and strict app/job gross
    compute or account net compute (optionally combined with egress) suspend_workloads with zero drain
    and no alert thresholds. Other enabled responses return 422 financial_budget_activation_unavailable.
    Strict activation requires quiet covered workloads, complete accounting and capable native owners;
    unavailable authority returns 422 financial_compute_not_ready.
    Disabled drafts create no notifications or workload changes. Active notification policies never stop
    workloads.

    Args:
        idempotency_key (str):
        body (CreateFinancialBudgetRequest): New budget intent; activation supports monitored
            notify, monitored compute-only stop_previews, and strict suspend_workloads (app/job gross
            compute or account net compute optionally combined with egress) with zero drain and no
            alert thresholds. Strict activation requires quiet covered workloads, complete accounting,
            and native owners. Account strict activation also requires recorded allowances and native
            owners capable of funded jobs. Combining compute and egress additionally requires complete
            egress accounting and byte-capable native owners.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FinancialBudgetResponse | Problem]
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
    body: CreateFinancialBudgetRequest,
    idempotency_key: str,
) -> FinancialBudgetResponse | Problem | None:
    """Save a budget policy with atomic revision audit and supported activation checks.

     Requires admin scope and session MFA. Idempotency-Key is required (1..255 bytes).
    It fixes creation identity beyond replay-cache retention. Reusing an operation
    identity cannot overwrite a changed or deleted policy. Disabled policies are drafts.
    Activation supports monitored notify, monitored compute-only stop_previews, and strict app/job gross
    compute or account net compute (optionally combined with egress) suspend_workloads with zero drain
    and no alert thresholds. Other enabled responses return 422 financial_budget_activation_unavailable.
    Strict activation requires quiet covered workloads, complete accounting and capable native owners;
    unavailable authority returns 422 financial_compute_not_ready.
    Disabled drafts create no notifications or workload changes. Active notification policies never stop
    workloads.

    Args:
        idempotency_key (str):
        body (CreateFinancialBudgetRequest): New budget intent; activation supports monitored
            notify, monitored compute-only stop_previews, and strict suspend_workloads (app/job gross
            compute or account net compute optionally combined with egress) with zero drain and no
            alert thresholds. Strict activation requires quiet covered workloads, complete accounting,
            and native owners. Account strict activation also requires recorded allowances and native
            owners capable of funded jobs. Combining compute and egress additionally requires complete
            egress accounting and byte-capable native owners.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FinancialBudgetResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateFinancialBudgetRequest,
    idempotency_key: str,
) -> Response[FinancialBudgetResponse | Problem]:
    """Save a budget policy with atomic revision audit and supported activation checks.

     Requires admin scope and session MFA. Idempotency-Key is required (1..255 bytes).
    It fixes creation identity beyond replay-cache retention. Reusing an operation
    identity cannot overwrite a changed or deleted policy. Disabled policies are drafts.
    Activation supports monitored notify, monitored compute-only stop_previews, and strict app/job gross
    compute or account net compute (optionally combined with egress) suspend_workloads with zero drain
    and no alert thresholds. Other enabled responses return 422 financial_budget_activation_unavailable.
    Strict activation requires quiet covered workloads, complete accounting and capable native owners;
    unavailable authority returns 422 financial_compute_not_ready.
    Disabled drafts create no notifications or workload changes. Active notification policies never stop
    workloads.

    Args:
        idempotency_key (str):
        body (CreateFinancialBudgetRequest): New budget intent; activation supports monitored
            notify, monitored compute-only stop_previews, and strict suspend_workloads (app/job gross
            compute or account net compute optionally combined with egress) with zero drain and no
            alert thresholds. Strict activation requires quiet covered workloads, complete accounting,
            and native owners. Account strict activation also requires recorded allowances and native
            owners capable of funded jobs. Combining compute and egress additionally requires complete
            egress accounting and byte-capable native owners.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FinancialBudgetResponse | Problem]
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
    body: CreateFinancialBudgetRequest,
    idempotency_key: str,
) -> FinancialBudgetResponse | Problem | None:
    """Save a budget policy with atomic revision audit and supported activation checks.

     Requires admin scope and session MFA. Idempotency-Key is required (1..255 bytes).
    It fixes creation identity beyond replay-cache retention. Reusing an operation
    identity cannot overwrite a changed or deleted policy. Disabled policies are drafts.
    Activation supports monitored notify, monitored compute-only stop_previews, and strict app/job gross
    compute or account net compute (optionally combined with egress) suspend_workloads with zero drain
    and no alert thresholds. Other enabled responses return 422 financial_budget_activation_unavailable.
    Strict activation requires quiet covered workloads, complete accounting and capable native owners;
    unavailable authority returns 422 financial_compute_not_ready.
    Disabled drafts create no notifications or workload changes. Active notification policies never stop
    workloads.

    Args:
        idempotency_key (str):
        body (CreateFinancialBudgetRequest): New budget intent; activation supports monitored
            notify, monitored compute-only stop_previews, and strict suspend_workloads (app/job gross
            compute or account net compute optionally combined with egress) with zero drain and no
            alert thresholds. Strict activation requires quiet covered workloads, complete accounting,
            and native owners. Account strict activation also requires recorded allowances and native
            owners capable of funded jobs. Combining compute and egress additionally requires complete
            egress accounting and byte-capable native owners.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FinancialBudgetResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
