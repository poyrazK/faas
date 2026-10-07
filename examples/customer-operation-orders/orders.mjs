// ADR-638: authorize business access before the receipt helper, including replays.
import {customerOperationRequestFromHeaders} from './sdk.mjs';

export class OrderRequestError extends Error {
  constructor(status, message) { super(message); this.status = status; }
}

export async function fulfillOrder({runtime, pool, request}) {
  const execution = customerOperationRequestFromHeaders(request.headers, request.method, request.path, request.body);
  let input;
  try { input = JSON.parse(Buffer.from(request.body).toString('utf8')); }
  catch { throw new OrderRequestError(400, 'Invalid order input'); }
  if (!input || Array.isArray(input) || Object.keys(input).length !== 2
      || typeof input.order_id !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(input.order_id ?? '')
      || typeof input.workflow_run_id !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(input.workflow_run_id ?? '')) {
    throw new OrderRequestError(400, 'Invalid order input');
  }
  const owner = [input.order_id, execution.platformTenantId];
  const authorized = await pool.query('SELECT id FROM public.example_orders WHERE id = $1 AND platform_tenant_id = $2', owner);
  if (authorized.rows.length !== 1) throw new OrderRequestError(404, 'Order not found');

  return runtime.runRequest(request.headers, async () => {
    const receipt = await runtime.transaction(request, pool, async tx => {
      // Recheck ownership under the row lock: authorization can change after the first read.
      const locked = await tx.query('SELECT status FROM public.example_orders WHERE id = $1 AND platform_tenant_id = $2 FOR UPDATE', owner);
      if (locked.rows.length !== 1) throw new OrderRequestError(404, 'Order not found');
      if (locked.rows[0].status === 'pending') {
        await tx.query("UPDATE public.example_orders SET status = 'fulfilled', fulfillment_count = 1 WHERE id = $1 AND platform_tenant_id = $2", owner);
        tx.workflowTransition('order-fulfillment', input.workflow_run_id, 'pending', 'fulfilled');
      }
      tx.milestone('order-fulfilled', {order_id: input.order_id, workflow_run_id: input.workflow_run_id, status: 'fulfilled'});
      tx.workflowState('order-fulfillment', input.workflow_run_id, 'fulfilled');
      return {order_id: input.order_id, status: 'fulfilled'};
    });
    // Report the committed stage once per execution; recovery has a fresh reporting fence.
    await runtime.progress({report_id: 'order-fulfilled', stage: 'complete', completed: 1, total: 1});
    return receipt;
  });
}
