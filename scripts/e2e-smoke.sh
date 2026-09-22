#!/usr/bin/env bash
# End-to-end smoke: panel + 2 agents (in-process sing-box, Panel-issued mTLS) via HTTP API.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

AGENT_BIN="${ROOT}/bin/ladder-agent"
PANEL_BIN="${ROOT}/bin/panel"
AGENT_LISTEN="127.0.0.1:50051"
AGENT2_LISTEN="127.0.0.1:50052"
PANEL_LISTEN="127.0.0.1:18080"
PANEL_URL="http://${PANEL_LISTEN}"
ADMIN_PASS="${ADMIN_PASS:-admin}"

TMPDIR="$(mktemp -d "${TMPDIR:-/tmp}/ladder-e2e.XXXXXX")"
COOKIE_JAR="${TMPDIR}/cookies.txt"
PANEL_DB="${TMPDIR}/panel.db"
AGENT_LOG="${TMPDIR}/agent.log"
AGENT2_LOG="${TMPDIR}/agent2.log"
PANEL_LOG="${TMPDIR}/panel.log"

AGENT_PID=""
AGENT2_PID=""
PANEL_PID=""

cleanup() {
  local code=$?
  set +e
  for pid in "${PANEL_PID}" "${AGENT_PID}" "${AGENT2_PID}"; do
    if [[ -n "${pid}" ]] && kill -0 "${pid}" 2>/dev/null; then
      kill "${pid}" 2>/dev/null || true
      wait "${pid}" 2>/dev/null || true
    fi
  done
  rm -rf "${TMPDIR}"
  exit "${code}"
}
trap cleanup EXIT INT TERM

json_get() {
  # json_get <json-string> <dot-separated-key-path> [default]
  # Key path is passed via sys.argv (never interpolated into Python source).
  # Walks dict keys and list indexes; missing/None value prints the default
  # when given, otherwise exits 1.
  # Example: json_get "$body" 'id' ; json_get "$body" 'status' ''
  local raw="$1"
  local path="$2"
  local has_default="0"
  local default=""
  if [[ $# -ge 3 ]]; then
    has_default="1"
    default="$3"
  fi
  python3 -c '
import json, sys
raw, path, has_default, default = sys.argv[1:5]
cur = json.loads(raw)
found = True
for key in path.split("."):
    if isinstance(cur, dict) and key in cur:
        cur = cur[key]
    elif isinstance(cur, list) and key.isdigit() and int(key) < len(cur):
        cur = cur[int(key)]
    else:
        found = False
        break
if not found or cur is None:
    if has_default == "1":
        print(default)
        sys.exit(0)
    sys.exit(1)
print(cur)
' "${raw}" "${path}" "${has_default}" "${default}"
}

wait_http() {
  local url="$1"
  local attempts="${2:-40}"
  local i
  for ((i = 1; i <= attempts; i++)); do
    if curl -sf -o /dev/null --max-time 1 "${url}" 2>/dev/null; then
      return 0
    fi
    # login endpoint returns 405 on GET; any TCP response from panel is fine.
    # curl prints 000 on connection failure — accept only real HTTP statuses.
    if curl -s -o /dev/null --max-time 1 -w '%{http_code}' "${url}" 2>/dev/null | grep -qE '^[1-5][0-9]{2}$'; then
      return 0
    fi
    sleep 0.25
  done
  echo "错误：等待 ${url} 超时" >&2
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
  echo "错误：等待 ${host}:${port} 超时" >&2
  return 1
}

dump_logs() {
  echo "--- agent log ---" >&2
  cat "${AGENT_LOG}" >&2 || true
  echo "--- agent2 log ---" >&2
  cat "${AGENT2_LOG}" >&2 || true
  echo "--- panel log ---" >&2
  cat "${PANEL_LOG}" >&2 || true
}

# enroll_agent <name> <grpc-port> <workdir>
# 走完整 Panel PKI 注册：bootstrap 建节点取一次性注册令牌 → 本地生成私钥/CSR →
# Panel CA 签发证书。证书/私钥/CA 落盘到 workdir；stdout 只打印 "<node_id> <control_token>"。
enroll_agent() {
  local name="$1"
  local port="$2"
  local dir="$3"
  mkdir -p "${dir}"

  local body node_id install_cmd enroll_token
  body="$(curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"${name}\",\"address\":\"127.0.0.1\",\"grpc_port\":${port},\"labels\":[\"smoke\",\"fleet\"]}" \
    "${PANEL_URL}/api/v1/nodes/bootstrap")" || {
    echo "错误：bootstrap 节点 ${name} 失败" >&2
    return 1
  }
  node_id="$(json_get "${body}" 'node.id')"
  install_cmd="$(json_get "${body}" 'install_command')"
  enroll_token="$(INSTALL_CMD="${install_cmd}" python3 - <<'PY'
import os, re
m = re.search(r"LADDER_ENROLL_TOKEN='([^']*)'", os.environ["INSTALL_CMD"])
print(m.group(1) if m else "")
PY
)"
  if [[ -z "${enroll_token}" ]]; then
    echo "错误：无法从安装命令解析注册令牌" >&2
    return 1
  fi

  openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "${dir}/server.key" 2>/dev/null
  {
    echo "[req]"
    echo "prompt=no"
    echo "distinguished_name=dn"
    echo "req_extensions=req_ext"
    echo "[dn]"
    echo "CN=${node_id}"
    echo "[req_ext]"
    echo "subjectAltName=DNS:localhost,IP:127.0.0.1"
  } >"${dir}/csr.conf"
  openssl req -new -key "${dir}/server.key" -config "${dir}/csr.conf" -out "${dir}/server.csr" 2>/dev/null

  PANEL_NODE_ID="${node_id}" PANEL_CSR="${dir}/server.csr" PANEL_PORT="${port}" python3 - <<'PY' >"${dir}/payload.json"
import json, os
with open(os.environ["PANEL_CSR"], "r", encoding="utf-8") as f:
    csr = f.read()
print(json.dumps({
    "node_id": os.environ["PANEL_NODE_ID"],
    "csr_pem": csr,
    "address": "127.0.0.1",
    "grpc_port": int(os.environ["PANEL_PORT"]),
}))
PY

  local code
  code="$(curl -sS --max-time 15 -o "${dir}/issue.json" -w '%{http_code}' \
    -X POST "${PANEL_URL}/api/v1/pki/agent-certificates" \
    -H "Authorization: Bearer ${enroll_token}" \
    -H "Content-Type: application/json" \
    --data-binary "@${dir}/payload.json")"
  if [[ "${code}" != "201" ]]; then
    echo "错误：Agent 证书签发失败 (HTTP ${code}): $(head -c 300 "${dir}/issue.json")" >&2
    return 1
  fi

  local ctrl_token
  ctrl_token="$(ISSUE="${dir}/issue.json" CERT="${dir}/server.crt" CA="${dir}/ca.crt" python3 - <<'PY'
import json, os
with open(os.environ["ISSUE"], "r", encoding="utf-8") as f:
    data = json.load(f)
for key, path in (("cert_pem", os.environ["CERT"]), ("ca_bundle_pem", os.environ["CA"])):
    value = data.get(key, "")
    if not value:
        raise SystemExit("签发响应缺少字段 " + key)
    with open(path, "w", encoding="utf-8") as f:
        f.write(value)
token = data.get("control_token", "")
if not token:
    raise SystemExit("签发响应缺少 control_token")
print(token)
PY
)"
  printf '%s %s\n' "${node_id}" "${ctrl_token}"
}

# start_agent <listen> <workdir> <token> <node_id> <log>；PID 写入全局 AGENT_STARTED_PID
start_agent() {
  local listen="$1"
  local dir="$2"
  local token="$3"
  local node_id="$4"
  local log="$5"
  "${AGENT_BIN}" \
    -listen "${listen}" \
    -token "${token}" \
    -data-dir "${dir}/data" \
    -tls-cert "${dir}/server.crt" \
    -tls-key "${dir}/server.key" \
    -tls-client-ca "${dir}/ca.crt" \
    -panel-url "${PANEL_URL}" \
    -node-id "${node_id}" \
    >"${log}" 2>&1 &
  AGENT_STARTED_PID=$!
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

echo "==> start panel on ${PANEL_LISTEN} (temp db)"
# Panel 首次初始化时读取 LADDER_ADMIN_PASSWORD 作为初始管理员密码；
# 不设置则会生成随机密码，e2e 将无法登录。
LADDER_ADMIN_PASSWORD="${ADMIN_PASS}" \
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
  echo "错误：GET / 返回 HTTP ${SPA_CODE}（内嵌前端应返回 200）" >&2
  exit 1
fi
if ! grep -qiE '<!doctype html|<html' "${TMPDIR}/index.html"; then
  echo "错误：GET / 返回内容不是 HTML" >&2
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

echo "==> set Public Base URL（bootstrap 前置条件）"
curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -X PUT \
  -H 'Content-Type: application/json' \
  -d "{\"public_base_url\":\"${PANEL_URL}\"}" \
  "${PANEL_URL}/api/v1/settings" >/dev/null

echo "==> enroll node1 via Panel PKI (${AGENT_LISTEN})"
ENROLL_OUT="$(enroll_agent "smoke-node" 50051 "${TMPDIR}/agent1")"
read -r NODE_ID NODE1_TOKEN <<<"${ENROLL_OUT}"
echo "node id=${NODE_ID}"
start_agent "${AGENT_LISTEN}" "${TMPDIR}/agent1" "${NODE1_TOKEN}" "${NODE_ID}" "${AGENT_LOG}"
AGENT_PID="${AGENT_STARTED_PID}"
wait_tcp 127.0.0.1 50051

echo "==> enroll node2 via Panel PKI (${AGENT2_LISTEN}, batch target)"
ENROLL_OUT="$(enroll_agent "smoke-node-2" 50052 "${TMPDIR}/agent2")"
read -r NODE2_ID NODE2_TOKEN <<<"${ENROLL_OUT}"
echo "node2 id=${NODE2_ID}"
start_agent "${AGENT2_LISTEN}" "${TMPDIR}/agent2" "${NODE2_TOKEN}" "${NODE2_ID}" "${AGENT2_LOG}"
AGENT2_PID="${AGENT_STARTED_PID}"
wait_tcp 127.0.0.1 50052

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
  echo "错误：协议模板应不少于 4 个，实际为 ${TMPL_COUNT}：${TMPL_BODY}" >&2
  exit 1
fi
echo "templates: count=${TMPL_COUNT}"

echo "==> create node with wrong agent token (no PKI enrollment)"
BAD_NODE_BODY="$(curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"bad-token-node\",\"address\":\"127.0.0.1\",\"grpc_port\":50051,\"token\":\"wrong-token\",\"labels\":[\"bad\"]}" \
  "${PANEL_URL}/api/v1/nodes")"
BAD_NODE_ID="$(json_get "${BAD_NODE_BODY}" 'id')"
echo "bad-token node id=${BAD_NODE_ID}"

echo "==> create shadowsocks inbound (node1, port 18443)"
IN_BODY="$(curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -H 'Content-Type: application/json' \
  -d '{"name":"ss-smoke","protocol":"shadowsocks","enabled":true,"params":{"listen":"0.0.0.0","port":18443,"method":"aes-128-gcm","password":"testpass"}}' \
  "${PANEL_URL}/api/v1/inbounds")"
INBOUND_ID="$(json_get "${IN_BODY}" 'id')"
echo "inbound id=${INBOUND_ID}"

echo "==> create second shadowsocks inbound (node2, port 18444)"
IN2_BODY="$(curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -H 'Content-Type: application/json' \
  -d '{"name":"ss-smoke-2","protocol":"shadowsocks","enabled":true,"params":{"listen":"0.0.0.0","port":18444,"method":"aes-128-gcm","password":"testpass"}}' \
  "${PANEL_URL}/api/v1/inbounds")"
INBOUND2_ID="$(json_get "${IN2_BODY}" 'id')"
echo "inbound2 id=${INBOUND2_ID}"

echo "==> attach inbounds (18443→node1, 18444→node2；同机两 agent 需不同端口)"
curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -X PUT \
  -H 'Content-Type: application/json' \
  -d "{\"inbound_ids\":[\"${INBOUND_ID}\"]}" \
  "${PANEL_URL}/api/v1/nodes/${NODE_ID}/inbounds" >/dev/null
curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -X PUT \
  -H 'Content-Type: application/json' \
  -d "{\"inbound_ids\":[\"${INBOUND2_ID}\"]}" \
  "${PANEL_URL}/api/v1/nodes/${NODE2_ID}/inbounds" >/dev/null

echo "==> apply config (single node)"
APPLY_BODY="$(curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -X POST \
  "${PANEL_URL}/api/v1/nodes/${NODE_ID}/apply")"
echo "apply: ${APPLY_BODY}"

TASK_STATUS="$(json_get "${APPLY_BODY}" 'status' '')"
if [[ "${TASK_STATUS}" != "success" ]]; then
  echo "错误：配置下发任务状态为 ${TASK_STATUS}（应为 success）" >&2
  echo "${APPLY_BODY}" >&2
  dump_logs
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
  echo "错误：配置下发结果并非全部成功" >&2
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
  echo "错误：按标签批量下发失败" >&2
  echo "${BATCH_BODY}" >&2
  dump_logs
  exit 1
fi

echo "==> 验证未完成 PKI 注册/令牌错误的节点必须探测失败"
BAD_PROBE_CODE="$(curl -s -o "${TMPDIR}/bad-probe.json" -w '%{http_code}' \
  -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -X POST \
  "${PANEL_URL}/api/v1/nodes/${BAD_NODE_ID}/probe")"
if [[ "${BAD_PROBE_CODE}" == "200" ]]; then
  echo "错误：使用错误 Agent 令牌探测时意外成功" >&2
  cat "${TMPDIR}/bad-probe.json" >&2 || true
  exit 1
fi
echo "wrong-token probe: HTTP ${BAD_PROBE_CODE} (expected non-200)"

echo "==> HTTP agent report + config-sync (reuse Panel HTTP + Bearer)"
UPLINK_BODY="$(curl -sf -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  -H 'Content-Type: application/json' \
  -d '{"name":"uplink-smoke","control_mode":"uplink","token":"uplink-e2e-token"}' \
  "${PANEL_URL}/api/v1/nodes")"
UPLINK_ID="$(json_get "${UPLINK_BODY}" 'id')"
UPLINK_MODE="$(json_get "${UPLINK_BODY}" 'control_mode')"
if [[ "${UPLINK_MODE}" != "uplink" ]]; then
  echo "错误：uplink 节点 control_mode=${UPLINK_MODE}" >&2
  echo "${UPLINK_BODY}" >&2
  exit 1
fi
NOW_UNIX="$(date +%s)"
REPORT_BODY="$(curl -sf \
  -H "Authorization: Bearer uplink-e2e-token" \
  -H 'Content-Type: application/json' \
  -d "{\"node_id\":\"${UPLINK_ID}\",\"collected_at_unix\":${NOW_UNIX},\"runtime_state\":\"running\",\"config_hash\":\"e2e\",\"capabilities\":[\"uplink-v1\"],\"connections\":1,\"cpu_percent\":1}" \
  "${PANEL_URL}/api/v1/agent/report")"
if [[ "$(json_get "${REPORT_BODY}" 'ok')" != "True" && "$(json_get "${REPORT_BODY}" 'ok')" != "true" ]]; then
  echo "错误：agent report 失败：${REPORT_BODY}" >&2
  exit 1
fi
WRONG_REPORT_CODE="$(curl -s -o /dev/null -w '%{http_code}' \
  -H "Authorization: Bearer wrong-token" \
  -H 'Content-Type: application/json' \
  -d "{\"node_id\":\"${UPLINK_ID}\",\"collected_at_unix\":${NOW_UNIX}}" \
  "${PANEL_URL}/api/v1/agent/report")"
if [[ "${WRONG_REPORT_CODE}" != "401" ]]; then
  echo "错误：错误令牌上报应返回 401，实际 ${WRONG_REPORT_CODE}" >&2
  exit 1
fi
SYNC_BODY="$(curl -sf \
  -H "Authorization: Bearer uplink-e2e-token" \
  -H 'Content-Type: application/json' \
  -d "{\"node_id\":\"${UPLINK_ID}\",\"applied_config_hash\":\"\",\"applied_frps_hash\":\"\"}" \
  "${PANEL_URL}/api/v1/agent/config-sync")"
if [[ "$(json_get "${SYNC_BODY}" 'changed')" != "True" && "$(json_get "${SYNC_BODY}" 'changed')" != "true" ]]; then
  echo "错误：首次 config-sync 应 changed=true：${SYNC_BODY}" >&2
  exit 1
fi
SYNC_HASH="$(json_get "${SYNC_BODY}" 'config_hash')"
HEAD_HASH="$(curl -sI \
  -H "Authorization: Bearer uplink-e2e-token" \
  "${PANEL_URL}/api/v1/agent/config-sync?node_id=${UPLINK_ID}" \
  | tr -d '\r' | awk -F': ' 'tolower($1)=="x-config-hash"{print $2; exit}')"
if [[ "${HEAD_HASH}" != "${SYNC_HASH}" ]]; then
  echo "错误：HEAD X-Config-Hash=${HEAD_HASH} 与 POST config_hash=${SYNC_HASH} 不一致" >&2
  exit 1
fi
GET_META="$(curl -sf \
  -H "Authorization: Bearer uplink-e2e-token" \
  "${PANEL_URL}/api/v1/agent/config-sync?node_id=${UPLINK_ID}")"
if [[ "$(json_get "${GET_META}" 'config_hash')" != "${SYNC_HASH}" ]]; then
  echo "错误：GET config-sync 哈希不一致：${GET_META}" >&2
  exit 1
fi
SYNC2_BODY="$(curl -sf \
  -H "Authorization: Bearer uplink-e2e-token" \
  -H 'Content-Type: application/json' \
  -d "{\"node_id\":\"${UPLINK_ID}\",\"applied_config_hash\":\"${SYNC_HASH}\",\"applied_frps_hash\":\"\"}" \
  "${PANEL_URL}/api/v1/agent/config-sync")"
if [[ "$(json_get "${SYNC2_BODY}" 'changed')" != "False" && "$(json_get "${SYNC2_BODY}" 'changed')" != "false" ]]; then
  echo "错误：hash 未变时 config-sync 应 changed=false：${SYNC2_BODY}" >&2
  exit 1
fi
echo "uplink HTTP: report ok, wrong token 401, HEAD/GET hash=${SYNC_HASH}, config-sync ok"

echo "==> e2e smoke OK (apply=${TASK_STATUS}, templates=${TMPL_COUNT}, PKI enroll ×2, batch labels OK, wrong token rejected, HTTP uplink OK)"
