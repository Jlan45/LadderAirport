#!/usr/bin/env bash
# 一键安装 LadderAirport Panel 为 systemd 服务（默认从 GitHub Release 拉最新二进制）。
#
# 无需克隆仓库（推荐）:
#   curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-panel.sh | sudo bash
#
# 生产推荐（固定 session secret + 监听）:
#   curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-panel.sh \
#     | sudo LADDER_SESSION_SECRET='长随机串' LADDER_LISTEN=':8080' bash
#
# 指定版本 / 本地文件 / 源码编译:
#   curl -fsSL https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-panel.sh \
#     | sudo LADDER_VERSION=v0.3.1 LADDER_SESSION_SECRET='长随机串' bash
#   sudo LADDER_SESSION_SECRET='长随机串' ./scripts/install-panel.sh /path/to/panel
#   sudo LADDER_FROM=local LADDER_SESSION_SECRET='长随机串' ./scripts/install-panel.sh
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
INSTALL_BIN="${INSTALL_BIN:-/usr/local/bin/ladder-panel}"
CONF_DIR="${CONF_DIR:-/etc/ladder-panel}"
DATA_DIR="${DATA_DIR:-/var/lib/ladder-panel}"
SERVICE_DST="${SERVICE_DST:-/etc/systemd/system/ladder-panel.service}"
USER_NAME="${LADDER_USER:-ladder-panel}"
GROUP_NAME="${LADDER_GROUP:-ladder-panel}"
LISTEN="${LADDER_LISTEN:-:8080}"
SESSION_SECRET="${LADDER_SESSION_SECRET:-}"
DB_PATH="${LADDER_DB:-${DATA_DIR}/panel.db}"
BOOTSTRAP="${LADDER_BOOTSTRAP:-true}"
BOOTSTRAP_TIMEOUT="${LADDER_BOOTSTRAP_TIMEOUT:-3m}"
BOOTSTRAP_RETRY="${LADDER_BOOTSTRAP_RETRY:-true}"
BOOTSTRAP_RETRY_INTERVAL="${LADDER_BOOTSTRAP_RETRY_INTERVAL:-30s}"
# release | local  （默认 release）
FROM="${LADDER_FROM:-release}"
VERSION="${LADDER_VERSION:-latest}" # latest 或 v0.3.1
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

# --- session secret helpers ---
gen_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

# Only generate when caller did not pass one; actual write happens later and
# never overwrites an existing panel.env secret.
ensure_session_secret() {
  if [[ -z "${SESSION_SECRET}" ]]; then
    SESSION_SECRET="$(gen_secret)"
    echo "==> 已生成 LADDER_SESSION_SECRET（将写入 panel.env，请妥善备份）"
  fi
}

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
  asset="panel-linux-${arch}"

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

  TMPDIR_DL="$(mktemp -d /tmp/ladder-panel-dl.XXXXXX)"
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
    # Prefer already-built binary names used by Makefile / install path
    if [[ -n "${ROOT}" && -x "${ROOT}/bin/panel" ]]; then
      echo "==> 使用仓库 bin/panel" >&2
      printf '%s\n' "${ROOT}/bin/panel"
      return
    fi
    if [[ -n "${ROOT}" && -x "${ROOT}/bin/ladder-panel" ]]; then
      echo "==> 使用仓库 bin/ladder-panel" >&2
      printf '%s\n' "${ROOT}/bin/ladder-panel"
      return
    fi
    [[ -n "${ROOT}" ]] || die "LADDER_FROM=local 需要在仓库内执行脚本"
    echo "==> 本地编译 panel（使用已提交的 panel/web/dist，无需 npm）" >&2
    need_cmd go
    # Offline-friendly: skip npm web rebuild; dist is committed for go:embed
    (cd "${ROOT}/panel" && go build -trimpath -ldflags="-s -w" -o ../bin/panel ./cmd/panel) >&2
    printf '%s\n' "${ROOT}/bin/panel"
    return
  fi

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

ENV_FILE="${CONF_DIR}/panel.env"
echo "==> 配置 ${ENV_FILE}"
if [[ -f "${ENV_FILE}" ]]; then
  echo "    已存在，不覆盖（改 listen/secret/db 请手动编辑后 restart）"
  # Backfill missing keys only (never overwrite existing secret)
  ensure_env_key() {
    local key="$1" val="$2"
    if ! grep -q "^${key}=" "${ENV_FILE}" 2>/dev/null; then
      echo "${key}=${val}" >>"${ENV_FILE}"
      echo "    已追加 ${key}"
    fi
  }
  ensure_env_key "LADDER_LISTEN" "${LISTEN}"
  ensure_env_key "LADDER_DB" "${DB_PATH}"
  if ! grep -q '^LADDER_SESSION_SECRET=' "${ENV_FILE}" 2>/dev/null; then
    ensure_session_secret
    ensure_env_key "LADDER_SESSION_SECRET" "${SESSION_SECRET}"
  fi
  ensure_env_key "LADDER_BOOTSTRAP" "${BOOTSTRAP}"
  ensure_env_key "LADDER_BOOTSTRAP_TIMEOUT" "${BOOTSTRAP_TIMEOUT}"
  ensure_env_key "LADDER_BOOTSTRAP_RETRY" "${BOOTSTRAP_RETRY}"
  ensure_env_key "LADDER_BOOTSTRAP_RETRY_INTERVAL" "${BOOTSTRAP_RETRY_INTERVAL}"
  # Show non-secret keys for operator feedback
  grep -E '^LADDER_LISTEN=|^LADDER_DB=|^LADDER_BOOTSTRAP' "${ENV_FILE}" || true
  if grep -q '^LADDER_SESSION_SECRET=' "${ENV_FILE}" 2>/dev/null; then
    echo "    LADDER_SESSION_SECRET=(已配置，未显示)"
  fi
else
  ensure_session_secret
  {
    echo "# /etc/ladder-panel/panel.env"
    echo "LADDER_LISTEN=${LISTEN}"
    echo "LADDER_DB=${DB_PATH}"
    echo "LADDER_SESSION_SECRET=${SESSION_SECRET}"
    echo "LADDER_BOOTSTRAP=${BOOTSTRAP}"
    echo "LADDER_BOOTSTRAP_TIMEOUT=${BOOTSTRAP_TIMEOUT}"
    echo "LADDER_BOOTSTRAP_RETRY=${BOOTSTRAP_RETRY}"
    echo "LADDER_BOOTSTRAP_RETRY_INTERVAL=${BOOTSTRAP_RETRY_INTERVAL}"
  } >"${ENV_FILE}"
  chmod 640 "${ENV_FILE}"
  chown root:"${GROUP_NAME}" "${ENV_FILE}"
fi

ACTIVE_LISTEN="$(grep -E '^LADDER_LISTEN=' "${ENV_FILE}" | cut -d= -f2- || true)"
ACTIVE_DB="$(grep -E '^LADDER_DB=' "${ENV_FILE}" | cut -d= -f2- || true)"
[[ -n "${ACTIVE_LISTEN}" ]] || ACTIVE_LISTEN="${LISTEN}"
[[ -n "${ACTIVE_DB}" ]] || ACTIVE_DB="${DB_PATH}"

echo "==> 写入 systemd: ${SERVICE_DST}"
# Single-line ExecStart matches install-agent.sh; systemd expands ${LADDER_*} from EnvironmentFile.
cat >"${SERVICE_DST}" <<EOF
[Unit]
Description=LadderAirport Panel (control plane)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${USER_NAME}
Group=${GROUP_NAME}
WorkingDirectory=${DATA_DIR}
EnvironmentFile=${ENV_FILE}
ExecStart=${INSTALL_BIN} -listen \${LADDER_LISTEN} -db \${LADDER_DB} -session-secret \${LADDER_SESSION_SECRET} -bootstrap=\${LADDER_BOOTSTRAP} -bootstrap-timeout \${LADDER_BOOTSTRAP_TIMEOUT} -bootstrap-retry=\${LADDER_BOOTSTRAP_RETRY} -bootstrap-retry-interval \${LADDER_BOOTSTRAP_RETRY_INTERVAL}
Restart=on-failure
RestartSec=3
LimitNOFILE=65536
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
systemctl enable ladder-panel.service
systemctl restart ladder-panel.service
sleep 0.8
systemctl --no-pager --full status ladder-panel.service || true

# Best-effort local bind address for operator tips
HTTP_HINT="http://127.0.0.1:8080"
case "${ACTIVE_LISTEN}" in
  :*) HTTP_HINT="http://127.0.0.1${ACTIVE_LISTEN}" ;;
  0.0.0.0:*) HTTP_HINT="http://127.0.0.1:${ACTIVE_LISTEN##*:}" ;;
  \[::\]:*) HTTP_HINT="http://127.0.0.1:${ACTIVE_LISTEN##*:}" ;;
  *)
    if [[ "${ACTIVE_LISTEN}" == *:* ]]; then
      HTTP_HINT="http://${ACTIVE_LISTEN}"
    fi
    ;;
esac

echo
echo "======== Panel 安装完成 ========"
echo "  二进制:  ${INSTALL_BIN}"
echo "  配置:    ${ENV_FILE}"
echo "  数据:    ${DATA_DIR}"
echo "  数据库:  ${ACTIVE_DB}"
echo "  监听:    ${ACTIVE_LISTEN}"
echo "  服务:    ladder-panel.service (已 enable + start)"
echo "  来源:    FROM=${FROM} VERSION=${VERSION}"
echo
echo "  浏览器:  ${HTTP_HINT}"
echo "  默认管理员密码: admin  （登录后立刻在「设置」中修改）"
echo
echo "建议下一步:"
echo "  1. 在「设置」填写 Public Base URL（用于订阅链接与节点一键安装命令）"
echo "  2. 修改管理员密码与 session secret 备份"
echo "  3. 按需用 Nginx/Caddy 反代 HTTPS 到 ${ACTIVE_LISTEN}"
echo "  4. 安装节点: 见 deploy/README-agent.md 或 Panel「添加节点并生成安装命令」"
echo
echo "运维: systemctl status|restart ladder-panel ; journalctl -u ladder-panel -f"
echo "升级: 重新执行本脚本（覆盖二进制，保留 panel.env 与 SQLite）"
echo "备份: 复制 ${ACTIVE_DB} 与 ${ENV_FILE}"
