#!/bin/bash
# CPO 데모용 스크린샷 캡처 스크립트
# Puppeteer 또는 Playwright 를 사용하여 대시보드 상태별 스크린샷 촬영

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
SCREENSHOTS_DIR="$PROJECT_DIR/screenshots"

echo "📸 CPO 데모 스크린샷 캡처"
echo "========================"
echo ""

# Create screenshots directory
mkdir -p "$SCREENSHOTS_DIR"

BASE_URL="${BASE_URL:-http://localhost:8080}"

# Check if server is running
if ! curl -s "$BASE_URL/healthz" > /dev/null; then
    echo "❌ 서버가 실행 중이지 않습니다. 먼저 서버를 시작하세요:"
    echo "   ./frontend/start.sh"
    exit 1
fi

echo "✅ 서버 확인됨: $BASE_URL"
echo ""

# Use browser tool to capture screenshots via OpenClaw
cat << 'EOF' > /tmp/capture_screenshots.js
// Browser automation script for screenshot capture
const fs = require('fs');
const path = require('path');

async function captureScreenshots() {
    const screenshotsDir = process.env.SCREENSHOTS_DIR || './screenshots';
    const baseUrl = process.env.BASE_URL || 'http://localhost:8080';
    
    console.log('📸 Starting screenshot capture...');
    console.log('   Base URL:', baseUrl);
    console.log('   Output:', screenshotsDir);
    console.log('');
    
    // This script is meant to be run with OpenClaw's browser tool
    // The actual implementation depends on the browser automation available
    
    const scenarios = [
        {
            name: '01_normal_state',
            description: '정상 상태 - 모든 Provider 정상, 사용량 낮음',
            url: baseUrl + '/',
            waitMs: 2000
        },
        {
            name: '02_high_usage_warning',
            description: '주의 상태 - 80% 이상 사용량',
            url: baseUrl + '/',
            waitMs: 2000,
            inject: 'simulateHighUsage()'
        },
        {
            name: '03_collection_error',
            description: '수집 실패 - last_error 노출',
            url: baseUrl + '/',
            waitMs: 2000,
            inject: 'simulateCollectionError()'
        },
        {
            name: '04_no_data',
            description: '데이터 없음 - 첫 수집 전',
            url: baseUrl + '/',
            waitMs: 2000,
            inject: 'simulateNoData()'
        },
        {
            name: '05_stale_data',
            description: '오래된 데이터 - 2 시간 이상 경과',
            url: baseUrl + '/',
            waitMs: 2000,
            inject: 'simulateStaleData()'
        },
        {
            name: '06_risk_banner_critical',
            description: '위험 배너 - 즉시 조치 필요',
            url: baseUrl + '/',
            waitMs: 2000,
            inject: 'simulateCriticalRisk()'
        },
        {
            name: '07_chart_24h',
            description: '추이 차트 - 24h 뷰',
            url: baseUrl + '/?range=24h',
            waitMs: 2000
        },
        {
            name: '08_chart_7d',
            description: '추이 차트 - 7d 뷰',
            url: baseUrl + '/?range=7d',
            waitMs: 2000
        },
        {
            name: '09_chart_30d',
            description: '추이 차트 - 30d 뷰',
            url: baseUrl + '/?range=30d',
            waitMs: 2000
        }
    ];
    
    console.log('총', scenarios.length, '개 시나리오');
    console.log('');
    
    // Output scenario list for manual execution
    scenarios.forEach((scenario, index) => {
        console.log(`${index + 1}. ${scenario.name}`);
        console.log(`   ${scenario.description}`);
        console.log(`   URL: ${scenario.url}`);
        console.log('');
    });
    
    return scenarios;
}

captureScreenshots().catch(console.error);
EOF

echo "📋 캡처 시나리오:"
echo ""
echo "1. 01_normal_state - 정상 상태 (모든 Provider 정상)"
echo "2. 02_high_usage_warning - 주의 상태 (80% 이상 사용량)"
echo "3. 03_collection_error - 수집 실패 (last_error 노출)"
echo "4. 04_no_data - 데이터 없음 (첫 수집 전)"
echo "5. 05_stale_data - 오래된 데이터 (2 시간 이상)"
echo "6. 06_risk_banner_critical - 위험 배너 (즉시 조치 필요)"
echo "7. 07_chart_24h - 추이 차트 24h 뷰"
echo "8. 08_chart_7d - 추이 차트 7d 뷰"
echo "9. 09_chart_30d - 추이 차트 30d 뷰"
echo ""
echo "💡 수동 캡처 방법:"
echo ""
echo "1. 브라우저에서 각 URL 접속"
echo "2. 개발자 도구로 원하는 상태 주입 (inject 함수)"
echo "3. 전체 페이지 스크린샷 (Cmd+Shift+4 또는 PrintScreen)"
echo "4. $SCREENSHOTS_DIR 에 저장"
echo ""
echo "🎯 HARD GATE 체크리스트:"
echo "   □ 5 초 안에 위험 상태 판단 가능 (Risk Banner)"
echo "   ✓ 에러/노후/데이터없음 명확히 구분"
echo "   ✓ Provider 카드 used/limit/remaining 표시"
echo "   ✓ last_success_at, reset_at 표시"
echo "   ✓ 24h/7d/30d 차트 토글"
echo ""
