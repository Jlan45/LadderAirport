#!/usr/bin/env bash
# Generate self-signed lab TLS material under deploy/dev/ for optional agent TLS.
# Produces a tiny CA, agent leaf cert, and matching private keys (openssl x509).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${ROOT}/deploy/dev"
DAYS="${CERT_DAYS:-825}"
CN="${CERT_CN:-localhost}"

mkdir -p "${OUT}"

CA_KEY="${OUT}/ca.key"
CA_CRT="${OUT}/ca.crt"
AGENT_KEY="${OUT}/agent.key"
AGENT_CSR="${OUT}/agent.csr"
AGENT_CRT="${OUT}/agent.crt"
AGENT_EXT="${OUT}/agent.ext"

echo "Writing lab certs to ${OUT}"

# Development CA
openssl genrsa -out "${CA_KEY}" 2048 2>/dev/null
openssl req -x509 -new -nodes \
  -key "${CA_KEY}" \
  -sha256 \
  -days "${DAYS}" \
  -subj "/CN=LadderAirport Dev CA" \
  -out "${CA_CRT}"

# Agent leaf key + CSR
openssl genrsa -out "${AGENT_KEY}" 2048 2>/dev/null
openssl req -new \
  -key "${AGENT_KEY}" \
  -subj "/CN=${CN}" \
  -out "${AGENT_CSR}"

cat >"${AGENT_EXT}" <<EOF
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:localhost,IP:127.0.0.1
EOF

openssl x509 -req \
  -in "${AGENT_CSR}" \
  -CA "${CA_CRT}" \
  -CAkey "${CA_KEY}" \
  -CAcreateserial \
  -out "${AGENT_CRT}" \
  -days "${DAYS}" \
  -sha256 \
  -extfile "${AGENT_EXT}"

# Drop intermediates not needed at runtime
rm -f "${AGENT_CSR}" "${AGENT_EXT}" "${OUT}/ca.srl"

chmod 600 "${CA_KEY}" "${AGENT_KEY}"
chmod 644 "${CA_CRT}" "${AGENT_CRT}"

echo "Generated:"
echo "  ${CA_CRT}"
echo "  ${CA_KEY}"
echo "  ${AGENT_CRT}"
echo "  ${AGENT_KEY}"
echo
echo "Agent (TLS lab):"
echo "  ./bin/ladder-agent -listen 127.0.0.1:50051 -token test -data-dir /tmp/ladder-agent \\"
echo "    -tls-cert ${AGENT_CRT} -tls-key ${AGENT_KEY}"
echo
echo "Panel node: set ca_cert_pem from ${CA_CRT} (or tls_skip_verify=true for lab only)."
echo "WARNING: lab/self-signed only — do not use these certs in production."
