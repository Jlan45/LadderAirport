#!/usr/bin/env bash
# One-time Panel migration to the mandatory management PKI implementation.
# A rollback copy exists only while this script is running. On success the old
# binary, legacy database columns and temporary rollback are not retained.
set -euo pipefail

REPO="${LADDER_REPO:-Jlan45/LadderAirport}"
API_BASE="${LADDER_GITHUB_API:-https://api.github.com}"
RELEASES_BASE="${LADDER_GITHUB_RELEASES:-https://github.com/${REPO}/releases}"
VERSION="${LADDER_VERSION:-latest}"
SOURCE_PANEL_BINARY="${LADDER_PANEL_BINARY:-}"

INSTALL_BIN="${INSTALL_BIN:-/usr/local/bin/ladder-panel}"
CONF_DIR="${CONF_DIR:-/etc/ladder-panel}"
DATA_DIR="${DATA_DIR:-/var/lib/ladder-panel}"
ENV_FILE="${ENV_FILE:-${CONF_DIR}/panel.env}"
SERVICE_DST="${SERVICE_DST:-/etc/systemd/system/ladder-panel.service}"
SERVICE_NAME="${LADDER_SERVICE_NAME:-ladder-panel.service}"
USER_NAME="${LADDER_USER:-ladder-panel}"
GROUP_NAME="${LADDER_GROUP:-ladder-panel}"
DB_OVERRIDE="${LADDER_DB:-}"

WORK_DIR=""
ROLLBACK_READY=0
SWITCH_STARTED=0
MIGRATION_DONE=0
PKI_EXISTED=0
ACTIVE_DB=""
ACTIVE_PKI_DIR=""
PANEL_BINARY=""

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

download_panel() {
  local arch asset tag url dest api_json sums
  arch="$(detect_arch)"
  asset="ladder-panel-linux-${arch}"
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
  echo "==> 下载 Panel ${tag}: ${url}" >&2
  curl -fL --retry 3 --retry-delay 1 -o "${dest}" "${url}"
  sums="${WORK_DIR}/SHA256SUMS.txt"
  if curl -fsSL -o "${sums}" "${RELEASES_BASE}/download/${tag}/SHA256SUMS.txt" 2>/dev/null &&
    command -v sha256sum >/dev/null 2>&1; then
    (cd "${WORK_DIR}" && grep " ${asset}\$" SHA256SUMS.txt | sha256sum -c -) >&2
  fi
  chmod 0755 "${dest}"
  "${dest}" -version >/dev/null
  echo "${dest}"
}

resolve_panel_binary() {
  if [[ -n "${SOURCE_PANEL_BINARY}" ]]; then
    [[ -x "${SOURCE_PANEL_BINARY}" ]] || die "LADDER_PANEL_BINARY 不可执行: ${SOURCE_PANEL_BINARY}"
    local dest="${WORK_DIR}/ladder-panel-local"
    install -m 0755 "${SOURCE_PANEL_BINARY}" "${dest}"
    "${dest}" -version >/dev/null
    echo "${dest}"
    return
  fi
  download_panel
}

validate_paths() {
  [[ -n "${ACTIVE_DB}" && "${ACTIVE_DB}" != "/" ]] || die "数据库路径无效: ${ACTIVE_DB}"
  [[ -n "${ACTIVE_PKI_DIR}" && "${ACTIVE_PKI_DIR}" != "/" ]] || die "PKI 路径无效: ${ACTIVE_PKI_DIR}"
  [[ "${ACTIVE_PKI_DIR}" != "${DATA_DIR}" ]] || die "PKI 路径不能等于整个数据目录"
  case "${ACTIVE_PKI_DIR}" in
    /bin|/boot|/dev|/etc|/home|/lib|/lib64|/opt|/root|/run|/sbin|/srv|/tmp|/usr|/var)
      die "PKI 路径过于宽泛: ${ACTIVE_PKI_DIR}"
      ;;
  esac
}

prepare_rollback() {
  local rollback="${WORK_DIR}/rollback"
  install -d -m 0700 "${rollback}" "${rollback}/db"
  [[ ! -x "${INSTALL_BIN}" ]] || cp -a "${INSTALL_BIN}" "${rollback}/ladder-panel"
  [[ ! -f "${ENV_FILE}" ]] || cp -a "${ENV_FILE}" "${rollback}/panel.env"
  [[ ! -f "${SERVICE_DST}" ]] || cp -a "${SERVICE_DST}" "${rollback}/ladder-panel.service"
  local db_file
  for db_file in "${ACTIVE_DB}" "${ACTIVE_DB}-wal" "${ACTIVE_DB}-shm"; do
    [[ ! -f "${db_file}" ]] || cp -a "${db_file}" "${rollback}/db/"
  done
  if [[ -d "${ACTIVE_PKI_DIR}" ]]; then
    cp -a "${ACTIVE_PKI_DIR}" "${rollback}/pki"
    PKI_EXISTED=1
  fi
  ROLLBACK_READY=1
}

restore_database() {
  local rollback="${WORK_DIR}/rollback"
  local name
  for name in "$(basename "${ACTIVE_DB}")" "$(basename "${ACTIVE_DB}")-wal" "$(basename "${ACTIVE_DB}")-shm"; do
    rm -f "$(dirname "${ACTIVE_DB}")/${name}"
    [[ ! -f "${rollback}/db/${name}" ]] || cp -a "${rollback}/db/${name}" "$(dirname "${ACTIVE_DB}")/${name}"
  done
}

rollback() {
  local exit_code=$?
  if [[ "${MIGRATION_DONE}" -eq 1 ]]; then
    [[ -z "${WORK_DIR}" ]] || rm -rf "${WORK_DIR}"
    return
  fi
  if [[ "${SWITCH_STARTED}" -eq 1 ]]; then
    echo "WARNING: Panel PKI 迁移失败，正在恢复原服务..." >&2
    systemctl stop "${SERVICE_NAME}" 2>/dev/null || true
    if [[ "${ROLLBACK_READY}" -eq 1 ]]; then
      [[ ! -f "${WORK_DIR}/rollback/ladder-panel" ]] ||
        install -m 0755 "${WORK_DIR}/rollback/ladder-panel" "${INSTALL_BIN}"
      [[ ! -f "${WORK_DIR}/rollback/panel.env" ]] ||
        install -m 0640 "${WORK_DIR}/rollback/panel.env" "${ENV_FILE}"
      [[ ! -f "${WORK_DIR}/rollback/ladder-panel.service" ]] ||
        install -m 0644 "${WORK_DIR}/rollback/ladder-panel.service" "${SERVICE_DST}"
      restore_database
      rm -rf "${ACTIVE_PKI_DIR}"
      if [[ "${PKI_EXISTED}" -eq 1 ]]; then
        cp -a "${WORK_DIR}/rollback/pki" "${ACTIVE_PKI_DIR}"
      fi
      systemctl daemon-reload || true
    fi
    systemctl restart "${SERVICE_NAME}" || true
  fi
  [[ -z "${WORK_DIR}" ]] || rm -rf "${WORK_DIR}"
  exit "${exit_code}"
}

if [[ "$(id -u)" -ne 0 ]]; then
  die "请使用 root 运行"
fi
for cmd in curl systemctl install grep runuser getent seq sleep; do
  need_cmd "${cmd}"
done
[[ -f "${ENV_FILE}" ]] || die "未找到 Panel 配置: ${ENV_FILE}"
[[ -f "${SERVICE_DST}" ]] || die "未找到 Panel systemd unit: ${SERVICE_DST}"
[[ -x "${INSTALL_BIN}" ]] || die "未找到 Panel 二进制: ${INSTALL_BIN}"
id -u "${USER_NAME}" >/dev/null 2>&1 || die "Panel 服务用户不存在: ${USER_NAME}"
getent group "${GROUP_NAME}" >/dev/null 2>&1 || die "Panel 服务组不存在: ${GROUP_NAME}"

ACTIVE_DB="${DB_OVERRIDE:-$(env_value LADDER_DB)}"
ACTIVE_DB="${ACTIVE_DB:-${DATA_DIR}/panel.db}"
ACTIVE_PKI_DIR="$(dirname "${ACTIVE_DB}")/pki"
validate_paths
[[ -f "${ACTIVE_DB}" ]] || die "未找到 Panel 数据库: ${ACTIVE_DB}"

WORK_DIR="$(mktemp -d /tmp/ladder-panel-pki-migration.XXXXXX)"
chmod 0755 "${WORK_DIR}"
trap rollback EXIT
PANEL_BINARY="$(resolve_panel_binary)"

echo "==> 停止 Panel 并创建本次执行的临时回滚"
SWITCH_STARTED=1
systemctl stop "${SERVICE_NAME}"
prepare_rollback

echo "==> 执行数据库与管理 CA 一次性迁移"
runuser -u "${USER_NAME}" -- "${PANEL_BINARY}" \
  -db "${ACTIVE_DB}" \
  -pki-dir "${ACTIVE_PKI_DIR}" \
  -migrate-management-pki

echo "==> 切换 Panel 二进制并启动"
install -m 0755 "${PANEL_BINARY}" "${INSTALL_BIN}"
systemctl daemon-reload
systemctl restart "${SERVICE_NAME}"

stable_checks=0
for _ in $(seq 1 20); do
  if systemctl is-active --quiet "${SERVICE_NAME}"; then
    stable_checks=$((stable_checks + 1))
    if [[ "${stable_checks}" -ge 3 ]]; then
      MIGRATION_DONE=1
      break
    fi
  else
    stable_checks=0
  fi
  sleep 1
done
[[ "${MIGRATION_DONE}" -eq 1 ]] || die "新 Panel 未能稳定运行"

# Successful migration deliberately keeps no previous binary or rollback.
rm -f "${INSTALL_BIN}.bak"
echo "======== Panel PKI 迁移完成 ========"
echo "  数据库: ${ACTIVE_DB}"
echo "  PKI:    ${ACTIVE_PKI_DIR}"
echo "  服务:   ${SERVICE_NAME}"
echo "  旧实现: 数据库旧 TLS 字段与旧二进制已删除"
echo "  必做:   备份 ${ACTIVE_PKI_DIR}/offline/root-ca.key 后转移到离线介质"
systemctl --no-pager --full status "${SERVICE_NAME}" || true
