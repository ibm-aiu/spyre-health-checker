# TLS reference

## Overview: two separate TLS channels

This system uses TLS in two distinct places. Understanding the difference is
essential before configuring certificates or troubleshooting connection errors.

```
Channel A -- spyre-health-checker gRPC server (always mTLS)

  External client --mTLS--> spyre-health-checker
  (e.g. spyre-device-plugin)   (pkg/server/server.go)

  Flags: --tls-cert  --tls-key  --tls-ca
  Env:   SPYRE_TLS_CERT  SPYRE_TLS_KEY  SPYRE_TLS_CA
  Always required. No insecure mode.

Channel B -- cardmgmt reporter -> aiu-cardmgmt-health-api sidecar
             (mTLS UNIX socket)

  spyre-health-checker --mTLS--> cardmgmt sidecar
  (internal/reporter/cardmgmt_client.go)  (src/health_check_api.py)

  Flags: --cardhealth-socket  --tls-cert  --tls-key  --tls-ca
  Env:   CARDHEALTH_GRPC_SOCKET  SPYRE_TLS_CERT  SPYRE_TLS_KEY
         SPYRE_TLS_CA
  mTLS required. Client cert must have non-empty Subject Organization.
  Same cert/key/CA files as Channel A.
```

Both channels are over UNIX domain sockets, which means traffic never crosses
the network. The same certificate material satisfies both channels because
`tls.crt` is issued with `extendedKeyUsage = serverAuth,clientAuth`.

---

## Certificate requirements (Channel A)

| Requirement | Value | Where enforced |
|---|---|---|
| Minimum TLS version | TLS 1.2 | Server sets `MinVersion: tls.VersionTLS12` |
| Client authentication | Required (mTLS) | Always required -- no insecure mode |
| Certificate format | PEM | All cert/key files |
| Client cert `Organisation` field | **Must be non-empty** | Post-handshake interceptor in `pkg/server/server.go` (`authorizeClientCert`) |

The **`Organisation` field check** is the most common source of unexpected
`Unauthenticated` errors. The TLS handshake itself may succeed (the cert is
CA-trusted), but the gRPC stream interceptor (`authorizeStream` /
`authorizeUnary`) then inspects the peer certificate and rejects any client
whose cert has an empty `Subject.Organization`.

For `gen-local-certs.sh`-generated certs the `Organisation` is set to `SpyreDev`,
so they pass. Custom or corporate-CA-issued certs must also include a non-empty
`O=` field.

---

## Certificate files produced by `gen-local-certs.sh`

Running `make gen-local-certs` (or `bash hack/gen-local-certs.sh`) produces the
following files in `./certs/`:

| File | Purpose | Used as |
|---|---|---|
| `ca.crt` | Self-signed CA certificate | `--tls-ca` |
| `ca.key` | CA private key | Not used at runtime -- keep offline |
| `tls.crt` | Server + client certificate signed by `ca.crt` | `--tls-cert` |
| `tls.key` | Private key for `tls.crt` | `--tls-key` |
| `fake-client.crt` | Self-signed cert from an **untrusted** CA | Testing: verifies the server rejects unknown CAs |
| `fake-client.key` | Private key for `fake-client.crt` | Testing only |

> **Dev only:** The generated certs have a 10-year validity and 4096-bit RSA
> keys. They are intended for local development and testing only -- do not use
> them in production.

---

## Mapping flags to certificate files

**Channel A -- spyre-health-checker server** (same flags also drive Channel B):

```bash
./spyre-health-checker \
  --tls-cert=certs/tls.crt  \   # server cert (Channel A) AND client cert (Channel B)
  --tls-key=certs/tls.key   \   # private key for both roles
  --tls-ca=certs/ca.crt         # CA used to verify clients (A) and the sidecar server (B)
```

---

## Production certificate guidance

For production deployments replace the self-signed certs with certificates
issued by your organisation's CA or a certificate manager (e.g. cert-manager,
Vault PKI). The certificates must satisfy:

- `O=` (Organisation) field is **non-empty** (required by the post-handshake
  check in `authorizeClientCert`).
- Extended key usage includes both `serverAuth` and `clientAuth`.
- The CA certificate used to verify clients matches the CA that signed the
  client certificates.

Store certificates in Kubernetes Secrets and mount them as volumes. Rotate
certificates before expiry by updating the Secret -- the process must be
restarted to pick up new cert files (there is no hot-reload).

---

## Troubleshooting TLS

### Step 1 -- identify the failure

| Symptom | Cause |
|---|---|
| `spyre-health-checker` fails to start with `failed to load TLS credentials` | Channel A -- server cert/key files missing or unreadable |
| Clients connecting to `spyre-health-checker` get `Unavailable` immediately | Channel A -- TLS handshake failure (wrong CA, expired cert, no client cert) |
| Clients get `Unauthenticated` after connecting | Channel A -- post-handshake check: client cert has empty `Organisation` field |

### Step 2 -- verify certificates with openssl

```bash
# Inspect a certificate (check CN, O=, validity, SANs, extended key usage)
openssl x509 -in certs/tls.crt -noout -text | grep -A5 "Subject:\|Validity\|Extended Key\|Subject Alt"

# Verify a cert was signed by a given CA
openssl verify -CAfile certs/ca.crt certs/tls.crt

# Test the Channel A handshake against the running server socket
openssl s_client -unix /usr/local/etc/device-plugins/health/checker.sock \
  -cert certs/tls.crt -key certs/tls.key -CAfile certs/ca.crt
```

### Step 3 -- test rejection paths with fake-client.crt

```bash
# Should fail with "certificate signed by unknown authority" or similar
openssl s_client -unix /usr/local/etc/device-plugins/health/checker.sock \
  -cert certs/fake-client.crt -key certs/fake-client.key -CAfile certs/ca.crt
```

### Common errors reference

| Channel | Error message | Cause | Fix |
|---|---|---|---|
| A | `failed to load TLS credentials` | cert or key file missing/unreadable at startup | Check file paths and permissions; confirm files exist |
| A | `certificate signed by unknown authority` | Client CA doesn't trust the server cert | Ensure `--tls-ca` points to the CA that signed the client cert |
| A | `tls: certificate required` | Server requires client cert but none was presented | Supply `--tls-cert` and `--tls-key` on the client |
| A | `rpc error: code = Unauthenticated` | Client cert passed TLS handshake but has empty `Organisation` field | Add `O=<value>` to the client certificate's subject |
| A | `rpc error: code = Unavailable` | TLS handshake failed before any RPC was attempted | Check cert validity, CA trust chain, and minimum TLS version (1.2 required) |
| B | `cardhealth: load client cert/key: ...` | `--tls-cert` / `--tls-key` files missing or unreadable | Confirm the files exist and are readable by the health-checker process |
| B | `cardhealth: read CA cert: ...` | `--tls-ca` file missing or unreadable | Confirm the CA file exists and is readable |
| B | `cardhealth: no valid CA certificate found` | CA file exists but contains no parseable PEM certificate | Verify the file with `openssl x509 -in certs/ca.crt -noout -text` |
| B | `rpc error: code = Unauthenticated` (from sidecar) | Client cert has empty `Organisation` field | Add `O=<value>` to the client certificate's subject |
| B | `rpc error: code = Unavailable` (from sidecar) | mTLS handshake failed -- wrong CA, expired cert, or no client cert | Ensure the same CA signs both the sidecar server cert and this client cert |
