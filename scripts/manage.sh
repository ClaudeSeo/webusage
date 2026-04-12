#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA_DIR="${WEBUSAGE_HOME:-$HOME/.webusage}"
PID_FILE="$DATA_DIR/webusage.pid"
LOG_FILE="$DATA_DIR/webusage.log"
BINARY="$REPO_DIR/webusage"

# 명시적 환경변수 > .env > 기본값 순으로 우선순위 적용
# source 대신 key-value 파서 사용: .env 내 셸 명령어 실행 방지
if [[ -f "$DATA_DIR/.env" ]]; then
  while IFS= read -r line; do
    [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
    [[ "$line" =~ ^([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]] || continue
    local_key="${BASH_REMATCH[1]}"
    local_val="${BASH_REMATCH[2]}"
    # 앞뒤 따옴표 제거
    if [[ "$local_val" =~ ^\"(.*)\"$ || "$local_val" =~ ^\'(.*)\'$ ]]; then
      local_val="${BASH_REMATCH[1]}"
    fi
    # 이미 설정된 환경변수는 덮어쓰지 않음
    [[ "${!local_key+set}" == "set" ]] || export "${local_key}=${local_val}"
  done < "$DATA_DIR/.env"
fi

export DB_PATH="${DB_PATH:-$DATA_DIR/usage.db}"

_ensure_data_dir() {
  # 다른 로컬 사용자로부터 DB·PID·로그 보호
  mkdir -p -m 0700 "$DATA_DIR"
}

_safe_path() {
  # 심링크를 통한 임의 파일 덮어쓰기 방지
  if [[ -L "$1" ]]; then
    echo "ERROR: 심링크 감지, 중단: $1"
    exit 1
  fi
}

_is_our_process() {
  local pid="$1"
  local running_name
  running_name="$(ps -p "$pid" -o comm= 2>/dev/null || true)"
  [[ "$running_name" == "$(basename "$BINARY")" ]]
}

_stop_existing() {
  if [[ -f "$PID_FILE" ]]; then
    local pid
    pid=$(<"$PID_FILE")
    # 숫자가 아닌 PID 파일은 오염된 것으로 간주
    if [[ ! "$pid" =~ ^[0-9]+$ ]]; then
      echo "잘못된 PID 파일 내용, 삭제합니다: $PID_FILE"
      rm -f "$PID_FILE"
      return
    fi
    if kill -0 "$pid" 2>/dev/null; then
      # PID 재사용 방지: 실행 중인 프로세스가 실제로 webusage 인지 확인
      if ! _is_our_process "$pid"; then
        echo "PID $pid 는 webusage 프로세스가 아님, PID 파일만 삭제합니다"
        rm -f "$PID_FILE"
        return
      fi
      echo "기존 프로세스 종료 (PID: $pid)..."
      kill "$pid"
      # kill -0 이 실패할 때까지 최대 5초 대기
      local deadline=$(( $(date +%s) + 5 ))
      until ! kill -0 "$pid" 2>/dev/null || [[ $(date +%s) -ge $deadline ]]; do
        sleep 0.1
      done
      # SIGTERM 무응답 시 SIGKILL 강제 종료
      if kill -0 "$pid" 2>/dev/null; then
        echo "SIGTERM 무응답, 강제 종료 (SIGKILL)..."
        kill -9 "$pid" 2>/dev/null || true
        sleep 0.2
      fi
    fi
    rm -f "$PID_FILE"
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
  # config.LoadConfig()가 godotenv.Load()를 cwd 기준으로 호출하므로 REPO_DIR 에서 실행
  cd "$REPO_DIR"
  echo "백그라운드로 실행 중 (데이터: $DATA_DIR)..."
  nohup "$BINARY" >> "$LOG_FILE" 2>&1 &
  local pid=$!
  echo "$pid" > "$PID_FILE"

  # nohup은 항상 성공하므로 즉시 crash 여부를 별도 확인
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
  # 빌드 성공 후 서비스 중단: 빌드 실패 시 기존 서비스 다운타임 방지
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
      echo "  DB:   $DB_PATH"
      return 0
    fi
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
