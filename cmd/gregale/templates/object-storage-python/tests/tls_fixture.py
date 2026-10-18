"""Ephemeral public CA and server certificates; needs OpenSSL only for tests."""

import datetime
import ssl
import subprocess
from pathlib import Path


class Certificates:
    def __init__(self, directory, *, hostname="IP:127.0.0.1", expired=False):
        self.root = Path(directory)
        self.root.mkdir(mode=0o700)
        self.ca, self.certificate = self.root / "ca.pem", self.root / "server.pem"
        self.key = self.root / "server.key"
        ca_key = self.root / "ca.key"
        self.run("req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes", "-sha256",
                 "-days", "2", "-subj", "/CN=owned-storage-test-ca", "-keyout", ca_key, "-out", self.ca,
                 "-addext", "basicConstraints=critical,CA:TRUE", "-addext", "keyUsage=critical,keyCertSign,cRLSign")
        request = self.root / "server.csr"
        self.run("req", "-new", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes", "-sha256",
                 "-subj", "/CN=owned-storage-test-server", "-keyout", self.key, "-out", request)
        database, serial = self.root / "index", self.root / "serial"
        database.write_text("")
        serial.write_text("01\n")
        config = self.root / "openssl.cnf"
        config.write_text(f"""[ca]
default_ca = owned
[owned]
certificate = {self.ca}
private_key = {ca_key}
database = {database}
serial = {serial}
new_certs_dir = {self.root}
default_md = sha256
default_days = 1
policy = policy
[policy]
commonName = supplied
[server]
basicConstraints = critical,CA:FALSE
keyUsage = critical,digitalSignature
extendedKeyUsage = serverAuth
subjectAltName = {hostname}
""")
        dates = []
        if expired:
            now = datetime.datetime.now(datetime.UTC)
            dates = ["-startdate", (now - datetime.timedelta(days=7)).strftime("%Y%m%d%H%M%SZ"),
                     "-enddate", (now - datetime.timedelta(days=1)).strftime("%Y%m%d%H%M%SZ")]
        self.run("ca", "-batch", "-notext", "-config", config, "-extensions", "server",
                 "-in", request, "-out", self.certificate, *dates)
        ca_key.chmod(0o600)
        self.key.chmod(0o600)

    @staticmethod
    def run(*arguments):
        try:
            subprocess.run(["openssl", *map(str, arguments)], check=True, capture_output=True, timeout=10)
        except FileNotFoundError:
            raise RuntimeError("OpenSSL is required for storage HTTPS acceptance") from None
        except subprocess.SubprocessError:
            raise RuntimeError("Could not create owned storage TLS fixture") from None

    def context(self):
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.load_cert_chain(self.certificate, self.key)
        return context
