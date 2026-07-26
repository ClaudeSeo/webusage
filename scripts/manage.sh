#!/usr/bin/env bash
set -euo pipefail

# Resolve REPO_DIR from the real script location even when invoked via symlink
_SCRIPT="${BASH_SOURCE[0]}"
while [[ -L "$_SCRIPT" ]]; do
  _SCRIPT="$(readlink "$_SCRIPT")"
  [[ "$_SCRIPT" == /* ]] || _SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$_SCRIPT"
done
REPO_DIR="$(cd "$(dirname "$_SCRIPT")/.." && pwd)"
unset _SCRIPT
DATA_DIR="${WEBUSAGE_HOME:-$HOME/.webusage}"
PID_FILE="$DATA_DIR/webusage.pid"
LOG_FILE="$DATA_DIR/webusage.log"
BINARY="$REPO_DIR/webusage"

# Priority: explicit env vars > .env > defaults
# Use a key-value parser instead of source: prevent shell command execution from .env
if [[ -f "$DATA_DIR/.env" ]]; then
  while IFS= read -r line; do
    [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
    [[ "$line" =~ ^([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]] || continue
    local_key="${BASH_REMATCH[1]}"
    local_val="${BASH_REMATCH[2]}"
    # Strip surrounding quotes
    if [[ "$local_val" =~ ^\"(.*)\"$ || "$local_val" =~ ^\'(.*)\'$ ]]; then
      local_val="${BASH_REMATCH[1]}"
    fi
    # Do not overwrite already-set env vars
    [[ "${!local_key+set}" == "set" ]] || export "${local_key}=${local_val}"
  done < "$DATA_DIR/.env"
fi

export DB_PATH="${DB_PATH:-$DATA_DIR/usage.db}"

_ensure_data_dir() {
  # Protect DB/PID/log from other local users
  mkdir -p -m 0700 "$DATA_DIR"
}

_safe_path() {
  # Prevent arbitrary file overwrite via symlink
  if [[ -L "$1" ]]; then
    echo "ERROR: 심링크 감지, 중단: $1"
    exit 1
  fi
}

_is_our_process() {
  local pid="$1"
  local running_name
  running_name="$(ps -p "$pid" -o comm= 2>/dev/null || true)"
  # macOS ps -o comm= may return a full path, so compare basenames
  [[ "$(basename "$running_name")" == "$(basename "$BINARY")" ]]
}

_kill_pid() {
  local pid="$1"
  echo "기존 프로세스 종료 (PID: $pid)..."
  kill "$pid"
  local deadline=$(( $(date +%s) + 5 ))
  until ! kill -0 "$pid" 2>/dev/null || [[ $(date +%s) -ge $deadline ]]; do
    sleep 0.1
  done
  if kill -0 "$pid" 2>/dev/null; then
    echo "SIGTERM 무응답, 강제 종료 (SIGKILL)..."
    kill -9 "$pid" 2>/dev/null || true
    sleep 0.2
  fi
}

_stop_existing() {
  local pid=""

  if [[ -f "$PID_FILE" ]]; then
    local file_pid
    file_pid=$(<"$PID_FILE")
    rm -f "$PID_FILE"
    if [[ "$file_pid" =~ ^[0-9]+$ ]] && kill -0 "$file_pid" 2>/dev/null && _is_our_process "$file_pid"; then
      pid="$file_pid"
    fi
  fi

  # If PID file is stale or missing, search for a running process by binary path
  if [[ -z "$pid" ]]; then
    pid="$(pgrep -f "$BINARY" 2>/dev/null | head -1 || true)"
  fi

  if [[ -n "$pid" ]]; then
    _kill_pid "$pid"
  fi
}

_build() {
  echo "빌드 중..."
  cd "$REPO_DIR"
  mise exec -- go build -o webusage ./cmd/server
  echo "빌드 완료: $BINARY"
}

_start_background() {
  _ensure_data_dir
  _safe_path "$LOG_FILE"
  _safe_path "$PID_FILE"
  # Run from REPO_DIR since config.LoadConfig() calls godotenv.Load() relative to cwd
  cd "$REPO_DIR"
  # Set DB_PATH explicitly right before nohup: avoid shell inheritance issues and .env override
  export DB_PATH="$DATA_DIR/usage.db"
  echo "백그라운드로 실행 중 (데이터: $DATA_DIR)..."
  nohup "$BINARY" >> "$LOG_FILE" 2>&1 &
  local pid=$!
  echo "$pid" > "$PID_FILE"

  # nohup always succeeds, so check for immediate crash separately
  sleep 0.5
  if ! kill -0 "$pid" 2>/dev/null; then
    echo "ERROR: 프로세스 시작 실패. 로그 확인: $LOG_FILE"
    rm -f "$PID_FILE"
    tail -5 "$LOG_FILE" 2>/dev/null
    return 1
  fi

  echo "실행됨 (PID: $pid)"
  echo "  로그: $LOG_FILE"
  echo "  DB:   $DB_PATH"
}

_pull_build_start() {
  # Stop the service only after a successful build: avoid downtime on build failure
  cd "$REPO_DIR" && git pull --ff-only
  _build
  _stop_existing
  _start_background
}

cmd_start() {
  _pull_build_start
}

cmd_stop() {
  _stop_existing
  echo "중지됨"
}

cmd_restart() {
  if [[ ! -x "$BINARY" ]]; then
    echo "ERROR: 바이너리 없음: $BINARY (먼저 start 또는 update 실행)"
    exit 1
  fi
  _stop_existing
  _start_background
}

cmd_status() {
  if [[ -f "$PID_FILE" ]]; then
    local pid
    pid=$(<"$PID_FILE")
    if kill -0 "$pid" 2>/dev/null && _is_our_process "$pid"; then
      echo "실행 중 (PID: $pid)"
      echo "  로그: $LOG_FILE"
      echo "  DB:   $DATA_DIR/usage.db"
      return 0
    fi
  fi
  # If PID file is missing or stale, search directly by binary path
  local found_pid
  found_pid="$(pgrep -f "$BINARY" 2>/dev/null | head -1 || true)"
  if [[ -n "$found_pid" ]]; then
    echo "실행 중 (PID: $found_pid, PID 파일 불일치 — restart 권장)"
    echo "  로그: $LOG_FILE"
    echo "  DB:   $DB_PATH"
    return 0
  fi
  echo "중지됨"
  return 1
}

cmd_logs() {
  if [[ ! -f "$LOG_FILE" ]]; then
    echo "로그 파일 없음: $LOG_FILE"
    exit 1
  fi
  tail -f "$LOG_FILE"
}

cmd_update() {
  _pull_build_start
}

usage() {
  echo "Usage: $(basename "$0") {start|stop|restart|status|logs|update}"
  echo ""
  echo "  start   - git pull + 빌드 + 백그라운드 실행 (기본값)"
  echo "  stop    - 실행 중인 프로세스 종료"
  echo "  restart - 재시작 (빌드 없이)"
  echo "  status  - 실행 상태 확인"
  echo "  logs    - 로그 실시간 출력 (tail -f)"
  echo "  update  - git pull + 재빌드 + 재시작"
  echo ""
  echo "환경변수:"
  echo "  WEBUSAGE_HOME  데이터 디렉터리 (기본값: ~/.webusage)"
  echo "  DB_PATH        SQLite 경로    (기본값: \$WEBUSAGE_HOME/usage.db)"
  echo "  .env 파일:     \$WEBUSAGE_HOME/.env 에 환경변수 저장 가능"
}

case "${1:-start}" in
  start)   cmd_start   ;;
  stop)    cmd_stop    ;;
  restart) cmd_restart ;;
  status)  cmd_status  ;;
  logs)    cmd_logs    ;;
  update)  cmd_update  ;;
  *)       usage; exit 1 ;;
esac
