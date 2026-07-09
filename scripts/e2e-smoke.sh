#!/usr/bin/env bash
# End-to-end smoke: panel + agent (in-process sing-box) via HTTP API.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

AGENT_BIN="${ROOT}/bin/ladder-agent"
PANEL_BIN="${ROOT}/bin/panel"
AGENT_LISTEN="127.0.0.1:50051"
AGENT_TOKEN="test"
PANEL_LISTEN="127.0.0.1:18080"
PANEL_URL="http://${PANEL_LISTEN}"
ADMIN_PASS="admin"

TMPDIR="$(mktemp -d "${TMPDIR:-/tmp}/ladder-e2e.XXXXXX")"
COOKIE_JAR="${TMPDIR}/cookies.txt"
PANEL_DB="${TMPDIR}/panel.db"
AGENT_LOG="${TMPDIR}/agent.log"
PANEL_LOG="${TMPDIR}/panel.log"

AGENT_PID=""
PANEL_PID=""

cleanup() {
  local code=$?
  set +e
  if [[ -n "${PANEL_PID}" ]] && kill -0 "${PANEL_PID}" 2>/dev/null; then
    kill "${PANEL_PID}" 2>/dev/null || true
    wait "${PANEL_PID}" 2>/dev/null || true
  fi
  if [[ -n "${AGENT_PID}" ]] && kill -0 "${AGENT_PID}" 2>/dev/null; then
    kill "${AGENT_PID}" 2>/dev/null || true
    wait "${AGENT_PID}" 2>/dev/null || true
  fi
  rm -rf "${TMPDIR}"
  exit "${code}"
}
trap cleanup EXIT INT TERM

json_get() {
  # json_get <json-string> <python-expr-on-obj>
  # Example: json_get "$body" 'obj["id"]'
  local raw="$1"
  local expr="$2"
  python3 -c "
import json,sys
obj=json.loads(sys.argv[1])
v=${expr}
if v is None:
    sys.exit(1)
print(v)
" "${raw}"
}

wait_http() {
  local url="$1"
  local attempts="${2:-40}"
  local i
  for ((i = 1; i <= attempts; i++)); do
    if curl -sf -o /dev/null --max-time 1 "${url}" 2>/dev/null; then
      return 0
    fi
    # login endpoint returns 405 on GET; any TCP response from panel is fine
    if curl -s -o /dev/null --max-time 1 -w '%{http_code}' "${url}" 2>/dev/null | grep -qE '^[0-9]{3}$'; then
      return 0
    fi
    sleep 0.25
  done
  echo "ERROR: timed out waiting for ${url}" >&2
  return 1
}

wait_tcp() {
  local host="$1"
  local port="$2"
  local attempts="${3:-40}"
  local i
  for ((i = 1; i <= attempts; i++)); do
    if (echo >/dev/tcp/"${host}"/"${port}") >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  echo "ERROR: timed out waiting for ${host}:${port}" >&2
  return 1
}

echo "==> ensure binaries"
if [[ ! -x "${AGENT_BIN}" ]]; then
  echo "building agent..."
  make agent
fi
if [[ ! -x "${PANEL_BIN}" ]]; then
  echo "building panel..."
  make panel
fi

echo "==> start agent (sing-box) on ${AGENT_LISTEN}"
"${AGENT_BIN}" \
  -listen "${AGENT_LISTEN}" \
  -token "${AGENT_TOKEN}" \
  -data-dir "${TMPDIR}/agent-data" \
  >"${AGENT_LOG}" 2>&1 &
AGENT_PID=$!
wait_tcp 127.0.0.1 50051

echo "==> start panel on ${PANEL_LISTEN} (temp db)"
"${PANEL_BIN}" \
  -listen "${PANEL_LISTEN}" \
  -db "${PANEL_DB}" \
  -session-secret "e2e-smoke-session-secret-32bytes!!" \
  >"${PANEL_LOG}" 2>&1 &
PANEL_PID=$!
wait_http "${PANEL_URL}/api/v1/auth/login"

echo "==> embedded SPA (single binary web)"
SPA_CODE="$(curl -s -o "${TMPDIR}/index.html" -w '%{http_code}' -b "${COOKIE_JAR}" "${PANEL_URL}/")"
if [[ "${SPA_CODE}" != "200" ]]; then
  echo "ERROR: GET / returned HTTP ${SPA_CODE} (want 200 for embedded SPA)" >&2
  exit 1
fi
if ! grep -qiE '<!doctype html|<html' "${TMPDIR}/index.html"; then
  echo "ERROR: GET / did not look like HTML" >&2
  head -c 200 "${TMPDIR}/index.html" >&2 || true
  exit 1
fi
echo "SPA: HTTP ${SPA_CODE}"

echo "==> login (password=${ADMIN_PASS})"
LOGIN_BODY="$(curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -H 'Content-Type: application/json' \
  -d "{\"password\":\"${ADMIN_PASS}\"}" \
  "${PANEL_URL}/api/v1/auth/login")"
echo "login: ${LOGIN_BODY}"

echo "==> GET /api/v1/templates (expect 4)"
TMPL_BODY="$(curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  "${PANEL_URL}/api/v1/templates")"
TMPL_COUNT="$(python3 -c '
import json,sys
obj=json.loads(sys.argv[1])
if isinstance(obj, list):
    print(len(obj))
elif isinstance(obj, dict) and "_array" in obj:
    print(len(obj["_array"]))
else:
    print(0)
' "${TMPL_BODY}")"
if [[ "${TMPL_COUNT}" -lt 4 ]]; then
  echo "ERROR: expected >=4 templates, got ${TMPL_COUNT}: ${TMPL_BODY}" >&2
  exit 1
fi
echo "templates: count=${TMPL_COUNT}"

echo "==> create node 127.0.0.1:50051 token=${AGENT_TOKEN}"
NODE_BODY="$(curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"smoke-node\",\"address\":\"127.0.0.1\",\"grpc_port\":50051,\"token\":\"${AGENT_TOKEN}\",\"labels\":[\"smoke\",\"fleet\"]}" \
  "${PANEL_URL}/api/v1/nodes")"
NODE_ID="$(json_get "${NODE_BODY}" 'obj["id"]')"
echo "node id=${NODE_ID}"

echo "==> create second node same labels (batch target)"
NODE2_BODY="$(curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"smoke-node-2\",\"address\":\"127.0.0.1\",\"grpc_port\":50051,\"token\":\"${AGENT_TOKEN}\",\"labels\":[\"smoke\",\"fleet\"]}" \
  "${PANEL_URL}/api/v1/nodes")"
NODE2_ID="$(json_get "${NODE2_BODY}" 'obj["id"]')"
echo "node2 id=${NODE2_ID}"

echo "==> create node with wrong agent token"
BAD_NODE_BODY="$(curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"bad-token-node\",\"address\":\"127.0.0.1\",\"grpc_port\":50051,\"token\":\"wrong-token\",\"labels\":[\"bad\"]}" \
  "${PANEL_URL}/api/v1/nodes")"
BAD_NODE_ID="$(json_get "${BAD_NODE_BODY}" 'obj["id"]')"
echo "bad-token node id=${BAD_NODE_ID}"

echo "==> create shadowsocks inbound"
IN_BODY="$(curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -H 'Content-Type: application/json' \
  -d '{"name":"ss-smoke","protocol":"shadowsocks","enabled":true,"params":{"listen":"0.0.0.0","port":18443,"method":"aes-128-gcm","password":"testpass"}}' \
  "${PANEL_URL}/api/v1/inbounds")"
INBOUND_ID="$(json_get "${IN_BODY}" 'obj["id"]')"
echo "inbound id=${INBOUND_ID}"

echo "==> attach inbound to both fleet nodes"
for nid in "${NODE_ID}" "${NODE2_ID}"; do
  curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
    -X PUT \
    -H 'Content-Type: application/json' \
    -d "{\"inbound_ids\":[\"${INBOUND_ID}\"]}" \
    "${PANEL_URL}/api/v1/nodes/${nid}/inbounds" >/dev/null
done

echo "==> apply config (single node)"
APPLY_BODY="$(curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -X POST \
  "${PANEL_URL}/api/v1/nodes/${NODE_ID}/apply")"
echo "apply: ${APPLY_BODY}"

TASK_STATUS="$(json_get "${APPLY_BODY}" 'obj.get("status","")')"
if [[ "${TASK_STATUS}" != "success" ]]; then
  echo "ERROR: apply task status=${TASK_STATUS} (want success)" >&2
  echo "${APPLY_BODY}" >&2
  echo "--- agent log ---" >&2
  cat "${AGENT_LOG}" >&2 || true
  echo "--- panel log ---" >&2
  cat "${PANEL_LOG}" >&2 || true
  exit 1
fi

# results[].ok should also be true when present
if ! python3 -c '
import json,sys
obj=json.loads(sys.argv[1])
if obj.get("status")!="success":
    sys.exit(1)
for r in obj.get("results") or []:
    if not r.get("ok"):
        sys.exit(2)
' "${APPLY_BODY}"; then
  echo "ERROR: apply results not all ok" >&2
  echo "${APPLY_BODY}" >&2
  exit 1
fi

echo "==> batch apply by labels=[fleet] (expect 2 nodes)"
BATCH_BODY="$(curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -X POST \
  -H 'Content-Type: application/json' \
  -d '{"node_ids":[],"labels":["fleet"]}' \
  "${PANEL_URL}/api/v1/batch/apply")"
echo "batch apply: ${BATCH_BODY}"
if ! python3 -c '
import json,sys
obj=json.loads(sys.argv[1])
if obj.get("status")!="success":
    sys.exit(1)
ids=obj.get("node_ids") or []
if len(ids)!=2:
    sys.exit(2)
for r in obj.get("results") or []:
    if not r.get("ok"):
        sys.exit(3)
' "${BATCH_BODY}"; then
  echo "ERROR: batch apply by labels failed" >&2
  echo "${BATCH_BODY}" >&2
  echo "--- agent log ---" >&2
  cat "${AGENT_LOG}" >&2 || true
  echo "--- panel log ---" >&2
  cat "${PANEL_LOG}" >&2 || true
  exit 1
fi

echo "==> wrong agent token must fail probe"
BAD_PROBE_CODE="$(curl -s -o "${TMPDIR}/bad-probe.json" -w '%{http_code}' \
  -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -X POST \
  "${PANEL_URL}/api/v1/nodes/${BAD_NODE_ID}/probe")"
if [[ "${BAD_PROBE_CODE}" == "200" ]]; then
  echo "ERROR: probe with wrong agent token unexpectedly succeeded" >&2
  cat "${TMPDIR}/bad-probe.json" >&2 || true
  exit 1
fi
echo "wrong-token probe: HTTP ${BAD_PROBE_CODE} (expected non-200)"

echo "==> e2e smoke OK (apply=${TASK_STATUS}, templates=${TMPL_COUNT}, batch labels OK, wrong token rejected)"
