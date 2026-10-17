import { randomUUID } from 'node:crypto';

export class ReceiptConflict extends Error {}

// Atomically claim across processes. Keep the content hash even on failure;
// one authenticated delivery ID cannot be reused with a different logical transition.
export class PostgresReceipts {
  constructor(pool) { this.pool = pool; }
  async claim(deliveryID, hash) {
    const token = randomUUID();
    const result = await this.pool.query(`
      INSERT INTO gregale_issue_slack_receipts(delivery_id,transition_sha256,status,lease_token,lease_until)
      VALUES($1,$2,'pending',$3,now()+interval '30 seconds')
      ON CONFLICT(delivery_id) DO UPDATE SET lease_token=excluded.lease_token,lease_until=excluded.lease_until
      WHERE gregale_issue_slack_receipts.status='pending'
        AND gregale_issue_slack_receipts.lease_until<=now()
        AND gregale_issue_slack_receipts.transition_sha256=excluded.transition_sha256
      RETURNING delivery_id`, [deliveryID, hash, token]);
    if (result.rowCount === 1) return { status: 'claimed', token };
    const current = await this.pool.query('SELECT transition_sha256,status FROM gregale_issue_slack_receipts WHERE delivery_id=$1', [deliveryID]);
    if (current.rows[0]?.transition_sha256 !== hash) throw new ReceiptConflict('delivery content changed');
    return { status: current.rows[0].status === 'sent' ? 'sent' : 'busy' };
  }
  async complete(deliveryID, token) {
    const result = await this.pool.query(`UPDATE gregale_issue_slack_receipts SET status='sent',sent_at=now(),lease_until=NULL
      WHERE delivery_id=$1 AND lease_token=$2 AND status='pending'`, [deliveryID, token]);
    if (result.rowCount !== 1) throw new Error('delivery lease changed');
  }
  async release(deliveryID, token) {
    await this.pool.query(`UPDATE gregale_issue_slack_receipts SET lease_until=now()
      WHERE delivery_id=$1 AND lease_token=$2 AND status='pending'`, [deliveryID, token]);
  }
}
