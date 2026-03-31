# AI Usage Dashboard

OpenUsage 스타일의 AI 사용량 관제 서버 백엔드 (Go + SQLite)

## 기술 스택

- **Language**: Go 1.21+
- **Database**: SQLite (WAL 모드, busy_timeout=5000)
- **Web**: Go html/template (SSR) + REST API
- **Scheduling**: Internal cron/job runner

## 패키지 구조

```
internal/provider/     # Provider 어댑터 (OpenAI, Claude)
internal/collector/    # 수집 스케줄러 (lock, retry, backoff)
internal/store/        # SQLite CRUD 레이어
internal/stats/        # 24h/7d 집계 로직
internal/http/         # REST API 엔드포인트
cmd/server/            # 메인 진입점
```

## 설치 및 실행

### 1. 환경 설정

```bash
cd ai-usage-dashboard
cp .env.example .env
```

`.env` 파일에 API 키를 입력하세요:

```env
DB_PATH=./data/usage.db
SERVER_PORT=8080
OPENAI_API_KEY=sk-your-openai-key-here
ANTHROPIC_API_KEY=sk-ant-your-anthropic-key-here
COLLECTION_INTERVAL=300
```

### 2. 의존성 설치

```bash
go mod download
```

### 3. 빌드

```bash
go build -o ai-usage-dashboard ./cmd/server
```

### 4. 실행

```bash
./ai-usage-dashboard
```

서버가 `http://localhost:8080` 에서 시작됩니다.

## API 엔드포인트

| 엔드포인트 | 설명 |
|-----------|------|
| `GET /` | 대시보드 UI (SSR) |
| `GET /api/current` | provider 별 최신 사용량 |
| `GET /api/trends?range=24h\|7d` | 시계열 추이 |
| `GET /api/providers` | 등록된 provider 목록 |
| `GET /healthz` | 헬스체크 |

### API 응답 예시

#### GET /api/current

```json
{
  "openai": {
    "provider_id": 1,
    "enabled": true,
    "metrics": {
      "tokens": 125000,
      "requests": 450
    },
    "last_run": "2026-03-31T17:00:00Z",
    "last_error": null
  },
  "anthropic": {
    "provider_id": 2,
    "enabled": true,
    "metrics": {
      "info": 0
    },
    "last_run": "2026-03-31T17:00:00Z",
    "last_error": null
  }
}
```

#### GET /api/trends?range=24h

```json
{
  "openai": {
    "provider_id": 1,
    "range": "24h",
    "trend": [
      {"timestamp": "2026-03-30T18:00:00Z", "value": 5000, "metric": "tokens"},
      {"timestamp": "2026-03-30T19:00:00Z", "value": 6200, "metric": "tokens"}
    ]
  }
}
```

## 핵심 기능

### 1. Provider 인터페이스

```go
type Provider interface {
    Name() string
    FetchUsage(ctx context.Context) ([]UsagePoint, error)
    Validate() error
}
```

- 결과는 공통 스키마로 정규화
- `raw_json` 필드에 원본 응답 저장 (API 변경 대응)

### 2. 데이터베이스 스키마

```sql
CREATE TABLE providers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    enabled BOOLEAN DEFAULT TRUE,
    config_json TEXT,
    last_run DATETIME,
    last_error TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE usage_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id INTEGER NOT NULL,
    metric TEXT NOT NULL,
    used REAL NOT NULL,
    limit REAL,
    reset_at DATETIME,
    collected_at DATETIME NOT NULL,
    raw_json TEXT,
    FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
);
```

### 3. 스케줄러 안정성

- **Job 중복 실행 방지**: sync.Map 기반 lock
- **실패 시 재시도**: 3 회 + 지수 백오프 (5s, 10s, 20s)
- **상태 노출**: `last_error` 를 UI 에 표시

### 4. 운영 설정

- ✅ SQLite WAL 모드 + busy_timeout=5000
- ✅ `.env` 기반 credential 로딩
- ✅ Structured logging (JSON 로그)

## 개발자 참고사항

### OpenAI API 제한사항

OpenAI 는 실시간 토큰 사용량 API 를 공개하지 않습니다. 현재 구현은 조직 관리자용 billing endpoint 를 사용합니다. 프로덕션에서는:

1. 로컬 미들웨어에서 토큰 수 카운트
2. OpenAI Organization Dashboard 에서 일별 리포트 추출
3. Proxy 서버를 통한 요청 가로채기

### Anthropic API 제한사항

Anthropic 도 공개 usage API 가 없습니다. 로컬 instrumentation 이 필요합니다:

```go
// 미들웨어에서 토큰 수 기록
provider.RecordUsage(InputOutputTokens{Input: 100, Output: 50})
```

## 라이선스

MIT
