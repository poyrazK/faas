import { createHmac, timingSafeEqual } from 'node:crypto';

export const TASK_INBOX_METHOD = 'gregale/tasks/inbox';
export const TASK_INBOX_EXTENSION = 'io.gregale/task-inbox';
export const TASK_INBOX_DEFAULT_LIMIT = 20;
export const TASK_INBOX_MAX_LIMIT = 50;
export const TASK_INBOX_STATUSES = Object.freeze(['working', 'input_required', 'completed', 'failed', 'cancelled']);
export const TASK_INBOX_CURSOR_PATTERN = /^[A-Za-z0-9_-]{1,128}\.[A-Za-z0-9_-]{43}$/;

// A continuation carries the exact PostgreSQL timestamp, including its
// microseconds. Sign it for the caller and filter rather than fetching the
// anchor row again, since that Task can expire between pages.
export function taskInboxCursor(ownerKey, namespace, principal, status) {
  const sign = payload => createHmac('sha256', ownerKey)
    .update(JSON.stringify(['gregale-mcp-task-inbox:v1', namespace, principal, status ?? null, payload]))
    .digest();

  return {
    encode(expiresAt, taskID) {
      const payload = Buffer.from(`${expiresAt},${taskID}`).toString('base64url');
      return `${payload}.${sign(payload).toString('base64url')}`;
    },
    decode(cursor) {
      if (cursor === undefined) return [null, null];
      if (typeof cursor !== 'string' || !TASK_INBOX_CURSOR_PATTERN.test(cursor)) throw new Error('Invalid MCP Task inbox cursor');
      const [payload, signature] = cursor.split('.');
      const expected = sign(payload);
      const received = Buffer.from(signature, 'base64url');
      if (received.toString('base64url') !== signature || !timingSafeEqual(expected, received)) {
        throw new Error('Invalid MCP Task inbox cursor');
      }
      return Buffer.from(payload, 'base64url').toString('utf8').split(',');
    },
  };
}
