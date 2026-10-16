// Register application task handlers in this module so the web and dedicated
// worker entry points load the same versions before they start.
import { registerMcpTaskHandler } from './tasks.js';

// Example:
// registerMcpTaskHandler('lookup', {
//   version: '1',
//   validate(args) {
//     if (typeof args?.key !== 'string' || !args.key.trim()) throw new Error('key is required');
//   },
//   async execute(args, { taskId, signal }) {
//     const result = await lookupRecord(args.key, { idempotencyKey: taskId, signal });
//     return { content: [{ type: 'text', text: result }] };
//   },
// });
