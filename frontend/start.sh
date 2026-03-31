#!/bin/bash
# AI 사용량 대시보드 빠른 시작 스크립트

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

echo "🚀 AI 사용량 대시보드 서버를 시작합니다..."
echo ""

cd "$PROJECT_DIR"

# 빌드 확인
if [ ! -f "ai-usage-dashboard" ]; then
    echo "📦 서버를 빌드합니다..."
    go build -ldflags="-s -w" -o ai-usage-dashboard .
fi

# 포트 확인
PORT="${PORT:-8080}"
echo "✅ 서버 바이너리 준비 완료"
echo "🌐 포트: $PORT"
echo "📊 URL: http://localhost:$PORT"
echo ""
echo "🛑 중료: Ctrl+C"
echo ""

# 서버 시작
./ai-usage-dashboard
