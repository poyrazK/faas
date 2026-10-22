import { createPaymentFixture } from "./server.js";

const fixture = createPaymentFixture({
  ledgerPath: process.env.PAYMENT_LEDGER_PATH || "/tmp/payment-charges.json",
  orderLedgerPath: process.env.ORDER_LEDGER_PATH || "/tmp/payment-orders.json",
  ledgerToken: process.env.ORDER_LEDGER_TOKEN,
});
let listeners = 0;
const ready = () => {
  if (++listeners === 2) console.log(JSON.stringify({ port: fixture.web.address().port, tcp_port: fixture.tcp.address().port }));
};
fixture.tcp.listen(Number(process.env.PAYMENT_TCP_PORT || 6379), "0.0.0.0", ready);
fixture.web.listen(Number(process.env.PORT || 8080), "0.0.0.0", ready);
