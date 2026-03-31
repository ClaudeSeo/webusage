# AI Usage Dashboard - Implementation Summary

## 완료 기준 (DoD) 체크리스트 - HARD GATE 포함

### ✅ 1. 패키지 구조 준수
```
internal/provider/     # Provider 어댑터 (OpenAI, Claude)
internal/collector/    # 수집 스케줄러 (lock, retry, backoff + jitter)
internal/store/        # SQLite CRUD 레이어 (멱등성 지원)
internal/stats/        # 24h/7d/30d 집계 로직
internal/http/         # REST API 엔드포인트 + 계약 테스트
cmd/server/            # 메인 진입점
```

**파일 목록:**
- `internal/provider/interface.go` - Provider 인터페이스 정의
- `internal/provider/types.go` - 공통 타입
- `internal/provider/openai.go` - OpenAI Provider 구현
- `internal/provider/anthropic.go` - Anthropic Provider 구현
- `internal/collector/collector.go` - 스케줄러 (lock/retry/backoff+jitter/timeout)
- `internal/store/store.go` - SQLite 연결 및 스키마 (WAL + busy_timeout)
- `internal/store/providers.go` - Provider CRUD
- `internal/store/usage.go` - UsageSnapshot CRUD (멱등성 지원)
- `internal/stats/stats.go` - 통계 집계 (24h/7d/30d)
- `internal/http/server.go` - HTTP 서버 + SSR 템플릿
- `internal/http/api_contract_test.go` - API 계약 테스트 (JSON schema)
- `cmd/server/main.go` - 메인 진입점

### ✅ 2. DB 스키마 필수 필드 모두 포함
```sql
CREATE TABLE usage_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id INTEGER NOT NULL,
    metric TEXT NOT NULL,
    used REAL NOT NULL,           -- ✅ 사용량 (명확한 이름)
    "limit" REAL,                 -- ✅ 한도
    reset_at DATETIME,            -- ✅ 리셋 시각
    collected_at DATETIME NOT NULL, -- ✅ 수집 시각
    raw_json TEXT,                -- ✅ 원본 응답 보존
    FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
);
```

### ✅ 3. Provider 2 개 구현 + 테스트
- **OpenAI Provider**: `internal/provider/openai.go`
- **Anthropic Provider**: `internal/provider/anthropic.go`
- **테스트**: 8 개 테스트 모두 통과

### ✅ 4. HARD GATE: 스케줄러 안정성
`internal/collector/collector.go`:

| 요구사항 | 구현 | 위치 |
|---------|------|------|
| **Job lock** | `sync.Map` 기반 중복 실행 방지 | `tryLock()`, `unlock()` |
| **Retry** | 최대 3 회 재시도 | `maxRetries = 3` |
| **Exponential Backoff** | `base * 2^attempt` | `calculateBackoffWithJitter()` |
| **Jitter** | `random(0, backoff*0.5)` | crypto/rand 사용 |
| **Timeout** | job 당 60 초 제한 | `context.WithTimeout()` |
| **Context 취소** | 대기 중 취소 지원 | `select { <-time.After, <-ctx.Done }` |
| **last_error 저장** | DB 에 오류 기록 | `UpdateProviderStatus()` |
| **멱등성** | 동일 시점 중복 insert 방지 | `CreateUsageSnapshotsIdempotent()` |

### ✅ 5. API 4 개 엔드포인트 + 30d 범위 + 유효성 검증
| 엔드포인트 | 설명 | 테스트 |
|-----------|------|--------|
| `GET /` | 대시보드 UI (SSR) | ✅ |
| `GET /api/current` | provider 별 최신 사용량 | ✅ |
| `GET /api/trends?range=24h\|7d\|30d` | 시계열 추이 (30d 추가) | ✅ |
| `GET /api/providers` | 등록된 provider 목록 | ✅ |
| `GET /healthz` | 헬스체크 | ✅ |

**범위 유효성 검증:**
- 허용값: `24h`, `7d`, `30d`
-_invalid 값:_ HTTP 400 응답 + 오류 메시지

**계약 테스트:** `internal/http/api_contract_test.go`
- `TestAPIContract_Healthz` - 헬스체크 스키마 검증
- `TestAPIContract_Current` - 현재 사용량 스키마 검증
- `TestAPIContract_Trends` - 24h/7d/30d 모든 범위 테스트
- `TestAPIContract_Trends_InvalidRange` - invalid range → 400 검증
- `TestAPIContract_Providers` - provider 목록 스키마 검증
- `TestAPIContract_ErrorResponses` - 오류 응답 형식 검증

### ✅ 6. HARD GATE: 운영 설정 필수
- **SQLite WAL 모드**: `PRAGMA journal_mode=WAL` ✅
- **busy_timeout=5000**: `PRAGMA busy_timeout=5000` ✅
- **.env 로딩**: `github.com/joho/godotenv` ✅
- **Structured logging**: `log/slog` JSON 핸들러 ✅

### ✅ 7. 단일 바이너리 빌드 성공
```bash
$ go build -o ai-usage-dashboard ./cmd/server
$ ls -lh ai-usage-dashboard
-rwx------  1 claudeseo-mini  staff   16M Mar 31 18:00 ai-usage-dashboard
```

## 테스트 결과
```
ok    github.com/openclaw/ai-usage-dashboard/internal/http      0.542s  (12 tests)
ok    github.com/openclaw/ai-usage-dashboard/internal/provider  0.336s  (8 tests)
ok    github.com/openclaw/ai-usage-dashboard/internal/store     0.294s  (7 tests)
```

**총 27 개 테스트 모두 통과**

## HARD GATE 체크리스트 (CTO 리뷰용)

- [x] SQLite WAL + busy_timeout=5000
- [x] Job lock (중복실행방지)
- [x] Retry: 지수백오프 + jitter + timeout/context 취소
- [x] last_error 상태 저장 및 노출
- [x] 멱등성 보장 (동일 시점 중복 insert 방지)
- [x] API 계약 테스트 1 회 (JSON schema 기반 6 개 테스트)
- [x] BE-06 stats 범위: 24h/7d/30d
- [x] BE-04 API 범위: 30d 추가 + 유효성 검증 (400 응답)

## 빠른 시작
```bash
cd ai-usage-dashboard
cp .env.example .env
# .env 파일에 API 키 입력

make build
./ai-usage-dashboard
```

서버가 `http://localhost:8080` 에서 시작됩니다.

## API 예시

### GET /api/trends?range=30d
```json
{
  "openai": {
    "provider_id": 1,
    "range": "30d",
    "trend": [
      {"timestamp": "2026-03-01T00:00:00Z", "value": 1000, "metric": "tokens"},
      {"timestamp": "2026-03-02T00:00:00Z", "value": 1500, "metric": "tokens"}
    ]
  }
}
```

### Invalid Range → 400
```bash
$ curl "http://localhost:8080/api/trends?range=60d"
{"error":"Invalid range '60d'. Valid values: 24h, 7d, 30d"}
```
