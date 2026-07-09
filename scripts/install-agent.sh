#!/usr/bin/env bash
# 一键安装 ladder-agent 为 systemd 服务（默认从 GitHub Release 拉最新二进制）。
#
# 无需克隆仓库（推荐）:
#   curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh | sudo bash
#   curl -fsSL ... | sudo LADDER_TOKEN=mysecret bash
#
# 指定版本 / 本地文件 / 源码编译:
#   sudo LADDER_VERSION=v0.1.0 ./scripts/install-agent.sh
#   sudo ./scripts/install-agent.sh /path/to/ladder-agent
#   sudo LADDER_FROM=local ./scripts/install-agent.sh   # 使用仓库 bin/ 或 make agent
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
SERVICE_DST="${SERVICE_DST:-/etc/systemd/system/ladder-agent.service}"
USER_NAME="${LADDER_USER:-ladder}"
GROUP_NAME="${LADDER_GROUP:-ladder}"
LISTEN="${LADDER_LISTEN:-0.0.0.0:50051}"
TOKEN="${LADDER_TOKEN:-}"
# release | local  （默认 release）
FROM="${LADDER_FROM:-release}"
VERSION="${LADDER_VERSION:-latest}" # latest 或 v0.1.0
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
download_release_binary() {
  need_cmd curl
  local arch asset tag url
  arch="$(detect_arch)"
  asset="ladder-agent-linux-${arch}"

  echo "==> 从 GitHub Release 获取二进制 (${REPO}, ${VERSION}, ${asset})"

  if [[ "${VERSION}" == "latest" ]]; then
    # API: latest release assets
    local api_json
    api_json="$(curl -fsSL "${API_BASE}/repos/${REPO}/releases/latest")" || die "无法访问 releases/latest（仓库私有或无 Release？）"
    tag="$(echo "${api_json}" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
    url="$(echo "${api_json}" | tr ',' '\n' | sed -n "s/.*\"browser_download_url\"[[:space:]]*:[[:space:]]*\"\\([^\"]*${asset}\\)\"/\\1/p" | head -1)"
    # fallback: construct URL if tag found
    if [[ -z "${url}" && -n "${tag}" ]]; then
      url="${RELEASES_BASE}/download/${tag}/${asset}"
    fi
  else
    tag="${VERSION}"
    url="${RELEASES_BASE}/download/${tag}/${asset}"
  fi

  [[ -n "${url}" ]] || die "未找到资源 ${asset}。请确认已发布 Release: ${RELEASES_BASE}"

  echo "    tag: ${tag:-?}"
  echo "    url: ${url}"

  TMPDIR_DL="$(mktemp -d /tmp/ladder-agent-dl.XXXXXX)"
  local dest="${TMPDIR_DL}/${asset}"
  curl -fL --retry 3 --retry-delay 1 -o "${dest}" "${url}" || die "下载失败: ${url}"

  # optional checksum
  local sums_url sums
  if [[ -n "${tag}" ]]; then
    sums_url="${RELEASES_BASE}/download/${tag}/SHA256SUMS.txt"
    if curl -fsSL -o "${TMPDIR_DL}/SHA256SUMS.txt" "${sums_url}" 2>/dev/null; then
      echo "==> 校验 SHA256"
      if command -v sha256sum >/dev/null 2>&1; then
        (cd "${TMPDIR_DL}" && grep " ${asset}\$" SHA256SUMS.txt | sha256sum -c -) \
          || die "SHA256 校验失败"
      fi
    else
      echo "    (无 SHA256SUMS.txt，跳过校验)"
    fi
  fi

  chmod +x "${dest}"
  # smoke: executable
  if ! head -c 4 "${dest}" | grep -q $'\x7fELF'; then
    # still try — might be script; for Go binary expect ELF on linux
    if file "${dest}" 2>/dev/null | grep -qi 'ELF'; then
      :
    else
      echo "WARNING: 下载文件可能不是 Linux ELF 可执行文件" >&2
    fi
  fi
  echo "${dest}"
}

resolve_binary() {
  # 1) explicit path argument
  if [[ -n "${BIN_SRC}" ]]; then
    [[ -f "${BIN_SRC}" ]] || die "文件不存在: ${BIN_SRC}"
    echo "==> 使用本地文件: ${BIN_SRC}"
    echo "${BIN_SRC}"
    return
  fi

  # 2) local mode: repo bin or build
  if [[ "${FROM}" == "local" ]]; then
    if [[ -n "${ROOT}" && -x "${ROOT}/bin/ladder-agent" ]]; then
      echo "==> 使用仓库 bin/ladder-agent"
      echo "${ROOT}/bin/ladder-agent"
      return
    fi
    [[ -n "${ROOT}" ]] || die "LADDER_FROM=local 需要在仓库内执行脚本"
    echo "==> 本地编译 agent"
    if [[ ! -f "${ROOT}/agent/sing-box/go.mod" ]]; then
      git -C "${ROOT}" submodule update --init --recursive
    fi
    need_cmd go
    (cd "${ROOT}" && make agent)
    echo "${ROOT}/bin/ladder-agent"
    return
  fi

  # 3) default: GitHub Release
  download_release_binary
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

ENV_FILE="${CONF_DIR}/agent.env"
echo "==> 配置 ${ENV_FILE}"
if [[ -f "${ENV_FILE}" ]]; then
  echo "    已存在，不覆盖（改 Token 请手动编辑后 restart）"
  grep -E '^LADDER_TOKEN=' "${ENV_FILE}" || true
else
  cat >"${ENV_FILE}" <<EOF
LADDER_LISTEN=${LISTEN}
LADDER_TOKEN=${TOKEN}
LADDER_DATA_DIR=${DATA_DIR}
EOF
  chmod 640 "${ENV_FILE}"
  chown root:"${GROUP_NAME}" "${ENV_FILE}"
fi

GRPC_PORT="${LISTEN##*:}"

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
ExecStart=${INSTALL_BIN} -listen \${LADDER_LISTEN} -token \${LADDER_TOKEN} -data-dir \${LADDER_DATA_DIR}
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
systemctl enable ladder-agent.service
systemctl restart ladder-agent.service
sleep 0.8
systemctl --no-pager --full status ladder-agent.service || true

ACTIVE_TOKEN="$(grep -E '^LADDER_TOKEN=' "${ENV_FILE}" | cut -d= -f2- || true)"

echo
echo "======== 安装完成 ========"
echo "  二进制:  ${INSTALL_BIN}"
echo "  配置:    ${ENV_FILE}"
echo "  数据:    ${DATA_DIR}"
echo "  服务:    ladder-agent.service (已 enable + start)"
echo "  来源:    FROM=${FROM} VERSION=${VERSION}"
echo
echo "在 Panel「节点」里登记:"
echo "  Address : <本机 IP>"
echo "  gRPC端口: ${GRPC_PORT}"
echo "  Token   : ${ACTIVE_TOKEN}"
echo
echo "运维: systemctl status|restart ladder-agent ; journalctl -u ladder-agent -f"
echo "升级: 重新执行本脚本（会覆盖二进制，保留 agent.env）"
