# AI 사용량 대시보드 프론트엔드

Go 기반 SSR 템플릿 + Chart.js 를 사용한 실시간 AI API 사용량 모니터링 대시보드입니다.

## 📁 프로젝트 구조

```
workspace-pmo/
├── main.go                          # Go 서버 (SSR + API)
├── templates/
│   ├── layout.html                  # 공통 레이아웃 (헤더, 스타일, 유틸리티)
│   ├── dashboard.html               # 메인 대시보드 페이지
│   └── components/
│       ├── provider_card.html       # Provider 별 사용량 카드
│       ├── trend_chart.html         # Chart.js 추이 차트
│       └── error_state.html         # 에러/데이터 없음 상태
└── frontend/
    └── README.md                    # 이 파일
```

## 🚀 빠른 시작

### 1. 서버 빌드 및 실행

```bash
cd /Users/claudeseo-mini/.openclaw/workspace-pmo
go run main.go
```

또는 프로덕션 빌드:

```bash
go build -o ai-usage-dashboard
./ai-usage-dashboard
```

### 2. 브라우저에서 확인

```
http://localhost:8080
```

## 📊 API 엔드포인트

### GET /api/current
현재 사용량 데이터 반환
```json
{
  "providers": [
    {
      "id": "openai",
      "name": "OpenAI",
      "used": 45000,
      "limit": 100000,
      "remaining": 55000,
      "resetAt": "2026-04-01T00:00:00Z",
      "collectedAt": "2026-03-31T17:00:00Z",
      "lastError": "",
      "updatedAt": "2026-03-31T17:00:00Z"
    }
  ]
}
```

### GET /api/trends?range=24h|7d|30d
사용량 추이 데이터 반환
```json
{
  "points": [
    {
      "collectedAt": "2026-03-31T17:00:00Z",
      "providerId": "openai",
      "metric": "usage",
      "used": 45000,
      "limit": 100000
    }
  ]
}
```

### GET /api/providers
등록된 Provider 목록 반환
```json
{
  "providers": [
    {"id": "openai", "name": "OpenAI", "enabled": true}
  ]
}
```

### GET /healthz
서버 상태 확인
```json
{"status": "ok"}
// 또는 {"status": "degraded"} | {"status": "error"}
```

## 🎨 기능

### FE-01: Provider 카드
- ✅ Provider 명
- ✅ 현재 사용량 / 한도 / 잔여량
- ✅ 사용률 진행바 (색상 코딩)
- ✅ 마지막 수집 시각
- ✅ 다음 리셋 시각
- ✅ 에러 상태 표시

### FE-02: 추이 차트
- ✅ 24h / 7d / 30d 토글 버튼
- ✅ Chart.js Line chart
- ✅ Provider 별 색상 구분
- ✅ Y 축: 사용률 (%)
- ✅ X 축: 시간 (상대적)
- ✅ 반응형 툴팁

### FE-03: 에러 상태 UI
- ✅ "데이터 없음" 상태
- ✅ "수집 실패" 상태 (last_error 노출)
- ✅ 재시도 버튼
- ✅ 명확한 컬러 코딩
- ✅ 30 초 자동 재시도 (collection-error)

## 🔧 환경 변수

| 변수명 | 설명 | 기본값 |
|--------|------|--------|
| PORT | 서버 포트 | 8080 |

## 📝 백엔드 통합 가이드

### 1. 데이터 콜렉터 구현

`main.go` 의 `collectData()` 함수를 실제 API 호출로 교체:

```go
func collectData() {
    currentState.Lock()
    defer currentState.Unlock()
    
    // 실제 Provider API 에서 데이터 수집
    providers, err := fetchFromProviderAPIs()
    if err != nil {
        currentState.LastError = err
        return
    }
    
    currentState.Providers = providers
    currentState.Trends["24h"] = fetchTrends("24h")
    currentState.Trends["7d"] = fetchTrends("7d")
    currentState.LastError = nil
}
```

### 2. 데이터 모델 확장

필요에 따라 `Provider` 구조체 확장:

```go
type Provider struct {
    ID          string     `json:"id"`
    Name        string     `json:"name"`
    Used        int64      `json:"used"`
    Limit       int64      `json:"limit"`
    Remaining   int64      `json:"remaining"`
    ResetAt     time.Time  `json:"resetAt"`
    CollectedAt *time.Time `json:"collectedAt,omitempty"`
    LastError   string     `json:"lastError,omitempty"`
    UpdatedAt   time.Time  `json:"updatedAt"`
    // 추가 필드...
    Endpoint    string     `json:"endpoint,omitempty"`
    Region      string     `json:"region,omitempty"`
}
```

## 🎯 완료 기준 (DoD)

- [x] SSR 템플릿 구조 완성
- [x] Provider 카드 컴포넌트 구현
- [x] Chart.js 연동 (24h/7d 토글)
- [x] 에러/데이터 없음 상태 처리
- [x] API 연동 및 데이터 바인딩
- [x] 반응형 레이아웃 (모바일 고려)
- [x] Go 템플릿 빌드 통합

## 📱 반응형 디자인

- 데스크톱: 그리드 레이아웃 (자동 조절)
- 태블릿: 2 컬럼
- 모바일: 단일 컬럼
- 차트: 자동 크기 조절

## 🔒 보안 고려사항

- Tailscale 내부 접근 전제 (public 보안 불필요)
- CORS 는 동일 출처 정책 적용
- 프로덕션 배포 시 HTTPS 권장

## 🧪 테스트 데이터

서버 시작 시 샘플 데이터가 자동으로 생성됩니다. 실제 연동 시에는 `collectData()` 함수를 수정하세요.

---

**마지막 업데이트**: 2026-03-31  
**버전**: 1.0.0
