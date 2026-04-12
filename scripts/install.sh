#!/usr/bin/env bash
set -euo pipefail

REPO_URL="https://github.com/ClaudeSeo/webusage.git"
INSTALL_DIR="${WEBUSAGE_INSTALL_DIR:-$HOME/.local/share/webusage}"
DATA_DIR="${WEBUSAGE_HOME:-$HOME/.webusage}"
BIN_LINK="${WEBUSAGE_BIN:-$HOME/.local/bin/webusage-manage}"

_log()  { printf '\033[1m==> %s\033[0m\n' "$*"; }
_ok()   { printf '\033[32m  ✓ %s\033[0m\n' "$*"; }
_warn() { printf '\033[33m  ! %s\033[0m\n' "$*"; }
_die()  { printf '\033[31mERROR: %s\033[0m\n' "$*" >&2; exit 1; }

_log "webusage installer"

# 사전 요구사항 확인
command -v git  >/dev/null 2>&1 || _die "git 이 설치되어 있지 않습니다"
command -v go   >/dev/null 2>&1 || \
  command -v mise >/dev/null 2>&1 || \
  _die "Go 또는 mise 가 필요합니다. https://mise.jdx.dev 또는 https://go.dev/dl"

# Go 빌드 래퍼: mise 우선, 없으면 system go 사용
_go_build() {
  if command -v mise >/dev/null 2>&1; then
    mise exec -- go build "$@"
  else
    go build "$@"
  fi
}

# 저장소 클론 또는 업데이트
if [[ -d "$INSTALL_DIR/.git" ]]; then
  _log "기존 설치 업데이트 중: $INSTALL_DIR"
  git -C "$INSTALL_DIR" pull --ff-only
else
  _log "저장소 클론 중: $INSTALL_DIR"
  git clone "$REPO_URL" "$INSTALL_DIR"
fi

# 빌드
_log "빌드 중..."
(cd "$INSTALL_DIR" && _go_build -o webusage ./cmd/server)
_ok "빌드 완료: $INSTALL_DIR/webusage"

# 데이터 디렉터리 준비
mkdir -p -m 0700 "$DATA_DIR"
_ok "데이터 디렉터리: $DATA_DIR"

# webusage-manage 심링크 생성
mkdir -p "$(dirname "$BIN_LINK")"
ln -sf "$INSTALL_DIR/scripts/manage.sh" "$BIN_LINK"
_ok "명령어 등록: $BIN_LINK"

# PATH 안내
if ! echo "$PATH" | grep -q "$(dirname "$BIN_LINK")"; then
  _warn "$(dirname "$BIN_LINK") 이 PATH 에 없습니다. 아래를 ~/.zshrc 또는 ~/.bashrc 에 추가하세요:"
  printf '    export PATH="%s:$PATH"\n' "$(dirname "$BIN_LINK")"
fi

printf '\n'
_log "설치 완료!"
printf '\n'
printf '  시작:       %s start\n' "$BIN_LINK"
printf '  상태 확인:  %s status\n' "$BIN_LINK"
printf '  로그 보기:  %s logs\n' "$BIN_LINK"
printf '  업데이트:   %s update\n' "$BIN_LINK"
printf '\n'
printf '  설치 경로:  %s\n' "$INSTALL_DIR"
printf '  데이터 경로: %s\n' "$DATA_DIR"
printf '\n'

read -r -p "지금 바로 시작할까요? [y/N] " answer
if [[ "${answer,,}" == "y" ]]; then
  "$INSTALL_DIR/scripts/manage.sh" restart
fi
