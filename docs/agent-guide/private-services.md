# Private services and TCP ports

Apps in your account reach each other privately; a port declared :internal never gets a public listener.

- Give an app no public URL:
    gregale app <app> --visibility internal
- Declare a private TCP port (Redis, a broker):
    gregale app <app> --ports <name>=tcp:<port>:internal
- Declare a public raw TCP port, then give it a listener:
    gregale app <app> --ports <name>=tcp:<port>
- Create the public listener for that port:
    gregale app <app> tcp add --name <name> --guest-port <port>
- Reach a private port from your machine:
    gregale app <app> port-forward <local-port>:<host>:<port>

Notes:
- A caller that lists the service in depends_on gets $GREGALE_SERVICE_<NAME>_HOST (raw TCP, any declared port) and $GREGALE_SERVICE_<NAME>_URL (HTTP).
- HTTP calls can also use http://<name>.internal:10081.
- A private service parks when idle and wakes on a connection; keep one running with `gregale app <app> --min 1`.
