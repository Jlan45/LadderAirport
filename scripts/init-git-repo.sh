#!/usr/bin/env bash
# Prepare / re-create the LadderAirport git repository.
#
# Default: ensure repo is healthy, stage all, show status (keeps history).
# Fresh single commit (optional):
#   FRESH=1 ./scripts/init-git-repo.sh
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

NAME="${GIT_AUTHOR_NAME:-LadderAirport}"
EMAIL="${GIT_AUTHOR_EMAIL:-ladder@localhost}"

echo "==> project: ${ROOT}"

if [[ "${FRESH:-0}" == "1" ]]; then
  echo "==> FRESH=1: remove .git and create new repository"
  rm -rf .git
fi

if [[ ! -d .git ]]; then
  echo "==> git init"
  git init -b main
else
  echo "==> existing git repo"
  # rename master -> main if needed
  cur="$(git branch --show-current 2>/dev/null || true)"
  if [[ "${cur}" == "master" ]]; then
    git branch -m main 2>/dev/null || true
  fi
fi

# Ensure submodule is registered if sing-box present
if [[ -d agent/sing-box/.git ]] || [[ -f agent/sing-box/go.mod ]]; then
  if [[ ! -f .gitmodules ]] || ! grep -q 'agent/sing-box' .gitmodules 2>/dev/null; then
    echo "==> ensure .gitmodules for agent/sing-box"
    cat > .gitmodules <<'EOF'
[submodule "agent/sing-box"]
	path = agent/sing-box
	url = https://github.com/SagerNet/sing-box.git
EOF
  fi
fi

echo "==> git add"
git add -A

# Don't force-add secrets
git reset -- data/ 2>/dev/null || true
git reset -- '*.db' 2>/dev/null || true

echo "==> status"
git status -sb

if git diff --cached --quiet 2>/dev/null && git rev-parse HEAD >/dev/null 2>&1; then
  echo "==> nothing new to commit"
else
  if ! git rev-parse HEAD >/dev/null 2>&1 || [[ "${FRESH:-0}" == "1" ]]; then
    echo "==> initial commit"
    git -c user.name="${NAME}" -c user.email="${EMAIL}" commit -m "$(cat <<'EOF'
chore: initial LadderAirport repository

Panel (Go + embedded React), ladder-agent (sing-box), gRPC control,
subscriptions (Clash/sing-box), and deploy scripts.
EOF
)"
  else
    if ! git diff --cached --quiet; then
      echo "==> commit pending changes"
      git -c user.name="${NAME}" -c user.email="${EMAIL}" commit -m "chore: sync repository tree"
    fi
  fi
fi

echo
echo "======== Git 仓库就绪 ========"
git log --oneline -5 2>/dev/null || true
echo
echo "当前分支: $(git branch --show-current 2>/dev/null || echo '?')"
echo "根目录:   ${ROOT}"
echo
echo "绑定远程并推送示例:"
echo "  git remote add origin git@github.com:<you>/LadderAirport.git"
echo "  git push -u origin main"
echo
echo "若要清空历史重来: FRESH=1 ./scripts/init-git-repo.sh"
