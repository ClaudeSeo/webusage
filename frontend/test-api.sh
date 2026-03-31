#!/bin/bash
# AI 사용량 대시보드 API 계약 테스트 스크립트

BASE_URL="${BASE_URL:-http://localhost:8080}"

echo "🧪 AI 사용량 대시보드 API 계약 테스트"
echo "======================================"
echo "Base URL: $BASE_URL"
echo ""

# 색상 정의
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

pass_count=0
fail_count=0

test_api() {
    local name="$1"
    local endpoint="$2"
    local expected_status="$3"
    local check_json_key="$4"
    
    echo -n "테스트: $name ... "
    
    response=$(curl -s -w "\n%{http_code}" "$BASE_URL$endpoint")
    body=$(echo "$response" | head -n -1)
    status=$(echo "$response" | tail -n 1)
    
    if [ "$status" != "$expected_status" ]; then
        echo -e "${RED}실패${NC} (HTTP $status, 예상: $expected_status)"
        echo "  응답: $body"
        ((fail_count++))
        return 1
    fi
    
    if [ -n "$check_json_key" ]; then
        if ! echo "$body" | jq -e ".$check_json_key" > /dev/null 2>&1; then
            echo -e "${RED}실패${NC} (JSON 키 '$check_json_key' 없음)"
            echo "  응답: $body"
            ((fail_count++))
            return 1
        fi
    fi
    
    echo -e "${GREEN}통과${NC}"
    ((pass_count++))
    return 0
}

echo "1️⃣  헬스체크 엔드포인트"
test_api "GET /healthz" "/healthz" "200" "status"

echo ""
echo "2️⃣  현재 사용량 API"
test_api "GET /api/current" "/api/current" "200" "providers"

echo ""
echo "3️⃣  추이 데이터 API (24h)"
test_api "GET /api/trends?range=24h" "/api/trends?range=24h" "200" "points"

echo ""
echo "4️⃣  추이 데이터 API (7d)"
test_api "GET /api/trends?range=7d" "/api/trends?range=7d" "200" "points"

echo ""
echo "5️⃣  추이 데이터 API (30d)"
test_api "GET /api/trends?range=30d" "/api/trends?range=30d" "200" "points"

echo ""
echo "6️⃣  Provider 목록 API"
test_api "GET /api/providers" "/api/providers" "200" "providers"

echo ""
echo "7️⃣  대시보드 페이지 (SSR)"
test_api "GET /" "/" "200" ""

echo ""
echo "8️⃣  잘못된 range 파라미터"
response=$(curl -s -w "\n%{http_code}" "$BASE_URL/api/trends?range=invalid")
status=$(echo "$response" | tail -n 1)
if [ "$status" = "400" ]; then
    echo -e "테스트: 잘못된 range 파라미터 처리 ... ${GREEN}통과${NC}"
    ((pass_count++))
else
    echo -e "테스트: 잘못된 range 파라미터 처리 ... ${RED}실패${NC} (HTTP $status)"
    ((fail_count++))
fi

echo ""
echo "======================================"
echo "결과: ${GREEN}$pass_count 통과${NC}, ${RED}$fail_count 실패${NC}"

if [ $fail_count -eq 0 ]; then
    echo -e "${GREEN}✅ 모든 API 계약 테스트가 통과되었습니다!${NC}"
    exit 0
else
    echo -e "${RED}❌ 일부 테스트가 실패했습니다.${NC}"
    exit 1
fi
