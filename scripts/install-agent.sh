#!/usr/bin/env bash
# Install labber-agent + systemd unit (auto-start on boot).
#
# From repo root:
#   sudo ./scripts/install-agent.sh
#   sudo LABBER_TOKEN=mysecret ./scripts/install-agent.sh
#   sudo ./scripts/install-agent.sh /path/to/prebuilt/labber-agent
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_SRC="${1:-}"
INSTALL_BIN="${INSTALL_BIN:-/usr/local/bin/labber-agent}"
CONF_DIR="${CONF_DIR:-/etc/labber-agent}"
DATA_DIR="${DATA_DIR:-/var/lib/labber-agent}"
SERVICE_DST="${SERVICE_DST:-/etc/systemd/system/labber-agent.service}"
USER_NAME="${LABBER_USER:-labber}"
GROUP_NAME="${LABBER_GROUP:-labber}"
LISTEN="${LABBER_LISTEN:-0.0.0.0:50051}"
TOKEN="${LABBER_TOKEN:-}"
SKIP_BUILD="${SKIP_BUILD:-0}"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "ERROR: run as root: sudo $0 $*" >&2
  exit 1
fi

if [[ -z "${TOKEN}" ]]; then
  if command -v openssl >/dev/null 2>&1; then
    TOKEN="$(openssl rand -hex 16)"
  else
    TOKEN="$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  fi
  echo "==> generated LABBER_TOKEN (save for Panel): ${TOKEN}"
fi

echo "==> user/group ${USER_NAME}"
getent group "${GROUP_NAME}" >/dev/null || groupadd --system "${GROUP_NAME}"
if ! id -u "${USER_NAME}" >/dev/null 2>&1; then
  useradd --system --gid "${GROUP_NAME}" --home-dir "${DATA_DIR}" \
    --shell /usr/sbin/nologin --create-home "${USER_NAME}" 2>/dev/null \
    || useradd --system --gid "${GROUP_NAME}" --home-dir "${DATA_DIR}" \
      --shell /bin/false "${USER_NAME}"
fi

echo "==> directories"
mkdir -p "${CONF_DIR}" "${DATA_DIR}"
chown -R "${USER_NAME}:${GROUP_NAME}" "${DATA_DIR}"
chmod 750 "${DATA_DIR}"
chmod 755 "${CONF_DIR}"

install_binary() {
  local src="$1"
  install -m 0755 "${src}" "${INSTALL_BIN}"
  echo "    installed ${INSTALL_BIN}"
}

if [[ -n "${BIN_SRC}" ]]; then
  [[ -f "${BIN_SRC}" ]] || { echo "ERROR: not found: ${BIN_SRC}" >&2; exit 1; }
  echo "==> use provided binary"
  install_binary "${BIN_SRC}"
elif [[ -x "${ROOT}/bin/labber-agent" ]]; then
  echo "==> use repo bin/labber-agent"
  install_binary "${ROOT}/bin/labber-agent"
elif [[ "${SKIP_BUILD}" != "1" ]]; then
  echo "==> build agent"
  if [[ ! -f "${ROOT}/agent/sing-box/go.mod" ]]; then
    git -C "${ROOT}" submodule update --init --recursive
  fi
  (cd "${ROOT}" && make agent)
  install_binary "${ROOT}/bin/labber-agent"
else
  echo "ERROR: no binary found and SKIP_BUILD=1" >&2
  exit 1
fi

ENV_FILE="${CONF_DIR}/agent.env"
echo "==> config ${ENV_FILE}"
if [[ -f "${ENV_FILE}" ]]; then
  echo "    exists — not overwriting (edit by hand to change token)"
  # shellcheck disable=SC1090
  # show current token hint
  grep -E '^LABBER_TOKEN=' "${ENV_FILE}" || true
else
  cat >"${ENV_FILE}" <<EOF
LABBER_LISTEN=${LISTEN}
LABBER_TOKEN=${TOKEN}
LABBER_DATA_DIR=${DATA_DIR}
EOF
  chmod 640 "${ENV_FILE}"
  chown root:"${GROUP_NAME}" "${ENV_FILE}"
fi

# Port from LISTEN for Panel hint
GRPC_PORT="${LISTEN##*:}"

echo "==> systemd unit ${SERVICE_DST}"
cat >"${SERVICE_DST}" <<EOF
[Unit]
Description=LabberAirport Agent (sing-box control plane)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${USER_NAME}
Group=${GROUP_NAME}
WorkingDirectory=${DATA_DIR}
EnvironmentFile=${ENV_FILE}
ExecStart=${INSTALL_BIN} -listen \${LABBER_LISTEN} -token \${LABBER_TOKEN} -data-dir \${LABBER_DATA_DIR}
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${DATA_DIR}
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable labber-agent.service
systemctl restart labber-agent.service
sleep 0.8
systemctl --no-pager --full status labber-agent.service || true

# Resolve token actually in env file
ACTIVE_TOKEN="$(grep -E '^LABBER_TOKEN=' "${ENV_FILE}" | cut -d= -f2- || true)"

echo
echo "======== 安装完成 ========"
echo "  二进制:  ${INSTALL_BIN}"
echo "  配置:    ${ENV_FILE}"
echo "  数据:    ${DATA_DIR}"
echo "  服务:    labber-agent.service (已 enable + start)"
echo
echo "在 Panel「节点」里登记:"
echo "  Address : <本机 IP，不要填 127.0.0.1 给远端 Panel>"
echo "  gRPC端口: ${GRPC_PORT}"
echo "  Token   : ${ACTIVE_TOKEN}"
echo
echo "运维命令:"
echo "  systemctl status labber-agent"
echo "  journalctl -u labber-agent -f"
echo "  systemctl restart labber-agent"
echo "  # 改 token: 编辑 ${ENV_FILE} 后 systemctl restart labber-agent"
LABBER_TOKEN} -data-dir \${LABBER_DATA_DIR}
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${DATA_DIR}
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

# Fix escaped dollars: we want systemd ${VAR} not shell-escaped
# The heredoc above with \${} becomes ${} in file when using unquoted? 
# With <<EOF unquoted, \${ becomes ${ in output. Good.

systemctl daemon-reload
systemctl enable labber-agent.service
systemctl restart labber-agent.service

sleep 0.5
systemctl --no-pager --full status labber-agent.service || true

echo
echo "==> installed"
echo "  binary:  ${INSTALL_BIN}"
echo "  config:  ${CONF_DIR}/agent.env"
echo "  data:    ${DATA_DIR}"
echo "  service: labber-agent.service (enabled)"
echo
echo "Panel 登记节点时填写:"
echo "  Address:  <本机公网或内网 IP>"
echo "  gRPC:     $(echo "${LISTEN}" | sed 's/.*://')"
echo "  Token:    ${TOKEN}"
echo
echo "常用命令:"
echo "  systemctl status labber-agent"
echo "  journalctl -u labber-agent -f"
echo "  systemctl restart labber-agent"
