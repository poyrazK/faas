import http from "node:http";
import net from "node:net";

const sockets = new Set();
const echo = net.createServer(client => {
  sockets.add(client);
  client.on("close", () => sockets.delete(client));
  client.on("error", () => client.destroy());
  client.setTimeout(60000, () => client.destroy());
  client.pipe(client);
});
const health = http.createServer((request, response) => {
  const ready = request.url === "/" || request.url === "/healthz";
  response.writeHead(ready ? 200 : 404);
  response.end(ready ? "ready\n" : "");
});
echo.listen(Number(process.env.GREGALE_TEST_TCP_PORT), "0.0.0.0", () => {
  health.listen(Number(process.env.PORT ?? "8080"), "0.0.0.0");
});
for (const signal of ["SIGINT", "SIGTERM"]) {
  process.once(signal, () => {
    for (const socket of sockets) socket.destroy();
    echo.close();
    health.close();
  });
}
