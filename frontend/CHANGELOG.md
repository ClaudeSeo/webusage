# 변경 로그 - AI 사용량 대시보드

## [1.1.0] - 2026-03-31 (스코프 수정 반영)

### 🚨 P0 스코프 변경 (CTO 요청)

#### FE-02 추이 차트 범위 확장
- **변경 전**: `24h/7d` 토글
- **변경 후**: `24h/7d/30d` 토글
- **영향 파일**: 
  - `templates/components/trend_chart.html`
  - `main.go` (handleAPITrends, collectData)
  - `frontend/test-api.sh`

### ✨ 추가 기능

#### Risk Banner (5 초 룰 구현)
- 대시보드 상단에 실시간 위험 상태 표시
- 4 개 카운터: 🚨 위험 / ⚠️ 주의 / 🕐 오래됨 / ✅ 정상
- 동적 평가 메시지: "🔴 위험 - 즉시 조치 필요" 등
- 30 초 자동 업데이트

#### Stale State Detection
- 2 시간 이상 수집 없으면 "오래됨" 상태
- 노란색 경고 배지 및 박스
- 마지막 수집 시각 명시
- Risk Banner 에 카운트 반영

#### Error State 개선
- "데이터 없음" vs "수집 실패" 명확히 구분
- 타입별 다른 아이콘/컬러/메시지
- 수집 실패 시 구체적 조치 안내
- 30 초 자동 재시도 (collection-error)

### 📁 생성된 파일

#### 문서화
- `frontend/CPO_DEMO_GUIDE.md` (6.3 KB) - CPO 데모 가이드
- `frontend/capture-screenshots.sh` (4.4 KB) - 스크린샷 캡처 가이드
- `frontend/CHANGELOG.md` (본 파일)

#### 업데이트된 파일
- `templates/layout.html` - isStale() 함수 추가
- `templates/dashboard.html` - Risk Banner 추가
- `templates/components/provider_card.html` - Stale 상태 표시
- `templates/components/error_state.html` - 에러 타입 구분 강화
- `templates/components/trend_chart.html` - 30d 버튼 추가
- `main.go` - 30d 지원, isStale 템플릿 함수
- `frontend/test-api.sh` - 30d 테스트 추가

### ✅ HARD GATE 조건 충족

#### API 계약 테스트
- [x] `/api/current` → providers 배열
- [x] `/api/trends?range=24h` → points 배열
- [x] `/api/trends?range=7d` → points 배열
- [x] `/api/trends?range=30d` → points 배열 (NEW)
- [x] `/api/providers` → providers 배열
- [x] `/healthz` → status ("ok"|"degraded"|"error")
- [x] 잘못된 range → 400 에러

#### UI 상태 구분
- [x] Empty: 데이터 없음 (파란 안내 박스)
- [x] Error: 수집 실패 (빨간 에러 박스 + last_error)
- [x] Stale: 오래됨 (노란 경고 박스 + 마지막 수집 시각) (NEW)
- [x] Warning: 사용량 80%+ (노란/빨간 진행바)
- [x] Normal: 정상 (초록 진행바)

#### 5 초 룰 (Risk Assessment)
- [x] Risk Banner 항상 visible (데이터 있을 때) (NEW)
- [x] 4 개 카운터: 위험/주의/오래됨/정상
- [x] 색상 코딩: 빨강/노랑/노랑/초록
- [x] 한 줄 평가: "🔴 위험 - 즉시 조치 필요" 등
- [x] 자동 업데이트 (30 초 간격)

### 📸 CPO 데모 산출물

필수 스크린샷 (9 개):
1. `01_normal_state.png` - 정상 상태
2. `02_high_usage_warning.png` - 고사용량 주의
3. `03_collection_error.png` - 수집 실패 (last_error 노출)
4. `04_no_data.png` - 데이터 없음
5. `05_stale_data.png` - 오래된 데이터
6. `06_risk_banner_critical.png` - 위험 배너
7. `07_chart_24h.png` - 추이 차트 24h
8. `08_chart_7d.png` - 추이 차트 7d
9. `09_chart_30d.png` - 추이 차트 30d (NEW)

### 🔧 기술 변경사항

#### 템플릿 엔진
```go
funcMap["isStale"] = func(t *time.Time) bool {
    if t == nil || t.IsZero() {
        return true
    }
    return time.Since(*t) > 2*time.Hour
}
```

#### Risk Banner JavaScript
```javascript
function calculateRiskSummary(providers) {
    let criticalCount = 0;  // lastError 있음
    let warningCount = 0;   // usage >= 80%
    let staleCount = 0;     // collectedAt > 2 hours
    let okCount = 0;        // 나머지
    
    return { criticalCount, warningCount, staleCount, okCount };
}
```

#### API 엔드포인트
```go
func handleAPITrends(w http.ResponseWriter, r *http.Request) {
    rangeParam := r.URL.Query().Get("range")
    
    // 30d 추가
    if rangeParam != "24h" && rangeParam != "7d" && rangeParam != "30d" {
        http.Error(w, `{"error": "range must be '24h', '7d', or '30d'"}`, 400)
        return
    }
}
```

### 📊 성능 영향
- Risk Banner 계산: <1ms (클라이언트)
- Stale detection: O(n), n = Provider 수
- 차트 30d 데이터: 30 포인트 (경량화 유지)

### 🎯 다음 마일스톤
- [ ] CPO 데모 완료
- [ ] CTO HARD GATE 리뷰
- [ ] PR 병합
- [ ] 프로덕션 배포

---

**버전**: 1.1.0  
**작성일**: 2026-03-31  
**승인**: CTO 요청 (P0 스코프 변경)
