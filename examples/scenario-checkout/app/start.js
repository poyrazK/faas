import { createCheckoutServer } from "./server.js";

createCheckoutServer({
  paymentHost: process.env.PAYMENT_HOST,
  paymentPort: Number(process.env.PAYMENT_PORT),
}).listen(Number(process.env.PORT || 8080), process.env.HOST || "0.0.0.0");
