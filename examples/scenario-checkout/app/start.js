import { createCheckoutServer } from "./server.js";

createCheckoutServer({
  paymentHost: process.env.PAYMENT_HOST,
  paymentPort: Number(process.env.PAYMENT_PORT),
  ledgerPath: process.env.ORDER_LEDGER_PATH || "/tmp/checkout-orders.json",
  ledgerURL: process.env.ORDER_LEDGER_URL,
  ledgerToken: process.env.ORDER_LEDGER_TOKEN,
}).listen(Number(process.env.PORT ?? 8080), process.env.HOST || "0.0.0.0", function () {
  console.log(JSON.stringify({ port: this.address().port }));
});
