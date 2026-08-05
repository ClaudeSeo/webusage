//go:build browser

package http

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// designSystemProbe is one design-system primitive expressed as markup that is
// valid in both the design source and the live dashboard. Injecting identical
// markup into both documents isolates the stylesheet: any computed difference
// is a stylesheet difference, never a data difference.
type designSystemProbe struct {
	Name   string `json:"name"`
	Markup string `json:"markup"`
	Target string `json:"target"`
}

// designSystemProbes covers the shared component layer of the design source.
// Live provider data never reaches these nodes, so every declaration is
// compared strictly.
var designSystemProbes = []designSystemProbe{
	{Name: "button-default", Markup: `<button data-slot="button" class="btn-default">지금 수집</button>`, Target: `[data-slot="button"]`},
	{Name: "button-outline", Markup: `<button data-slot="button" class="btn-outline">취소</button>`, Target: `[data-slot="button"]`},
	{Name: "button-ghost", Markup: `<button data-slot="button" class="btn-ghost">닫기</button>`, Target: `[data-slot="button"]`},
	{Name: "button-block", Markup: `<button data-slot="button" class="btn-default btn-block">지금 수집</button>`, Target: `[data-slot="button"]`},
	{Name: "button-sm", Markup: `<button data-slot="button" class="btn-outline btn-sm">저장</button>`, Target: `[data-slot="button"]`},
	{Name: "button-icon", Markup: `<button data-slot="button" class="btn-ghost btn-icon">i</button>`, Target: `[data-slot="button"]`},
	{Name: "button-icon-sm", Markup: `<button data-slot="button" class="btn-ghost btn-icon btn-sm">i</button>`, Target: `[data-slot="button"]`},
	{Name: "badge-secondary", Markup: `<span data-slot="badge" class="badge-secondary">주</span>`, Target: `[data-slot="badge"]`},
	{Name: "badge-outline", Markup: `<span data-slot="badge" class="badge-outline">주간</span>`, Target: `[data-slot="badge"]`},
	{Name: "badge-ok", Markup: `<span data-slot="badge" class="badge-ok">정상</span>`, Target: `[data-slot="badge"]`},
	{Name: "badge-warn", Markup: `<span data-slot="badge" class="badge-warn"><span class="dot"></span>주의</span>`, Target: `[data-slot="badge"]`},
	{Name: "badge-danger", Markup: `<span data-slot="badge" class="badge-danger"><span class="dot"></span>한도 임박</span>`, Target: `[data-slot="badge"]`},
	{Name: "badge-dot", Markup: `<span data-slot="badge" class="badge-warn"><span class="dot"></span>주의</span>`, Target: `.dot`},
	{Name: "kbd", Markup: `<kbd>R</kbd>`, Target: `kbd`},
	{Name: "switch-on", Markup: `<button data-slot="switch" role="switch" aria-checked="true"></button>`, Target: `[data-slot="switch"]`},
	{Name: "switch-off", Markup: `<button data-slot="switch" role="switch" aria-checked="false"></button>`, Target: `[data-slot="switch"]`},
	{Name: "chip-on", Markup: `<button data-slot="chip" aria-pressed="true"><span class="swatch"></span>Claude</button>`, Target: `[data-slot="chip"]`},
	{Name: "chip-off", Markup: `<button data-slot="chip" aria-pressed="false"><span class="swatch"></span>Claude</button>`, Target: `[data-slot="chip"]`},
	{Name: "chip-swatch", Markup: `<button data-slot="chip" aria-pressed="true"><span class="swatch"></span>Claude</button>`, Target: `.swatch`},
	{Name: "tabs-list", Markup: `<div data-slot="tabs-list"><button data-slot="tabs-trigger" aria-selected="true">7일</button></div>`, Target: `[data-slot="tabs-list"]`},
	{Name: "tabs-trigger-on", Markup: `<div data-slot="tabs-list"><button data-slot="tabs-trigger" aria-selected="true">7일</button></div>`, Target: `[data-slot="tabs-trigger"]`},
	{Name: "toggle-item-on", Markup: `<div data-slot="toggle-group"><button data-slot="toggle-group-item" aria-pressed="true">누적</button></div>`, Target: `[data-slot="toggle-group-item"]`},
	{Name: "gauge-track", Markup: `<div data-slot="gauge"><span class="gauge-proj" style="width:70%"></span><span class="gauge-fill" style="width:40%"></span><span class="gauge-tick" style="left:90%"></span></div>`, Target: `[data-slot="gauge"]`},
	{Name: "gauge-fill", Markup: `<div data-slot="gauge"><span class="gauge-proj" style="width:70%"></span><span class="gauge-fill" style="width:40%"></span><span class="gauge-tick" style="left:90%"></span></div>`, Target: `.gauge-fill`},
	{Name: "gauge-projection", Markup: `<div data-slot="gauge"><span class="gauge-proj" style="width:70%"></span><span class="gauge-fill" style="width:40%"></span><span class="gauge-tick" style="left:90%"></span></div>`, Target: `.gauge-proj`},
	{Name: "gauge-tick", Markup: `<div data-slot="gauge"><span class="gauge-proj" style="width:70%"></span><span class="gauge-fill" style="width:40%"></span><span class="gauge-tick" style="left:90%"></span></div>`, Target: `.gauge-tick`},
	{Name: "gauge-fill-danger", Markup: `<div data-slot="gauge"><span class="gauge-fill danger" style="width:96%"></span></div>`, Target: `.gauge-fill`},
	{Name: "mini-gauge", Markup: `<div class="mini-gauge"><div data-slot="gauge"><span class="gauge-fill" style="width:40%"></span></div></div>`, Target: `.mini-gauge`},
	{Name: "mini-gauge-track", Markup: `<div class="mini-gauge"><div data-slot="gauge"><span class="gauge-fill" style="width:40%"></span></div></div>`, Target: `.mini-gauge [data-slot="gauge"]`},
	{Name: "tooltip", Markup: `<div data-slot="tooltip"><div class="tt-head">8/3 09:45</div><div class="tt-row"><span class="swatch"></span>Claude<span class="v">57%</span></div></div>`, Target: `[data-slot="tooltip"]`},
	{Name: "tooltip-open", Markup: `<div data-slot="tooltip" class="open"><div class="tt-head">8/3 09:45</div><div class="tt-row"><span class="swatch"></span>Claude<span class="v">57%</span></div></div>`, Target: `[data-slot="tooltip"]`},
	{Name: "tooltip-head", Markup: `<div data-slot="tooltip" class="open"><div class="tt-head">8/3 09:45</div><div class="tt-row"><span class="swatch"></span>Claude<span class="v">57%</span></div></div>`, Target: `.tt-head`},
	{Name: "tooltip-row", Markup: `<div data-slot="tooltip" class="open"><div class="tt-head">8/3 09:45</div><div class="tt-row"><span class="swatch"></span>Claude<span class="v">57%</span></div></div>`, Target: `.tt-row`},
	{Name: "tooltip-value", Markup: `<div data-slot="tooltip" class="open"><div class="tt-head">8/3 09:45</div><div class="tt-row"><span class="swatch"></span>Claude<span class="v">57%</span></div></div>`, Target: `.tt-row .v`},
	{Name: "alert", Markup: `<div data-slot="alert"><div><div class="at">표시할 프로바이더가 없습니다</div>설정에서 켜세요.</div></div>`, Target: `[data-slot="alert"]`},
	{Name: "alert-title", Markup: `<div data-slot="alert"><div><div class="at">표시할 프로바이더가 없습니다</div>설정에서 켜세요.</div></div>`, Target: `.at`},
	{Name: "toast", Markup: `<div data-slot="toast"><div><div class="tt">수집 완료</div><div class="td">반영했습니다</div></div></div>`, Target: `[data-slot="toast"]`},
	{Name: "toast-title", Markup: `<div data-slot="toast"><div><div class="tt">수집 완료</div><div class="td">반영했습니다</div></div></div>`, Target: `.tt`},
	{Name: "toast-detail", Markup: `<div data-slot="toast"><div><div class="tt">수집 완료</div><div class="td">반영했습니다</div></div></div>`, Target: `.td`},
	{Name: "heatmap-cell-empty", Markup: `<span class="hm-cell" data-lv="0"></span>`, Target: `.hm-cell`},
	{Name: "heatmap-cell-full", Markup: `<span class="hm-cell" data-lv="4"></span>`, Target: `.hm-cell`},
	{Name: "heatmap-legend", Markup: `<div class="hm-legend"><span>적음</span><span class="hm-cell" data-lv="2"></span><span>많음</span></div>`, Target: `.hm-legend`},
	{Name: "heatmap-legend-cell", Markup: `<div class="hm-legend"><span>적음</span><span class="hm-cell" data-lv="2"></span><span>많음</span></div>`, Target: `.hm-legend .hm-cell`},
	{Name: "heatmap-day", Markup: `<div class="hm-grid"><div class="hm-day">08/03</div><div class="hm-hour">09</div></div>`, Target: `.hm-day`},
	{Name: "heatmap-hour", Markup: `<div class="hm-grid"><div class="hm-day">08/03</div><div class="hm-hour">09</div></div>`, Target: `.hm-hour`},
	{Name: "table-header", Markup: `<table data-slot="table"><thead><tr><th class="sortable" aria-sort="descending">사용률<span class="arrow">↓</span></th></tr></thead><tbody><tr><td class="cell-strong">Claude</td><td class="cell-right num">57</td></tr></tbody></table>`, Target: `th`},
	{Name: "table-sort-arrow", Markup: `<table data-slot="table"><thead><tr><th class="sortable" aria-sort="descending">사용률<span class="arrow">↓</span></th></tr></thead><tbody><tr><td class="cell-strong">Claude</td><td class="cell-right num">57</td></tr></tbody></table>`, Target: `th .arrow`},
	{Name: "table-cell-strong", Markup: `<table data-slot="table"><thead><tr><th class="sortable" aria-sort="descending">사용률<span class="arrow">↓</span></th></tr></thead><tbody><tr><td class="cell-strong">Claude</td><td class="cell-right num">57</td></tr></tbody></table>`, Target: `td.cell-strong`},
	{Name: "table-cell-right", Markup: `<table data-slot="table"><thead><tr><th class="sortable" aria-sort="descending">사용률<span class="arrow">↓</span></th></tr></thead><tbody><tr><td class="cell-strong">Claude</td><td class="cell-right num">57</td></tr></tbody></table>`, Target: `td.cell-right`},
	{Name: "snapshot-note", Markup: `<span class="snapshot-note"><span>8/3 09:45 스냅샷</span></span>`, Target: `.snapshot-note`},
	{Name: "brand-sub", Markup: `<div class="brand"><div class="brand-mark">wu</div><div><div class="brand-name">webusage</div><div class="brand-sub">usage monitor</div></div></div>`, Target: `.brand-sub`},
	{Name: "brand-mark", Markup: `<div class="brand"><div class="brand-mark">wu</div><div><div class="brand-name">webusage</div><div class="brand-sub">usage monitor</div></div></div>`, Target: `.brand-mark`},
	{Name: "pulse", Markup: `<div class="foot-row"><span class="pulse"></span><span>수집 정상</span></div>`, Target: `.pulse`},
	{Name: "foot-note", Markup: `<div class="foot-note">수집 주기 15분</div>`, Target: `.foot-note`},
	{Name: "legend-hatch", Markup: `<span class="legend-hatch"></span>`, Target: `.legend-hatch`},
	{Name: "metric-row", Markup: `<div class="metric"><div class="metric-top"><span class="metric-name">weekly</span><span class="metric-val">57</span></div><div class="metric-foot"><span>57% 사용</span><span class="right over">리셋 시점 추정 120%</span></div></div>`, Target: `.metric`},
	{Name: "metric-name", Markup: `<div class="metric"><div class="metric-top"><span class="metric-name">weekly</span><span class="metric-val">57</span></div><div class="metric-foot"><span>57% 사용</span><span class="right over">리셋 시점 추정 120%</span></div></div>`, Target: `.metric-name`},
	{Name: "metric-value", Markup: `<div class="metric"><div class="metric-top"><span class="metric-name">weekly</span><span class="metric-val">57</span></div><div class="metric-foot"><span>57% 사용</span><span class="right over">리셋 시점 추정 120%</span></div></div>`, Target: `.metric-val`},
	{Name: "metric-foot", Markup: `<div class="metric"><div class="metric-top"><span class="metric-name">weekly</span><span class="metric-val">57</span></div><div class="metric-foot"><span>57% 사용</span><span class="right over">리셋 시점 추정 120%</span></div></div>`, Target: `.metric-foot`},
	{Name: "metric-foot-over", Markup: `<div class="metric"><div class="metric-foot"><span>57% 사용</span><span class="right over">리셋 시점 추정 120%</span></div></div>`, Target: `.metric-foot .over`},
	{Name: "metric-foot-near", Markup: `<div class="metric"><div class="metric-foot"><span>57% 사용</span><span class="right near">리셋 시점 추정 88%</span></div></div>`, Target: `.metric-foot .near`},
	{Name: "settings-row", Markup: `<div class="set-row"><div class="grow"><div class="set-row-name">사용량 기준</div><div class="set-row-desc">쓴 만큼 채워집니다</div></div><button data-slot="switch" aria-checked="true"></button></div>`, Target: `.set-row`},
	{Name: "settings-row-name", Markup: `<div class="set-row"><div class="grow"><div class="set-row-name">사용량 기준</div><div class="set-row-desc">쓴 만큼 채워집니다</div></div><button data-slot="switch" aria-checked="true"></button></div>`, Target: `.set-row-name`},
	{Name: "settings-row-desc", Markup: `<div class="set-row"><div class="grow"><div class="set-row-name">사용량 기준</div><div class="set-row-desc">쓴 만큼 채워집니다</div></div><button data-slot="switch" aria-checked="true"></button></div>`, Target: `.set-row-desc`},
	{Name: "settings-title", Markup: `<div class="set-group"><div class="set-title">프로바이더</div><p class="set-help">끄면 모두 제외됩니다.</p></div>`, Target: `.set-title`},
	{Name: "settings-help", Markup: `<div class="set-group"><div class="set-title">프로바이더</div><p class="set-help">끄면 모두 제외됩니다.</p></div>`, Target: `.set-help`},
	{Name: "preference-provider", Markup: `<div class="pref-provider"><div class="pref-head">Claude</div><div class="pref-item"><span class="nm">weekly</span><span class="pref-order"><button data-slot="button" class="btn-ghost btn-icon btn-sm">↑</button></span><button data-slot="switch" aria-checked="true"></button></div></div>`, Target: `.pref-provider`},
	{Name: "preference-head", Markup: `<div class="pref-provider"><div class="pref-head">Claude</div><div class="pref-item"><span class="nm">weekly</span><span class="pref-order"><button data-slot="button" class="btn-ghost btn-icon btn-sm">↑</button></span><button data-slot="switch" aria-checked="true"></button></div></div>`, Target: `.pref-head`},
	{Name: "preference-item", Markup: `<div class="pref-provider"><div class="pref-head">Claude</div><div class="pref-item"><span class="nm">weekly</span><span class="pref-order"><button data-slot="button" class="btn-ghost btn-icon btn-sm">↑</button></span><button data-slot="switch" aria-checked="true"></button></div></div>`, Target: `.pref-item`},
	{Name: "preference-item-name", Markup: `<div class="pref-provider"><div class="pref-head">Claude</div><div class="pref-item"><span class="nm">weekly</span><span class="pref-order"><button data-slot="button" class="btn-ghost btn-icon btn-sm">↑</button></span><button data-slot="switch" aria-checked="true"></button></div></div>`, Target: `.pref-item .nm`},
	{Name: "preference-item-off-name", Markup: `<div class="pref-provider"><div class="pref-item off"><span class="nm">weekly</span></div></div>`, Target: `.pref-item.off .nm`},
	{Name: "preference-order", Markup: `<div class="pref-provider"><div class="pref-item"><span class="nm">weekly</span><span class="pref-order"><button data-slot="button" class="btn-ghost btn-icon btn-sm">↑</button></span></div></div>`, Target: `.pref-order`},
	{Name: "section-head", Markup: `<div class="sec-head"><div><div class="sec-title">지표 테이블</div><div class="sec-desc">정렬과 비교</div></div><div class="right"><span data-slot="badge" class="badge-outline">3</span></div></div>`, Target: `.sec-head`},
	{Name: "section-title", Markup: `<div class="sec-head"><div><div class="sec-title">지표 테이블</div><div class="sec-desc">정렬과 비교</div></div></div>`, Target: `.sec-title`},
	{Name: "section-desc", Markup: `<div class="sec-head"><div><div class="sec-title">지표 테이블</div><div class="sec-desc">정렬과 비교</div></div></div>`, Target: `.sec-desc`},
	{Name: "card", Markup: `<div data-slot="card"><div data-slot="card-header"><div><h3 data-slot="card-title">Claude</h3><p data-slot="card-description">지표 3개</p></div></div><div data-slot="card-content">본문</div><div data-slot="card-footer">푸터</div></div>`, Target: `[data-slot="card"]`},
	{Name: "card-header", Markup: `<div data-slot="card"><div data-slot="card-header"><div><h3 data-slot="card-title">Claude</h3><p data-slot="card-description">지표 3개</p></div></div><div data-slot="card-content">본문</div><div data-slot="card-footer">푸터</div></div>`, Target: `[data-slot="card-header"]`},
	{Name: "card-title", Markup: `<div data-slot="card"><div data-slot="card-header"><div><h3 data-slot="card-title">Claude</h3><p data-slot="card-description">지표 3개</p></div></div></div>`, Target: `[data-slot="card-title"]`},
	{Name: "card-description", Markup: `<div data-slot="card"><div data-slot="card-header"><div><h3 data-slot="card-title">Claude</h3><p data-slot="card-description">지표 3개</p></div></div></div>`, Target: `[data-slot="card-description"]`},
	{Name: "card-content", Markup: `<div data-slot="card"><div data-slot="card-content">본문</div></div>`, Target: `[data-slot="card-content"]`},
	{Name: "card-footer", Markup: `<div data-slot="card"><div data-slot="card-content">본문</div><div data-slot="card-footer">푸터</div></div>`, Target: `[data-slot="card-footer"]`},
	{Name: "screen-reader-only", Markup: `<span class="sr-only">시간당 추정</span>`, Target: `.sr-only`},
}

// designSystemStyleKeys are the declarations the design source controls for its
// component layer.
var designSystemStyleKeys = []string{
	"display", "position", "boxSizing", "flexDirection", "flexGrow", "flexShrink", "alignItems", "justifyContent",
	"gap", "width", "height", "minWidth", "minHeight", "maxWidth", "flexBasis",
	"paddingTop", "paddingRight", "paddingBottom", "paddingLeft",
	"marginTop", "marginRight", "marginBottom", "marginLeft",
	"borderTopWidth", "borderRightWidth", "borderBottomWidth", "borderLeftWidth",
	"borderTopStyle", "borderTopColor", "borderRightColor", "borderBottomColor", "borderLeftColor",
	"borderTopLeftRadius", "borderTopRightRadius", "borderBottomRightRadius", "borderBottomLeftRadius",
	"color", "backgroundColor", "backgroundImage", "boxShadow", "opacity", "overflow", "textAlign", "textTransform",
	"fontFamily", "fontSize", "fontWeight", "fontVariantNumeric", "lineHeight", "letterSpacing", "whiteSpace",
	"transitionProperty", "transitionDuration", "transitionTimingFunction",
	"animationName", "animationDuration", "animationTimingFunction", "animationFillMode",
	"transform", "zIndex", "cursor", "textIndent", "inset",
}

// TestDashboardDesignSystemParity injects identical component markup into the
// design source and the live dashboard and requires the computed styles to
// match. It isolates the stylesheet from live data, so a missing or drifted
// component rule fails here even when the data-bearing surfaces agree.
func TestDashboardDesignSystemParity(t *testing.T) {
	chromePath := os.Getenv("WEBUSAGE_CHROME_BIN")
	if chromePath == "" {
		chromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	}
	info, err := os.Stat(chromePath)
	if err != nil {
		t.Fatalf("Chrome executable %q is unavailable: %v", chromePath, err)
	}
	if info.IsDir() || info.Mode()&0111 == 0 {
		t.Fatalf("Chrome executable %q is not executable", chromePath)
	}

	_, localServer, _ := setupDashboardBrowserServer(t)
	defer localServer.Close()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	designURL := "file://" + workingDirectory + "/../../webusage-dashboard.html"

	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(),
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-extensions", true),
	)
	defer cancelAllocator()
	ctx, cancelContext := chromedp.NewContext(allocator)
	defer cancelContext()
	ctx, cancelTimeout := context.WithTimeout(ctx, 120*time.Second)
	defer cancelTimeout()
	if err := chromedp.Run(ctx, chromedp.EmulateViewport(1440, 1000)); err != nil {
		t.Fatalf("Chrome failed to launch: %v", err)
	}

	design, err := captureDesignSystemProbes(ctx, designURL)
	if err != nil {
		t.Fatalf("capture design source component styles: %v", err)
	}
	live, err := captureDesignSystemProbes(ctx, localServer.URL)
	if err != nil {
		t.Fatalf("capture live dashboard component styles: %v", err)
	}

	for _, probe := range designSystemProbes {
		designStyle, ok := design[probe.Name]
		if !ok || len(designStyle) == 0 {
			t.Fatalf("design source did not resolve component probe %q (%s)", probe.Name, probe.Target)
		}
		liveStyle, ok := live[probe.Name]
		if !ok || len(liveStyle) == 0 {
			t.Fatalf("live dashboard did not resolve component probe %q (%s)", probe.Name, probe.Target)
		}
		var differences []string
		for _, key := range designSystemStyleKeys {
			if designStyle[key] != liveStyle[key] {
				differences = append(differences, fmt.Sprintf("%s: design=%q live=%q", key, designStyle[key], liveStyle[key]))
			}
		}
		if len(differences) > 0 {
			sort.Strings(differences)
			t.Errorf("component %q (%s) drifted from the design source:\n  %v", probe.Name, probe.Target, differences)
		}
	}
}

func captureDesignSystemProbes(ctx context.Context, url string) (map[string]map[string]string, error) {
	if err := chromedp.Run(ctx, chromedp.Navigate(url), chromedp.WaitReady("#cardGrid")); err != nil {
		return nil, err
	}
	probesJSON, err := json.Marshal(designSystemProbes)
	if err != nil {
		return nil, err
	}
	keysJSON, err := json.Marshal(designSystemStyleKeys)
	if err != nil {
		return nil, err
	}
	expression := fmt.Sprintf(`(() => {
const probes = %s;
const keys = %s;
const host = document.createElement('div');
host.id = 'design-system-probe-host';
host.style.position = 'absolute';
host.style.left = '0';
host.style.top = '0';
host.style.width = '420px';
document.body.appendChild(host);
const result = {};
probes.forEach(probe => {
  host.replaceChildren();
  const wrapper = document.createElement('div');
  wrapper.innerHTML = probe.markup;
  host.appendChild(wrapper);
  const node = wrapper.querySelector(probe.target);
  if (!node) { result[probe.name] = {}; return; }
  const computed = getComputedStyle(node);
  const style = {};
  keys.forEach(key => { style[key] = String(computed[key]); });
  result[probe.name] = style;
});
host.remove();
return JSON.stringify(result);
})()`, string(probesJSON), string(keysJSON))
	var encoded string
	if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &encoded)); err != nil {
		return nil, err
	}
	var captured map[string]map[string]string
	if err := json.Unmarshal([]byte(encoded), &captured); err != nil {
		return nil, fmt.Errorf("decode probe styles: %w; raw=%s", err, encoded)
	}
	return captured, nil
}
