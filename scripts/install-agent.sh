#!/usr/bin/env bash
# 一键安装 ladder-agent 为 systemd 服务（默认从 GitHub Release 拉最新二进制）。
#
# 无需克隆仓库（推荐）:
#   curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
#     | sudo LADDER_TOKEN=mysecret bash
#
# 默认开启 TLS（自签 CA + 节点证书）。关闭:
#   curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
#     | sudo LADDER_TLS=0 LADDER_TOKEN=mysecret bash
#
# 指定版本 / 本地文件 / 源码编译:
#   curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh \
#     | sudo LADDER_VERSION=v0.3.1 LADDER_TOKEN=mysecret bash
#   sudo LADDER_TOKEN=mysecret ./scripts/install-agent.sh /path/to/ladder-agent
#   sudo LADDER_FROM=local LADDER_TOKEN=mysecret ./scripts/install-agent.sh
#
set -euo pipefail

REPO="${LADDER_REPO:-Jlan45/LadderAirport}"
API_BASE="${LADDER_GITHUB_API:-https://api.github.com}"
RELEASES_BASE="${LADDER_GITHUB_RELEASES:-https://github.com/${REPO}/releases}"

# 若从 curl|bash 运行，$0 不是仓库路径
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || true)"
ROOT=""
if [[ -n "${SCRIPT_DIR}" && -f "${SCRIPT_DIR}/../Makefile" ]]; then
  ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
fi

BIN_SRC="${1:-}"
INSTALL_BIN="${INSTALL_BIN:-/usr/local/bin/ladder-agent}"
CONF_DIR="${CONF_DIR:-/etc/ladder-agent}"
DATA_DIR="${DATA_DIR:-/var/lib/ladder-agent}"
TLS_DIR="${TLS_DIR:-${CONF_DIR}/tls}"
SERVICE_DST="${SERVICE_DST:-/etc/systemd/system/ladder-agent.service}"
USER_NAME="${LADDER_USER:-ladder}"
GROUP_NAME="${LADDER_GROUP:-ladder}"
LISTEN="${LADDER_LISTEN:-0.0.0.0:50051}"
TOKEN="${LADDER_TOKEN:-}"
# release | local  （默认 release）
FROM="${LADDER_FROM:-release}"
VERSION="${LADDER_VERSION:-latest}" # latest 或 v0.2.0
# TLS: 1=自签并启用（默认）; 0=明文 lab
TLS_ENABLE="${LADDER_TLS:-1}"
TLS_DAYS="${LADDER_TLS_DAYS:-825}"
TLS_CN="${LADDER_TLS_CN:-}"
TLS_EXTRA_SANS="${LADDER_TLS_EXTRA_SANS:-}" # 逗号分隔: DNS:foo,IP:1.2.3.4
# Panel auto-enroll (set by Panel-generated install command)
PANEL_URL="${LADDER_PANEL:-}"          # e.g. https://panel.example.com
NODE_ID="${LADDER_NODE_ID:-}"
REPORT_ADDR="${LADDER_REPORT_ADDRESS:-}" # force reported address; else auto-detect
GRPC_PORT_HINT="${LADDER_GRPC_PORT:-}"
TMPDIR_DL=""

cleanup() {
  if [[ -n "${TMPDIR_DL}" && -d "${TMPDIR_DL}" ]]; then
    rm -rf "${TMPDIR_DL}"
  fi
}
trap cleanup EXIT

die() { echo "ERROR: $*" >&2; exit 1; }

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "需要命令: $1"
}

if [[ "$(id -u)" -ne 0 ]]; then
  die "请使用 root 运行: sudo $0 $*"
fi

need_cmd systemctl
need_cmd install
need_cmd uname

# --- token ---
if [[ -z "${TOKEN}" ]]; then
  if command -v openssl >/dev/null 2>&1; then
    TOKEN="$(openssl rand -hex 16)"
  else
    TOKEN="$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  fi
  echo "==> 已生成 LADDER_TOKEN（请保存到 Panel）: ${TOKEN}"
fi

# --- arch ---
detect_arch() {
  local m
  m="$(uname -m)"
  case "${m}" in
    x86_64 | amd64) echo "amd64" ;;
    aarch64 | arm64) echo "arm64" ;;
    armv7l | armhf) die "暂无 armv7 官方预编译，请自行交叉编译或使用 LADDER_FROM=local" ;;
    *) die "不支持的架构: ${m}（需要 amd64 或 arm64）" ;;
  esac
}

# --- download from GitHub Release ---
# Progress/status MUST go to stderr; only the binary path is printed on stdout
# (callers capture via SRC="$(resolve_binary)").
download_release_binary() {
  need_cmd curl
  local arch asset tag url
  arch="$(detect_arch)"
  asset="ladder-agent-linux-${arch}"

  echo "==> 从 GitHub Release 获取二进制 (${REPO}, ${VERSION}, ${asset})" >&2

  if [[ "${VERSION}" == "latest" ]]; then
    local api_json
    api_json="$(curl -fsSL "${API_BASE}/repos/${REPO}/releases/latest")" || die "无法访问 releases/latest（仓库私有或无 Release？）"
    tag="$(echo "${api_json}" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
    url="$(echo "${api_json}" | tr ',' '\n' | sed -n "s/.*\"browser_download_url\"[[:space:]]*:[[:space:]]*\"\\([^\"]*${asset}\\)\"/\\1/p" | head -1)"
    if [[ -z "${url}" && -n "${tag}" ]]; then
      url="${RELEASES_BASE}/download/${tag}/${asset}"
    fi
  else
    tag="${VERSION}"
    url="${RELEASES_BASE}/download/${tag}/${asset}"
  fi

  [[ -n "${url}" ]] || die "未找到资源 ${asset}。请确认已发布 Release: ${RELEASES_BASE}"

  echo "    tag: ${tag:-?}" >&2
  echo "    url: ${url}" >&2

  TMPDIR_DL="$(mktemp -d /tmp/ladder-agent-dl.XXXXXX)"
  local dest="${TMPDIR_DL}/${asset}"
  curl -fL --retry 3 --retry-delay 1 -o "${dest}" "${url}" || die "下载失败: ${url}"
  [[ -f "${dest}" ]] || die "下载后文件不存在: ${dest}"

  local sums_url
  if [[ -n "${tag}" ]]; then
    sums_url="${RELEASES_BASE}/download/${tag}/SHA256SUMS.txt"
    if curl -fsSL -o "${TMPDIR_DL}/SHA256SUMS.txt" "${sums_url}" 2>/dev/null; then
      echo "==> 校验 SHA256" >&2
      if command -v sha256sum >/dev/null 2>&1; then
        (cd "${TMPDIR_DL}" && grep " ${asset}\$" SHA256SUMS.txt | sha256sum -c -) >&2 \
          || die "SHA256 校验失败"
      fi
    else
      echo "    (无 SHA256SUMS.txt，跳过校验)" >&2
    fi
  fi

  chmod +x "${dest}"
  if ! head -c 4 "${dest}" | grep -q $'\x7fELF'; then
    if file "${dest}" 2>/dev/null | grep -qi 'ELF'; then
      :
    else
      echo "WARNING: 下载文件可能不是 Linux ELF 可执行文件" >&2
    fi
  fi
  # stdout: path only
  printf '%s\n' "${dest}"
}

resolve_binary() {
  if [[ -n "${BIN_SRC}" ]]; then
    [[ -f "${BIN_SRC}" ]] || die "文件不存在: ${BIN_SRC}"
    echo "==> 使用本地文件: ${BIN_SRC}" >&2
    printf '%s\n' "${BIN_SRC}"
    return
  fi

  if [[ "${FROM}" == "local" ]]; then
    if [[ -n "${ROOT}" && -x "${ROOT}/bin/ladder-agent" ]]; then
      echo "==> 使用仓库 bin/ladder-agent" >&2
      printf '%s\n' "${ROOT}/bin/ladder-agent"
      return
    fi
    [[ -n "${ROOT}" ]] || die "LADDER_FROM=local 需要在仓库内执行脚本"
    echo "==> 本地编译 agent" >&2
    if [[ ! -f "${ROOT}/agent/sing-box/go.mod" ]]; then
      git -C "${ROOT}" submodule update --init --recursive
    fi
    need_cmd go
    (cd "${ROOT}" && make agent) >&2
    printf '%s\n' "${ROOT}/bin/ladder-agent"
    return
  fi

  download_release_binary
}

# Collect SANs for the agent server cert (DNS + IPs Panel may dial).
build_san_list() {
  local -a sans=()
  local h ip primary

  sans+=("DNS:localhost")
  sans+=("IP:127.0.0.1")
  sans+=("IP:::1")

  h="$(hostname -f 2>/dev/null || hostname 2>/dev/null || true)"
  if [[ -n "${h}" && "${h}" != "localhost" ]]; then
    sans+=("DNS:${h}")
  fi
  h="$(hostname -s 2>/dev/null || true)"
  if [[ -n "${h}" && "${h}" != "localhost" ]]; then
    sans+=("DNS:${h}")
  fi

  # Non-loopback IPv4s
  if command -v hostname >/dev/null 2>&1; then
    for ip in $(hostname -I 2>/dev/null || true); do
      case "${ip}" in
        127.*|::1) continue ;;
        *:*) sans+=("IP:${ip}") ;; # v6
        *) sans+=("IP:${ip}") ;;
      esac
    done
  fi

  # Optional public IP (best-effort; skip if offline)
  if command -v curl >/dev/null 2>&1; then
    primary="$(curl -fsS --max-time 3 https://api.ipify.org 2>/dev/null || true)"
    if [[ "${primary}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      sans+=("IP:${primary}")
    fi
  fi

  if [[ -n "${TLS_EXTRA_SANS}" ]]; then
    local IFS=','
    local extra
    for extra in ${TLS_EXTRA_SANS}; do
      extra="$(echo "${extra}" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
      [[ -n "${extra}" ]] || continue
      case "${extra}" in
        DNS:*|IP:*) sans+=("${extra}") ;;
        *:* ) sans+=("IP:${extra}") ;; # likely v6
        *.*)
          if [[ "${extra}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            sans+=("IP:${extra}")
          else
            sans+=("DNS:${extra}")
          fi
          ;;
        *) sans+=("DNS:${extra}") ;;
      esac
    done
  fi

  # Dedup preserving order
  local -a out=()
  local s seen
  for s in "${sans[@]}"; do
    seen=0
    for x in "${out[@]+"${out[@]}"}"; do
      if [[ "${x}" == "${s}" ]]; then seen=1; break; fi
    done
    if [[ "${seen}" -eq 0 ]]; then
      out+=("${s}")
    fi
  done

  local joined=""
  for s in "${out[@]}"; do
    if [[ -z "${joined}" ]]; then
      joined="${s}"
    else
      joined="${joined},${s}"
    fi
  done
  echo "${joined}"
}

# Generate node-local CA + leaf cert if missing. Idempotent.
ensure_tls_material() {
  need_cmd openssl

  mkdir -p "${TLS_DIR}"
  local ca_key="${TLS_DIR}/ca.key"
  local ca_crt="${TLS_DIR}/ca.crt"
  local srv_key="${TLS_DIR}/server.key"
  local srv_crt="${TLS_DIR}/server.crt"
  local srv_csr="${TLS_DIR}/server.csr"
  local srv_ext="${TLS_DIR}/server.ext"

  if [[ -f "${ca_crt}" && -f "${srv_crt}" && -f "${srv_key}" ]]; then
    echo "==> TLS 证书已存在，复用: ${TLS_DIR}"
    return
  fi

  echo "==> 生成自签 TLS 材料 → ${TLS_DIR}"
  local cn="${TLS_CN}"
  if [[ -z "${cn}" ]]; then
    cn="$(hostname -f 2>/dev/null || hostname 2>/dev/null || echo ladder-agent)"
  fi
  local san
  san="$(build_san_list)"
  echo "    CN=${cn}"
  echo "    SAN=${san}"

  openssl genrsa -out "${ca_key}" 2048 2>/dev/null
  openssl req -x509 -new -nodes \
    -key "${ca_key}" \
    -sha256 \
    -days "${TLS_DAYS}" \
    -subj "/CN=LadderAirport Agent CA ($(hostname -s 2>/dev/null || echo node))" \
    -out "${ca_crt}"

  openssl genrsa -out "${srv_key}" 2048 2>/dev/null
  openssl req -new \
    -key "${srv_key}" \
    -subj "/CN=${cn}" \
    -out "${srv_csr}"

  cat >"${srv_ext}" <<EOF
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=${san}
EOF

  openssl x509 -req \
    -in "${srv_csr}" \
    -CA "${ca_crt}" \
    -CAkey "${ca_key}" \
    -CAcreateserial \
    -out "${srv_crt}" \
    -days "${TLS_DAYS}" \
    -sha256 \
    -extfile "${srv_ext}"

  rm -f "${srv_csr}" "${srv_ext}" "${TLS_DIR}/ca.srl"

  # CA key stays on node for re-issue; lock down
  chmod 600 "${ca_key}" "${srv_key}"
  chmod 644 "${ca_crt}" "${srv_crt}"
  chown -R root:"${GROUP_NAME}" "${TLS_DIR}"
  # ladder user needs read server cert/key at runtime
  chmod 640 "${srv_key}"
  # ca.key only root
  chmod 600 "${ca_key}"
}

write_panel_import() {
  local import_file="${CONF_DIR}/panel-import.txt"
  local ca_crt="${TLS_DIR}/ca.crt"
  local addr_hint
  addr_hint="$(detect_report_address)"
  [[ -n "${addr_hint}" ]] || addr_hint="<本机公网或内网 IP>"

  {
    echo "# Panel 节点登记（本文件含敏感信息，权限 640）"
    echo "# 生成时间: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo
    echo "Address     : ${addr_hint}"
    echo "gRPC 端口   : ${GRPC_PORT}"
    echo "Token       : ${ACTIVE_TOKEN}"
    if [[ -n "${PANEL_URL}" ]]; then
      echo "Panel       : ${PANEL_URL} (auto-enroll attempted)"
    fi
    echo "TLS         : see LADDER_TLS / ca.crt"
    echo
    echo "-----BEGIN PANEL_CA_PEM-----"
    if [[ -f "${ca_crt}" ]]; then
      cat "${ca_crt}"
    fi
    echo "-----END PANEL_CA_PEM-----"
  } >"${import_file}"
  chmod 640 "${import_file}"
  chown root:"${GROUP_NAME}" "${import_file}"
  echo "${import_file}"
}

# Pick an address Panel should dial (override with LADDER_REPORT_ADDRESS).
detect_report_address() {
  if [[ -n "${REPORT_ADDR}" ]]; then
    echo "${REPORT_ADDR}"
    return
  fi
  local ip pub
  # Prefer first non-loopback from hostname -I
  for ip in $(hostname -I 2>/dev/null || true); do
    case "${ip}" in
      127.*|::1) continue ;;
      *)
        echo "${ip}"
        return
        ;;
    esac
  done
  if command -v curl >/dev/null 2>&1; then
    pub="$(curl -fsS --max-time 3 https://api.ipify.org 2>/dev/null || true)"
    if [[ "${pub}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      echo "${pub}"
      return
    fi
  fi
  echo ""
}

# POST address + CA to Panel so operator need not paste manually.
enroll_to_panel() {
  local panel="${PANEL_URL%/}"
  if [[ -z "${panel}" ]]; then
    echo "==> 未设置 LADDER_PANEL，跳过自动上报（请在 Panel 设置 Public Base URL 后重新生成安装命令）"
    return 0
  fi
  need_cmd curl
  local addr ca_pem="" tls_json="true" port payload
  addr="$(detect_report_address)"
  port="${GRPC_PORT_HINT:-${GRPC_PORT}}"
  if [[ -z "${addr}" ]]; then
    echo "WARNING: 无法探测上报地址，跳过 enroll（可设 LADDER_REPORT_ADDRESS）" >&2
    return 0
  fi
  if [[ -f "${TLS_DIR}/ca.crt" ]]; then
    ca_pem="$(cat "${TLS_DIR}/ca.crt")"
  else
    tls_json="false"
  fi
  echo "==> 向 Panel 自动上报: ${panel}/api/v1/agent/enroll"
  echo "    address=${addr} port=${port} node_id=${NODE_ID:-auto}"

  if command -v python3 >/dev/null 2>&1; then
    payload="$(PANEL="${panel}" TOKEN="${ACTIVE_TOKEN}" NODE_ID="${NODE_ID}" \
      ADDR="${addr}" PORT="${port}" CA="${ca_pem}" TLS="${tls_json}" HOST="$(hostname -f 2>/dev/null || hostname)" \
      python3 - <<'PY'
import json, os
print(json.dumps({
  "token": os.environ.get("TOKEN", ""),
  "node_id": os.environ.get("NODE_ID", ""),
  "address": os.environ.get("ADDR", ""),
  "grpc_port": int(os.environ.get("PORT") or "50051"),
  "ca_cert_pem": os.environ.get("CA", ""),
  "hostname": os.environ.get("HOST", ""),
  "tls_enabled": os.environ.get("TLS", "true").lower() in ("1", "true", "yes"),
}))
PY
)"
  else
    echo "WARNING: 需要 python3 以安全编码 enroll JSON，跳过自动上报" >&2
    return 0
  fi

  local resp code
  resp="$(curl -fsS -w '\n%{http_code}' -X POST "${panel}/api/v1/agent/enroll" \
    -H "Authorization: Bearer ${ACTIVE_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "${payload}" 2>&1)" || {
    echo "WARNING: enroll 请求失败: ${resp}" >&2
    return 0
  }
  code="$(echo "${resp}" | tail -n1)"
  body="$(echo "${resp}" | sed '$d')"
  if [[ "${code}" == "200" ]]; then
    echo "    enroll 成功 (HTTP ${code})"
    echo "${body}" | head -c 400
    echo
  else
    echo "WARNING: enroll HTTP ${code}: ${body}" >&2
  fi
}

echo "==> 创建用户/组 ${USER_NAME}"
getent group "${GROUP_NAME}" >/dev/null || groupadd --system "${GROUP_NAME}"
if ! id -u "${USER_NAME}" >/dev/null 2>&1; then
  useradd --system --gid "${GROUP_NAME}" --home-dir "${DATA_DIR}" \
    --shell /usr/sbin/nologin --create-home "${USER_NAME}" 2>/dev/null \
    || useradd --system --gid "${GROUP_NAME}" --home-dir "${DATA_DIR}" \
      --shell /bin/false "${USER_NAME}"
fi

echo "==> 准备目录"
mkdir -p "${CONF_DIR}" "${DATA_DIR}"
chown -R "${USER_NAME}:${GROUP_NAME}" "${DATA_DIR}"
chmod 750 "${DATA_DIR}"
chmod 755 "${CONF_DIR}"

SRC="$(resolve_binary)"
echo "==> 安装二进制 → ${INSTALL_BIN}"
install -m 0755 "${SRC}" "${INSTALL_BIN}"

TLS_CERT_PATH=""
TLS_KEY_PATH=""
case "${TLS_ENABLE}" in
  1|true|TRUE|yes|YES|on|ON)
    ensure_tls_material
    TLS_CERT_PATH="${TLS_DIR}/server.crt"
    TLS_KEY_PATH="${TLS_DIR}/server.key"
    ;;
  0|false|FALSE|no|NO|off|OFF)
    echo "==> LADDER_TLS=${TLS_ENABLE}: 跳过 TLS（明文 gRPC lab 模式）"
    ;;
  *)
    die "LADDER_TLS 取值无效: ${TLS_ENABLE}（用 1 或 0）"
    ;;
esac

ENV_FILE="${CONF_DIR}/agent.env"
echo "==> 配置 ${ENV_FILE}"
if [[ -f "${ENV_FILE}" ]]; then
  echo "    已存在，不覆盖（改 Token/TLS 请手动编辑后 restart）"
  # If TLS newly enabled but env lacks cert paths, append once
  if [[ -n "${TLS_CERT_PATH}" ]]; then
    if ! grep -q '^LADDER_TLS_CERT=' "${ENV_FILE}" 2>/dev/null; then
      {
        echo "LADDER_TLS_CERT=${TLS_CERT_PATH}"
        echo "LADDER_TLS_KEY=${TLS_KEY_PATH}"
      } >>"${ENV_FILE}"
      echo "    已追加 LADDER_TLS_CERT/KEY"
    fi
  fi
  grep -E '^LADDER_TOKEN=|^LADDER_TLS_' "${ENV_FILE}" || true
else
  {
    echo "LADDER_LISTEN=${LISTEN}"
    echo "LADDER_TOKEN=${TOKEN}"
    echo "LADDER_DATA_DIR=${DATA_DIR}"
    if [[ -n "${TLS_CERT_PATH}" ]]; then
      echo "LADDER_TLS_CERT=${TLS_CERT_PATH}"
      echo "LADDER_TLS_KEY=${TLS_KEY_PATH}"
    fi
  } >"${ENV_FILE}"
  chmod 640 "${ENV_FILE}"
  chown root:"${GROUP_NAME}" "${ENV_FILE}"
fi

GRPC_PORT="${LISTEN##*:}"
ACTIVE_TOKEN="$(grep -E '^LADDER_TOKEN=' "${ENV_FILE}" | cut -d= -f2- || true)"

# Build ExecStart TLS args only when certs configured
TLS_EXEC_ARGS=""
if [[ -n "${TLS_CERT_PATH}" ]]; then
  TLS_EXEC_ARGS=" -tls-cert \${LADDER_TLS_CERT} -tls-key \${LADDER_TLS_KEY}"
fi

echo "==> 写入 systemd: ${SERVICE_DST}"
cat >"${SERVICE_DST}" <<EOF
[Unit]
Description=LadderAirport Agent (sing-box control plane)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${USER_NAME}
Group=${GROUP_NAME}
WorkingDirectory=${DATA_DIR}
EnvironmentFile=${ENV_FILE}
ExecStart=${INSTALL_BIN} -listen \${LADDER_LISTEN} -token \${LADDER_TOKEN} -data-dir \${LADDER_DATA_DIR}${TLS_EXEC_ARGS}
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${DATA_DIR}
ReadOnlyPaths=${CONF_DIR}
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable ladder-agent.service
systemctl restart ladder-agent.service
sleep 0.8
systemctl --no-pager --full status ladder-agent.service || true

IMPORT_FILE=""
if [[ -n "${TLS_CERT_PATH}" ]] || [[ -n "${PANEL_URL}" ]]; then
  IMPORT_FILE="$(write_panel_import)"
fi

# Auto-report address + CA to Panel (no manual paste when LADDER_PANEL is set).
enroll_to_panel

echo
echo "======== 安装完成 ========"
echo "  二进制:  ${INSTALL_BIN}"
echo "  配置:    ${ENV_FILE}"
echo "  数据:    ${DATA_DIR}"
echo "  服务:    ladder-agent.service (已 enable + start)"
echo "  来源:    FROM=${FROM} VERSION=${VERSION}"
if [[ -n "${TLS_CERT_PATH}" ]]; then
  echo "  TLS:     ON  cert=${TLS_CERT_PATH}"
else
  echo "  TLS:     OFF（明文 gRPC）"
fi
if [[ -n "${PANEL_URL}" ]]; then
  echo "  Panel:   ${PANEL_URL}（已尝试自动 enroll）"
  echo "  请在 Panel 刷新节点 → 探测"
else
  echo "  Panel:   未设置 LADDER_PANEL（无自动上报）"
  echo "  Token  : ${ACTIVE_TOKEN}"
  if [[ -n "${IMPORT_FILE}" ]]; then
    echo "  登记提示: ${IMPORT_FILE}"
  fi
fi
echo
echo "运维: systemctl status|restart ladder-agent ; journalctl -u ladder-agent -f"
echo "升级: 重新执行本脚本（会覆盖二进制，保留 agent.env 与 TLS 证书）"
echo "强制重签 TLS: 删除 ${TLS_DIR} 后 LADDER_TLS=1 再装（会再次 enroll）"
