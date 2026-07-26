#!/usr/bin/env bash
# One-time migration from the removed node-local CA implementation to the
# Panel-managed PKI. On success no legacy CA key, certificate, unit or binary
# backup is retained. A temporary rollback copy exists only while this script
# is running.
set -euo pipefail

REPO="${LADDER_REPO:-Jlan45/LadderAirport}"
API_BASE="${LADDER_GITHUB_API:-https://api.github.com}"
RELEASES_BASE="${LADDER_GITHUB_RELEASES:-https://github.com/${REPO}/releases}"
VERSION="${LADDER_VERSION:-latest}"
SOURCE_AGENT_BINARY="${LADDER_AGENT_BINARY:-}"
PANEL_URL="${LADDER_PANEL:-}"
NODE_ID="${LADDER_NODE_ID:-}"
ENROLL_TOKEN="${LADDER_ENROLL_TOKEN:-}"
REPORT_ADDR="${LADDER_REPORT_ADDRESS:-}"
GRPC_PORT_HINT="${LADDER_GRPC_PORT:-}"
TLS_EXTRA_SANS="${LADDER_TLS_EXTRA_SANS:-}"
ALLOW_HTTP="${LADDER_ALLOW_HTTP:-0}"

INSTALL_BIN="${INSTALL_BIN:-/usr/local/bin/ladder-agent}"
CONF_DIR="${CONF_DIR:-/etc/ladder-agent}"
DATA_DIR="${DATA_DIR:-/var/lib/ladder-agent}"
TLS_DIR="${TLS_DIR:-${CONF_DIR}/tls}"
ENV_FILE="${ENV_FILE:-${CONF_DIR}/agent.env}"
SERVICE_DST="${SERVICE_DST:-/etc/systemd/system/ladder-agent.service}"
SERVICE_NAME="${LADDER_SERVICE_NAME:-ladder-agent.service}"
USER_NAME="${LADDER_USER:-ladder}"
GROUP_NAME="${LADDER_GROUP:-ladder}"

WORK_DIR=""
ROLLBACK_READY=0
SWITCH_STARTED=0
MIGRATION_DONE=0

die() {
  echo "ERROR: $*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "需要命令: $1"
}

env_value() {
  local key="$1"
  grep -E "^${key}=" "${ENV_FILE}" 2>/dev/null | head -1 | cut -d= -f2- || true
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) die "不支持的架构: $(uname -m)（需要 amd64 或 arm64）" ;;
  esac
}

download_agent() {
  local arch asset tag url dest api_json
  arch="$(detect_arch)"
  asset="ladder-agent-linux-${arch}"
  if [[ "${VERSION}" == "latest" ]]; then
    api_json="$(curl -fsSL "${API_BASE}/repos/${REPO}/releases/latest")" \
      || die "无法读取最新 Release"
    tag="$(echo "${api_json}" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
    url="$(echo "${api_json}" | tr ',' '\n' | sed -n "s/.*\"browser_download_url\"[[:space:]]*:[[:space:]]*\"\\([^\"]*${asset}\\)\"/\\1/p" | head -1)"
    [[ -n "${url}" ]] || url="${RELEASES_BASE}/download/${tag}/${asset}"
  else
    tag="${VERSION}"
    url="${RELEASES_BASE}/download/${tag}/${asset}"
  fi
  [[ -n "${tag:-}" && -n "${url:-}" ]] || die "未找到 ${asset} Release"
  dest="${WORK_DIR}/${asset}"
  echo "==> 下载 Agent ${tag}: ${url}"
  curl -fL --retry 3 --retry-delay 1 -o "${dest}" "${url}"
  local sums="${WORK_DIR}/SHA256SUMS.txt"
  if curl -fsSL -o "${sums}" "${RELEASES_BASE}/download/${tag}/SHA256SUMS.txt" 2>/dev/null; then
    if command -v sha256sum >/dev/null 2>&1; then
      (cd "${WORK_DIR}" && grep " ${asset}\$" SHA256SUMS.txt | sha256sum -c -)
    fi
  fi
  chmod 0755 "${dest}"
  "${dest}" -version >/dev/null
  echo "${dest}"
}

resolve_agent_binary() {
  if [[ -n "${SOURCE_AGENT_BINARY}" ]]; then
    [[ -x "${SOURCE_AGENT_BINARY}" ]] || die "LADDER_AGENT_BINARY 不可执行: ${SOURCE_AGENT_BINARY}"
    local dest="${WORK_DIR}/ladder-agent-local"
    install -m 0755 "${SOURCE_AGENT_BINARY}" "${dest}"
    "${dest}" -version >/dev/null
    echo "${dest}"
    return
  fi
  download_agent | tail -1
}

detect_report_address() {
  if [[ -n "${REPORT_ADDR}" ]]; then
    echo "${REPORT_ADDR}"
    return
  fi
  local ip
  for ip in $(hostname -I 2>/dev/null || true); do
    case "${ip}" in
      127.*|::1) ;;
      *) echo "${ip}"; return ;;
    esac
  done
  echo ""
}

build_sans() {
  local -a values=("DNS:localhost" "IP:127.0.0.1" "IP:::1")
  local host ip item existing
  host="$(hostname -f 2>/dev/null || hostname 2>/dev/null || true)"
  [[ -z "${host}" || "${host}" == "localhost" ]] || values+=("DNS:${host}")
  for ip in $(hostname -I 2>/dev/null || true); do
    case "${ip}" in
      127.*|::1) ;;
      *) values+=("IP:${ip}") ;;
    esac
  done
  if [[ -n "${REPORT_ADDR}" ]]; then
    local report_san="${REPORT_ADDR%%%*}"
    if [[ "${report_san}" == *:* || "${report_san}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      values+=("IP:${report_san}")
    else
      values+=("DNS:${report_san}")
    fi
  fi
  if [[ -n "${TLS_EXTRA_SANS}" ]]; then
    local IFS=','
    for item in ${TLS_EXTRA_SANS}; do
      item="${item//[[:space:]]/}"
      [[ -z "${item}" ]] || values+=("${item}")
    done
  fi
  local joined=""
  local -a unique=()
  for item in "${values[@]}"; do
    existing=0
    for host in "${unique[@]+"${unique[@]}"}"; do
      [[ "${host}" != "${item}" ]] || existing=1
    done
    [[ "${existing}" -eq 1 ]] || unique+=("${item}")
  done
  for item in "${unique[@]}"; do
    [[ -z "${joined}" ]] || joined+=","
    joined+="${item}"
  done
  echo "${joined}"
}

request_certificate() {
  local key="${WORK_DIR}/server.key"
  local csr="${WORK_DIR}/server.csr"
  local conf="${WORK_DIR}/csr.conf"
  local request="${WORK_DIR}/request.json"
  local response="${WORK_DIR}/response.json"
  local address port sans code
  address="$(detect_report_address)"
  port="${GRPC_PORT_HINT:-${ACTIVE_LISTEN##*:}}"
  sans="$(build_sans)"

  openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "${key}"
  {
    echo "[req]"
    echo "prompt=no"
    echo "distinguished_name=dn"
    echo "req_extensions=req_ext"
    echo "[dn]"
    echo "CN=${NODE_ID}"
    echo "[req_ext]"
    echo "subjectAltName=${sans}"
  } >"${conf}"
  openssl req -new -key "${key}" -config "${conf}" -out "${csr}"

  PANEL_CSR="${csr}" PANEL_NODE_ID="${NODE_ID}" PANEL_ADDRESS="${address}" PANEL_PORT="${port}" \
    python3 - <<'PY' >"${request}"
import json, os
with open(os.environ["PANEL_CSR"], "r", encoding="utf-8") as f:
    csr = f.read()
print(json.dumps({
    "node_id": os.environ["PANEL_NODE_ID"],
    "csr_pem": csr,
    "address": os.environ.get("PANEL_ADDRESS", ""),
    "grpc_port": int(os.environ.get("PANEL_PORT") or "50051"),
}))
PY
  code="$(curl -sS -o "${response}" -w '%{http_code}' \
    -X POST "${PANEL_URL%/}/api/v1/pki/agent-certificates" \
    -H "Authorization: Bearer ${ENROLL_TOKEN}" \
    -H "Content-Type: application/json" \
    --data-binary "@${request}")"
  if [[ "${code}" != "201" ]]; then
    die "Panel CA 签发失败 (HTTP ${code}): $(head -c 500 "${response}")"
  fi
  PANEL_RESPONSE="${response}" PANEL_WORK="${WORK_DIR}" python3 - <<'PY'
import json, os
with open(os.environ["PANEL_RESPONSE"], "r", encoding="utf-8") as f:
    data = json.load(f)
work = os.environ["PANEL_WORK"]
for field, name in (("cert_pem", "server.crt"), ("ca_bundle_pem", "ca.crt"), ("control_token", "control-token")):
    value = data.get(field, "")
    if not value:
        raise SystemExit("Panel response missing " + field)
    with open(os.path.join(work, name), "w", encoding="utf-8") as f:
        f.write(value)
serial = data.get("serial", "")
if not serial:
    raise SystemExit("Panel response missing serial")
with open(os.path.join(work, "certificate-serial"), "w", encoding="utf-8") as f:
    f.write(serial)
PY
  openssl x509 -in "${WORK_DIR}/server.crt" -noout -checkend 86400 >/dev/null
  openssl verify -CAfile "${WORK_DIR}/ca.crt" "${WORK_DIR}/server.crt" >/dev/null
  local key_pub cert_pub
  key_pub="$(openssl pkey -in "${key}" -pubout 2>/dev/null)"
  cert_pub="$(openssl x509 -in "${WORK_DIR}/server.crt" -pubkey -noout 2>/dev/null)"
  [[ "${key_pub}" == "${cert_pub}" ]] || die "Panel 返回证书与本地私钥不匹配"
}

complete_migration() {
  local request="${WORK_DIR}/complete-request.json"
  local response="${WORK_DIR}/complete-response.txt"
  local code
  PANEL_NODE_ID="${NODE_ID}" PANEL_SERIAL_FILE="${WORK_DIR}/certificate-serial" \
    python3 - <<'PY' >"${request}"
import json, os
with open(os.environ["PANEL_SERIAL_FILE"], "r", encoding="utf-8") as f:
    serial = f.read().strip()
print(json.dumps({"node_id": os.environ["PANEL_NODE_ID"], "serial": serial}))
PY
  code="$(curl -sS -o "${response}" -w '%{http_code}' \
    -X POST "${PANEL_URL%/}/api/v1/pki/agent-migrations/complete" \
    -H "Authorization: Bearer $(cat "${WORK_DIR}/control-token")" \
    -H "Content-Type: application/json" \
    --data-binary "@${request}")"
  if [[ "${code}" != "204" ]]; then
    die "Panel 未确认迁移完成 (HTTP ${code}): $(head -c 500 "${response}")"
  fi
}

write_new_env() {
  local output="${WORK_DIR}/agent.env"
  PANEL_TOKEN="$(cat "${WORK_DIR}/control-token")" \
  PANEL_URL_VALUE="${PANEL_URL%/}" PANEL_NODE_VALUE="${NODE_ID}" \
  PANEL_REPORT_VALUE="${REPORT_ADDR}" PANEL_SANS_VALUE="${TLS_EXTRA_SANS}" \
  PANEL_TLS_DIR="${TLS_DIR}" \
  PANEL_ENV_SOURCE="${ENV_FILE}" PANEL_ENV_OUTPUT="${output}" \
    python3 - <<'PY'
import os
source = os.environ["PANEL_ENV_SOURCE"]
output = os.environ["PANEL_ENV_OUTPUT"]
updates = {
    "LADDER_TOKEN": os.environ["PANEL_TOKEN"],
    "LADDER_TLS_CERT": os.path.join(os.environ["PANEL_TLS_DIR"], "server.crt"),
    "LADDER_TLS_KEY": os.path.join(os.environ["PANEL_TLS_DIR"], "server.key"),
    "LADDER_TLS_CLIENT_CA": os.path.join(os.environ["PANEL_TLS_DIR"], "ca.crt"),
    "LADDER_PANEL_URL": os.environ["PANEL_URL_VALUE"],
    "LADDER_NODE_ID": os.environ["PANEL_NODE_VALUE"],
    "LADDER_REPORT_ADDRESS": os.environ.get("PANEL_REPORT_VALUE", ""),
    "LADDER_TLS_EXTRA_SANS": os.environ.get("PANEL_SANS_VALUE", ""),
}
seen = set()
lines = []
with open(source, "r", encoding="utf-8") as f:
    for raw in f:
        key = raw.split("=", 1)[0] if "=" in raw and not raw.lstrip().startswith("#") else ""
        if key in updates:
            if key not in seen:
                lines.append(f"{key}={updates[key]}\n")
                seen.add(key)
        else:
            lines.append(raw)
for key, value in updates.items():
    if key not in seen:
        lines.append(f"{key}={value}\n")
with open(output, "w", encoding="utf-8") as f:
    f.writelines(lines)
PY
}

write_new_unit() {
  cat >"${WORK_DIR}/ladder-agent.service" <<EOF
[Unit]
Description=LadderAirport Agent (Panel-managed mTLS)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${USER_NAME}
Group=${GROUP_NAME}
WorkingDirectory=${ACTIVE_DATA_DIR}
EnvironmentFile=${ENV_FILE}
ExecStart=${INSTALL_BIN} -listen=\${LADDER_LISTEN} -token=\${LADDER_TOKEN} -data-dir=\${LADDER_DATA_DIR} -tls-cert=\${LADDER_TLS_CERT} -tls-key=\${LADDER_TLS_KEY} -tls-client-ca=\${LADDER_TLS_CLIENT_CA} -panel-url=\${LADDER_PANEL_URL} -node-id=\${LADDER_NODE_ID} -report-address=\${LADDER_REPORT_ADDRESS} -tls-sans=\${LADDER_TLS_EXTRA_SANS}
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${ACTIVE_DATA_DIR} ${TLS_DIR}
ReadOnlyPaths=${CONF_DIR}
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF
}

prepare_rollback() {
  mkdir -p "${WORK_DIR}/rollback"
  [[ ! -x "${INSTALL_BIN}" ]] || cp -a "${INSTALL_BIN}" "${WORK_DIR}/rollback/ladder-agent"
  cp -a "${ENV_FILE}" "${WORK_DIR}/rollback/agent.env"
  [[ ! -f "${SERVICE_DST}" ]] || cp -a "${SERVICE_DST}" "${WORK_DIR}/rollback/ladder-agent.service"
  [[ ! -d "${TLS_DIR}" ]] || cp -a "${TLS_DIR}" "${WORK_DIR}/rollback/tls"
  ROLLBACK_READY=1
}

rollback() {
  local exit_code=$?
  if [[ "${MIGRATION_DONE}" -eq 1 ]]; then
    [[ -z "${WORK_DIR}" ]] || rm -rf "${WORK_DIR}"
    return
  fi
  if [[ "${SWITCH_STARTED}" -eq 1 && "${ROLLBACK_READY}" -eq 1 ]]; then
    echo "WARNING: 迁移失败，正在恢复旧 Agent..." >&2
    [[ ! -f "${WORK_DIR}/rollback/ladder-agent" ]] || install -m 0755 "${WORK_DIR}/rollback/ladder-agent" "${INSTALL_BIN}"
    install -m 0640 "${WORK_DIR}/rollback/agent.env" "${ENV_FILE}"
    if [[ -f "${WORK_DIR}/rollback/ladder-agent.service" ]]; then
      install -m 0644 "${WORK_DIR}/rollback/ladder-agent.service" "${SERVICE_DST}"
    fi
    rm -rf "${TLS_DIR}"
    [[ ! -d "${WORK_DIR}/rollback/tls" ]] || cp -a "${WORK_DIR}/rollback/tls" "${TLS_DIR}"
    systemctl daemon-reload || true
    systemctl restart "${SERVICE_NAME}" || true
  fi
  [[ -z "${WORK_DIR}" ]] || rm -rf "${WORK_DIR}"
  exit "${exit_code}"
}

if [[ "$(id -u)" -ne 0 ]]; then
  die "请使用 root 运行"
fi
for cmd in curl openssl python3 systemctl install grep seq sleep; do
  need_cmd "${cmd}"
done
[[ -n "${PANEL_URL}" ]] || die "缺少 LADDER_PANEL"
[[ -n "${NODE_ID}" ]] || die "缺少 LADDER_NODE_ID"
[[ -n "${ENROLL_TOKEN}" ]] || die "缺少 LADDER_ENROLL_TOKEN"
if [[ "${PANEL_URL}" != https://* && "${ALLOW_HTTP}" != "1" ]]; then
  die "Panel URL 必须使用 HTTPS；仅隔离测试环境可设置 LADDER_ALLOW_HTTP=1"
fi
[[ -f "${ENV_FILE}" ]] || die "未找到旧 Agent 配置: ${ENV_FILE}"
[[ -f "${SERVICE_DST}" ]] || die "未找到旧 Agent systemd unit: ${SERVICE_DST}"

ACTIVE_LISTEN="$(env_value LADDER_LISTEN)"
ACTIVE_DATA_DIR="$(env_value LADDER_DATA_DIR)"
ACTIVE_LISTEN="${ACTIVE_LISTEN:-0.0.0.0:50051}"
ACTIVE_DATA_DIR="${ACTIVE_DATA_DIR:-${DATA_DIR}}"
WORK_DIR="$(mktemp -d /tmp/ladder-agent-pki-migration.XXXXXX)"
chmod 0700 "${WORK_DIR}"
trap rollback EXIT

AGENT_BINARY="$(resolve_agent_binary)"
echo "==> 预签发 Panel 管理证书（旧 Agent 仍在运行）"
request_certificate
write_new_env
write_new_unit
prepare_rollback

echo "==> 原子切换到 Panel 管理 PKI"
SWITCH_STARTED=1
systemctl stop "${SERVICE_NAME}"
rm -rf "${TLS_DIR}"
install -d -m 0700 -o "${USER_NAME}" -g "${GROUP_NAME}" "${TLS_DIR}"
install -m 0600 -o "${USER_NAME}" -g "${GROUP_NAME}" "${WORK_DIR}/server.key" "${TLS_DIR}/server.key"
install -m 0644 -o "${USER_NAME}" -g "${GROUP_NAME}" "${WORK_DIR}/server.crt" "${TLS_DIR}/server.crt"
install -m 0644 -o "${USER_NAME}" -g "${GROUP_NAME}" "${WORK_DIR}/ca.crt" "${TLS_DIR}/ca.crt"
install -m 0640 -o root -g "${GROUP_NAME}" "${WORK_DIR}/agent.env" "${ENV_FILE}"
install -m 0644 "${WORK_DIR}/ladder-agent.service" "${SERVICE_DST}"
install -m 0755 "${AGENT_BINARY}" "${INSTALL_BIN}"
systemctl daemon-reload
systemctl restart "${SERVICE_NAME}"

stable_checks=0
service_stable=0
for _ in $(seq 1 20); do
  if systemctl is-active --quiet "${SERVICE_NAME}"; then
    stable_checks=$((stable_checks + 1))
    if [[ "${stable_checks}" -ge 3 ]]; then
      service_stable=1
      break
    fi
  else
    stable_checks=0
  fi
  sleep 1
done
[[ "${service_stable}" -eq 1 ]] || die "新 Agent 未能稳定运行"
complete_migration
MIGRATION_DONE=1

# Successful migration deliberately retains no legacy CA/private key or
# rollback copy. The EXIT trap only removes the temporary work directory.
find "${TLS_DIR}" -maxdepth 1 -type f \( -name 'ca.key' -o -name 'ca.srl' \) -delete
rm -f "${INSTALL_BIN}.bak" "${CONF_DIR}/panel-import.txt"
echo "======== PKI 迁移完成 ========"
echo "  节点: ${NODE_ID}"
echo "  Panel: ${PANEL_URL%/}"
echo "  TLS: Panel CA + strict mTLS"
echo "  旧实现: 已删除，未保留旧 CA 私钥或二进制备份"
systemctl --no-pager --full status "${SERVICE_NAME}" || true
