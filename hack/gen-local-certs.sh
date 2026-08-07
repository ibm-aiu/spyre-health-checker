#!/usr/bin/env bash
# +-------------------------------------------------------------------+
# | (C) Copyright IBM Corp. 2025, 2026                                |
# | SPDX-License-Identifier: Apache-2.0                               |
# +-------------------------------------------------------------------+
#
# gen-local-certs.sh — generate a self-signed CA and TLS certificate/key
# pairs for local development and mTLS testing.
#
# Output directory defaults to ./certs (override with CERT_DIR env var).
# Files produced:
#   ca.crt            — self-signed CA certificate
#   ca.key            — CA private key
#   tls.crt           — server/client certificate (signed by ca.crt)
#   tls.key           — server/client private key
#   fake-client.crt   — self-signed cert NOT trusted by the server (untrusted-CA testing)
#   fake-client.key   — fake-client private key
#
# Usage:
#   bash hack/gen-local-certs.sh
#   CERT_DIR=/tmp/my-certs bash hack/gen-local-certs.sh

set -euo pipefail

CERT_DIR="${CERT_DIR:-$(pwd)/certs}"
mkdir -p "${CERT_DIR}"

DAYS=3650 # 10-year validity — dev use only
KEYBITS=4096

echo "==> Generating certificates in: ${CERT_DIR}"

# ---------------------------------------------------------------------------
# 1. Self-signed CA
# ---------------------------------------------------------------------------
echo "--> CA key & certificate"
openssl genrsa -out "${CERT_DIR}/ca.key" ${KEYBITS} 2>/dev/null

CA_CNF="${CERT_DIR}/ca.cnf"
cat >"${CA_CNF}" <<'EOF'
[req]
distinguished_name = req_distinguished_name
x509_extensions    = v3_ca
prompt             = no

[req_distinguished_name]
CN = spyre-local-ca
O  = SpyreDevCA

[v3_ca]
subjectKeyIdentifier   = hash
authorityKeyIdentifier = keyid:always,issuer
basicConstraints       = critical,CA:TRUE
keyUsage               = critical,keyCertSign,cRLSign
EOF

openssl req -new -x509 \
	-config "${CA_CNF}" \
	-key "${CERT_DIR}/ca.key" \
	-out "${CERT_DIR}/ca.crt" \
	-days ${DAYS}

# ---------------------------------------------------------------------------
# 2. Single TLS certificate — used by both server and client
# ---------------------------------------------------------------------------
echo "--> TLS key & certificate (shared server/client)"
openssl genrsa -out "${CERT_DIR}/tls.key" ${KEYBITS} 2>/dev/null

TLS_CNF="${CERT_DIR}/tls.cnf"
cat >"${TLS_CNF}" <<'EOF'
[req]
distinguished_name = req_distinguished_name
prompt             = no

[req_distinguished_name]
CN = spyre-components
O  = SpyreDev

[tls_ext]
subjectKeyIdentifier   = hash
authorityKeyIdentifier = keyid:always,issuer
basicConstraints       = CA:FALSE
keyUsage               = critical,digitalSignature,keyEncipherment
extendedKeyUsage       = serverAuth,clientAuth
subjectAltName         = DNS:spyre-components,DNS:localhost,IP:127.0.0.1
EOF

openssl req -new \
	-config "${TLS_CNF}" \
	-key "${CERT_DIR}/tls.key" \
	-out "${CERT_DIR}/tls.csr"

openssl x509 -req \
	-in "${CERT_DIR}/tls.csr" \
	-CA "${CERT_DIR}/ca.crt" \
	-CAkey "${CERT_DIR}/ca.key" \
	-CAcreateserial \
	-extfile "${TLS_CNF}" \
	-extensions tls_ext \
	-out "${CERT_DIR}/tls.crt" \
	-days ${DAYS}

rm -f "${CERT_DIR}/tls.csr"

# ---------------------------------------------------------------------------
# 3. Fake client certificate — self-signed by its own key (untrusted CA)
#    Use this to test that the server correctly rejects clients whose cert
#    is not signed by the trusted CA.
# ---------------------------------------------------------------------------
echo "--> Fake client key & self-signed certificate (untrusted CA)"
openssl genrsa -out "${CERT_DIR}/fake-client.key" ${KEYBITS} 2>/dev/null

FAKE_CNF="${CERT_DIR}/fake-client.cnf"
cat >"${FAKE_CNF}" <<'EOF'
[req]
distinguished_name = req_distinguished_name
x509_extensions    = v3_fake
prompt             = no

[req_distinguished_name]
CN = fake-client
O  = FakeOrg

[v3_fake]
subjectKeyIdentifier   = hash
basicConstraints       = CA:FALSE
keyUsage               = critical,digitalSignature,keyEncipherment
extendedKeyUsage       = clientAuth
subjectAltName         = DNS:fake-client
EOF

openssl req -new -x509 \
	-config "${FAKE_CNF}" \
	-key "${CERT_DIR}/fake-client.key" \
	-out "${CERT_DIR}/fake-client.crt" \
	-days ${DAYS}

# Clean up intermediate files
rm -f "${CA_CNF}" "${TLS_CNF}" "${FAKE_CNF}" "${CERT_DIR}/ca.srl"
