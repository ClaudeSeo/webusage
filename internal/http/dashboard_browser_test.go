//go:build browser

package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ClaudeSeo/webusage/internal/collector"
	"github.com/ClaudeSeo/webusage/internal/openusage"
	"github.com/ClaudeSeo/webusage/internal/store"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// TestDashboardBrowserAcceptance is the opt-in acceptance suite for the
// rendered dashboard. It deliberately launches a real Chrome process and
// drives the same HTTP server and templates used by the application.
func TestDashboardBrowserAcceptance(t *testing.T) {
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

	server, localServer, collectionCalls := setupDashboardBrowserServer(t)
	defer localServer.Close()
	response, err := http.Get(localServer.URL)
	if err != nil {
		t.Fatalf("GET / dashboard fixture: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatalf("read GET / dashboard fixture: %v", readErr)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `data-metric="session"`) || !strings.Contains(string(body), `data-metric="weekly"`) {
		t.Fatalf("GET / dashboard fixture did not render multiple metrics: status=%d body=%s", response.StatusCode, string(body))
	}
	// Provider enablement is a DOM-only acceptance checkpoint. Keep its
	// asynchronous production side effect out of the later refresh assertion.
	server.SetCollector(nil)

	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(),
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-extensions", true),
	)
	defer cancelAllocator()

	ctx, cancelContext := chromedp.NewContext(allocator)
	defer cancelContext()
	ctx, cancelTimeout := context.WithTimeout(ctx, 90*time.Second)
	defer cancelTimeout()
	if err := chromedp.Run(ctx); err != nil {
		t.Fatalf("Chrome failed to launch: %v", err)
	}
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.ActionFunc(func(actionContext context.Context) error {
			return emulation.SetTimezoneOverride("America/Los_Angeles").Do(actionContext)
		}),
		chromedp.Navigate(localServer.URL),
		chromedp.WaitReady("#cardGrid"),
	); err != nil {
		t.Fatalf("navigate dashboard in Chrome: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.readyState === 'complete' && document.querySelectorAll('#cardGrid [data-slot="card"]').length >= 2 && document.querySelector('#overviewStatus').textContent === 'API 연결됨'`); err != nil {
		t.Fatalf("initial dashboard did not become ready: %v", err)
	}

	// Desktop layout, SSR projection/error state, eight-column semantics, and
	// real SVG/gauge geometry are checked through computed browser state.
	assertDashboardBrowser(t, ctx, `(() => {
		const sidebar = getComputedStyle(document.querySelector('[data-slot="sidebar"]'));
		const topbar = getComputedStyle(document.querySelector('[data-slot="topbar"]'));
		const grid = getComputedStyle(document.getElementById('cardGrid'));
		const gauge = document.querySelector('[data-slot="gauge"]');
		const fill = gauge && gauge.querySelector('.gauge-fill');
		const projection = gauge && gauge.querySelector('.gauge-proj');
		const tableColumns = document.querySelectorAll('#metricTable thead th').length;
		return sidebar.position === 'sticky' && sidebar.width === '248px' && topbar.position === 'sticky' &&
			grid.display === 'grid' && grid.gridTemplateColumns.split(' ').length === 2 &&
			gauge && fill && projection && parseFloat(getComputedStyle(gauge).height) >= 8 &&
			parseFloat(fill.style.width) > 0 && parseFloat(projection.style.width) >= 0 && tableColumns === 8;
	})()`)
	assertDashboardBrowser(t, ctx, `(() => {
		const error = document.querySelector('.provider-error');
		const projection = [...document.querySelectorAll('.metric-projection')].some(node => /리셋 시점 추정|표본 부족|페이스 계산 불가/.test(node.textContent));
		const tableGauge = document.querySelector('#metricTable tr[data-provider="claude"][data-metric="session"] .table-gauge-fill');
		return error && error.textContent.includes('수집에 실패') && projection && tableGauge && parseFloat(tableGauge.style.width) > 0;
	})()`)
	assertDashboardBrowser(t, ctx, `(() => {
		const date = document.querySelector('.collected-at');
		return date && date.textContent.length > 0 && !date.textContent.includes('undefined');
	})()`)
	assertDashboardBrowser(t, ctx, fmt.Sprintf(`(() => {
		const cardMetric = document.querySelector('[data-provider-id="kirocli"] [data-metric="unlimited"]');
		const tableRow = document.querySelector('#metricTableBody tr[data-provider="kirocli"][data-metric="unlimited"]');
		const cardGauge = cardMetric && cardMetric.querySelector('[data-slot="gauge"]');
		const miniGauge = tableRow && tableRow.querySelector('.mini-gauge');
		const percent = cardMetric && cardMetric.querySelector('.metric-percent');
		const tablePercent = tableRow && tableRow.querySelector('.table-percent');
		const hydratedDate = document.querySelector('[data-provider-id="kirocli"] .collected-at');
		return cardMetric && tableRow && !cardGauge && !miniGauge && percent && percent.textContent.trim() === '—' &&
			tablePercent && tablePercent.textContent.trim() === '—' && hydratedDate && hydratedDate.textContent.trim() === %q;
	})()`, browserBoundaryDateText(browserBoundaryTimestamp())))
	assertDashboardBrowser(t, ctx, `(() => {
		const cardMetric = document.querySelector('[data-provider-id="claude"] [data-metric="weekly"]');
		const tableRow = document.querySelector('#metricTableBody tr[data-provider="claude"][data-metric="weekly"]');
		const cardValue = cardMetric && cardMetric.querySelector('.metric-display');
		const cardPercent = cardMetric && cardMetric.querySelector('.metric-percent');
		const tableValue = tableRow && tableRow.querySelector('.table-used');
		const tablePercent = tableRow && tableRow.querySelector('.table-percent');
		const cardFill = cardMetric && cardMetric.querySelector('.metric-progress');
		const tableFill = tableRow && tableRow.querySelector('.table-gauge-fill');
		const normalize = value => value && value.textContent.replace(/,/g, '').trim();
		return cardMetric && tableRow && normalize(cardValue) === '0 / 1000' && cardPercent && cardPercent.textContent.trim() === '0.0% 사용' &&
			tableValue && tableValue.textContent.trim() === '0' && tablePercent && tablePercent.textContent.trim() === '0.0%' && tablePercent.getAttribute('aria-label') === '0.0% 사용' &&
			cardFill && parseFloat(cardFill.style.width) === 0 && tableFill && parseFloat(tableFill.style.width) === 0;
	})()`)

	// The providers table is sorted through real user clicks in both gauge
	// modes. Unlimited rows stay visible with limit-unavailable values while
	// limited rows keep deterministic order and bounded gauge geometry.
	if err := chromedp.Run(ctx, chromedp.Click(`[data-view="providers"]`)); err != nil {
		t.Fatalf("open providers view for sorting: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `!document.querySelector('#view-providers').hidden && document.querySelectorAll('#metricTableBody tr:not([hidden])').length >= 4`); err != nil {
		t.Fatalf("providers table did not load for sorting: %v", err)
	}
	if err := chromedp.Run(ctx, chromedp.Click("#metricValueHeader")); err != nil {
		t.Fatalf("sort usage values: %v", err)
	}
	assertDashboardBrowser(t, ctx, `(() => {
		const rows = [...document.querySelectorAll('#metricTableBody tr:not([hidden])')];
		const order = rows.map(row => row.dataset.provider + ':' + row.dataset.metric);
		const values = rows.map(row => row.querySelector('.table-used').textContent.trim());
		return document.querySelector('#metricValueHeader').getAttribute('aria-sort') === 'ascending' &&
			JSON.stringify(order.slice(0, 4)) === JSON.stringify(['claude:weekly', 'kirocli:credits', 'claude:session', 'kirocli:unlimited']) &&
			JSON.stringify(values.slice(0, 4)) === JSON.stringify(['0', '36', '72', '42']);
	})()`)
	if err := chromedp.Run(ctx, chromedp.Click("#metricPercentHeader")); err != nil {
		t.Fatalf("sort usage percentages: %v", err)
	}
	assertDashboardBrowser(t, ctx, `(() => {
		const rows = [...document.querySelectorAll('#metricTableBody tr:not([hidden])')];
		const order = rows.map(row => row.dataset.provider + ':' + row.dataset.metric);
		const values = rows.map(row => row.querySelector('.table-percent').textContent.trim());
		const labels = rows.map(row => row.querySelector('.table-percent').getAttribute('aria-label'));
		const bounded = rows.filter(row => row.dataset.metric !== 'unlimited').every(row => {
			const gauge = row.querySelector('.mini-gauge');
			const fill = gauge && gauge.querySelector('.table-gauge-fill');
			const projection = gauge && gauge.querySelector('.table-gauge-proj');
			return gauge && fill && projection && parseFloat(fill.style.width) >= 0 && parseFloat(fill.style.width) <= 100 &&
				parseFloat(projection.style.width) >= 0 && parseFloat(projection.style.width) <= 100;
		});
		return document.querySelector('#metricPercentHeader').getAttribute('aria-sort') === 'ascending' &&
			JSON.stringify(order.slice(0, 4)) === JSON.stringify(['claude:weekly', 'kirocli:credits', 'claude:session', 'kirocli:unlimited']) &&
			JSON.stringify(values.slice(0, 4)) === JSON.stringify(['0.0%', '36.0%', '72.0%', '—']) &&
			JSON.stringify(labels.slice(0, 3)) === JSON.stringify(['0.0% 사용', '36.0% 사용', '72.0% 사용']) && bounded;
	})()`)
	if err := browserDOMClick(ctx, "#settingsBtn"); err != nil {
		t.Fatalf("open settings for remaining sort: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#settingsDrawer').classList.contains('open') && document.activeElement.id === 'sheetClose'`); err != nil {
		t.Fatalf("settings did not open for remaining sort: %v", err)
	}
	if err := chromedp.Run(ctx,
		chromedp.Focus("gaugeModeSwitch", chromedp.ByID),
		chromedp.KeyEvent(" "),
	); err != nil {
		t.Fatalf("switch to remaining mode for sorting: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#gaugeModeSwitch').getAttribute('aria-checked') === 'false' && document.querySelector('#metricValueHeader').dataset.gaugeMode === 'remaining'`); err != nil {
		t.Fatalf("remaining gauge mode did not activate: %v", err)
	}
	if err := browserDOMClick(ctx, "#sheetClose"); err != nil {
		t.Fatalf("close settings after remaining switch: %v", err)
	}
	assertDashboardBrowser(t, ctx, `(() => {
		const cardMetric = document.querySelector('[data-provider-id="claude"] [data-metric="weekly"]');
		const tableRow = document.querySelector('#metricTableBody tr[data-provider="claude"][data-metric="weekly"]');
		const cardValue = cardMetric && cardMetric.querySelector('.metric-display');
		const cardPercent = cardMetric && cardMetric.querySelector('.metric-percent');
		const tableValue = tableRow && tableRow.querySelector('.table-used');
		const tablePercent = tableRow && tableRow.querySelector('.table-percent');
		const cardFill = cardMetric && cardMetric.querySelector('.metric-progress');
		const tableFill = tableRow && tableRow.querySelector('.table-gauge-fill');
		const normalize = value => value && value.textContent.replace(/,/g, '').trim();
		return cardMetric && tableRow && normalize(cardValue) === '1000 / 1000' && cardPercent && cardPercent.textContent.trim() === '100.0% 남음' &&
			tableValue && tableValue.textContent.replace(/,/g, '').trim() === '1000' && tablePercent && tablePercent.textContent.trim() === '100.0%' && tablePercent.getAttribute('aria-label') === '100.0% 남음' &&
			cardFill && parseFloat(cardFill.style.width) === 100 && tableFill && parseFloat(tableFill.style.width) === 100;
	})()`)
	if err := chromedp.Run(ctx, chromedp.Click("#metricValueHeader")); err != nil {
		t.Fatalf("sort remaining values: %v", err)
	}
	assertDashboardBrowser(t, ctx, `(() => {
		const rows = [...document.querySelectorAll('#metricTableBody tr:not([hidden])')];
		const order = rows.map(row => row.dataset.provider + ':' + row.dataset.metric);
		const values = rows.map(row => row.querySelector('.table-used').textContent.trim());
		return document.querySelector('#metricValueHeader').getAttribute('aria-sort') === 'ascending' &&
			JSON.stringify(order.slice(0, 4)) === JSON.stringify(['claude:session', 'kirocli:credits', 'claude:weekly', 'kirocli:unlimited']) &&
			JSON.stringify(values.slice(0, 4)) === JSON.stringify(['28', '64', '1,000', '—']);
	})()`)
	if err := chromedp.Run(ctx, chromedp.Click("#metricPercentHeader")); err != nil {
		t.Fatalf("sort remaining percentages: %v", err)
	}
	assertDashboardBrowser(t, ctx, `(() => {
		const rows = [...document.querySelectorAll('#metricTableBody tr:not([hidden])')];
		const order = rows.map(row => row.dataset.provider + ':' + row.dataset.metric);
		const values = rows.map(row => row.querySelector('.table-percent').textContent.trim());
		const labels = rows.map(row => row.querySelector('.table-percent').getAttribute('aria-label'));
		const unlimited = rows.find(row => row.dataset.metric === 'unlimited');
		const cardMetric = document.querySelector('[data-provider-id="kirocli"] [data-metric="unlimited"]');
		return document.querySelector('#metricPercentHeader').getAttribute('aria-sort') === 'ascending' &&
			JSON.stringify(order.slice(0, 4)) === JSON.stringify(['claude:session', 'kirocli:credits', 'claude:weekly', 'kirocli:unlimited']) &&
			JSON.stringify(values.slice(0, 4)) === JSON.stringify(['28.0%', '64.0%', '100.0%', '—']) &&
			JSON.stringify(labels.slice(0, 3)) === JSON.stringify(['28.0% 남음', '64.0% 남음', '100.0% 남음']) && unlimited && !unlimited.querySelector('.mini-gauge') &&
			cardMetric && !cardMetric.querySelector('[data-slot="gauge"]');
	})()`)
	if err := browserDOMClick(ctx, "#settingsBtn"); err != nil {
		t.Fatalf("reopen settings to restore usage mode: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#settingsDrawer').classList.contains('open') && document.activeElement.id === 'sheetClose'`); err != nil {
		t.Fatalf("settings did not reopen: %v", err)
	}
	if err := chromedp.Run(ctx,
		chromedp.Focus("gaugeModeSwitch", chromedp.ByID),
		chromedp.KeyEvent(" "),
	); err != nil {
		t.Fatalf("restore usage mode: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#gaugeModeSwitch').getAttribute('aria-checked') === 'true' && document.querySelector('#metricValueHeader').dataset.gaugeMode === 'usage'`); err != nil {
		t.Fatalf("usage gauge mode did not restore: %v", err)
	}
	if err := browserDOMClick(ctx, "#sheetClose"); err != nil {
		t.Fatalf("close settings after restoring usage mode: %v", err)
	}

	// Trends use real API responses and SVG output. Exercise every supported
	// range, provider chips, cumulative mode, and reset-aware delta mode.
	if err := chromedp.Run(ctx, chromedp.Click(`[data-view="trends"]`)); err != nil {
		t.Fatalf("open trends view: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#view-trends') && !document.querySelector('#view-trends').hidden && document.querySelectorAll('#chipRow [data-slot="chip"]').length >= 2`); err != nil {
		t.Fatalf("trends did not load: %v", err)
	}
	// Let the initial dashboard hydration settle before issuing range changes;
	// the real client intentionally performs these requests concurrently.
	if err := chromedp.Run(ctx, chromedp.Sleep(500*time.Millisecond)); err != nil {
		t.Fatalf("wait for trend hydration: %v", err)
	}
	assertDashboardBrowser(t, ctx, `document.querySelector('#trendDataStatus').dataset.apiState === 'connected' && document.querySelector('#trendDataStatus').textContent === '설정의 지표 순서 기준' && document.querySelectorAll('#trendChart .series-line').length >= 1 && document.querySelector('#trendChart').textContent.includes('KST')`)
	// Every plotted series must own a line, not just the one whose first point
	// happens to start the shared timeline. A path that opens with anything but
	// M is dropped by the renderer, so the series would silently vanish.
	assertDashboardBrowser(t, ctx, `(() => {
		const counts = Array.from(document.querySelectorAll('#trendChart .series-dot')).reduce((totals, dot) => {
			totals[dot.dataset.series] = (totals[dot.dataset.series] || 0) + 1;
			return totals;
		}, {});
		const expected = Object.values(counts).filter(count => count >= 2).length;
		const lines = Array.from(document.querySelectorAll('#trendChart .series-line'));
		return expected >= 2 && lines.length === expected && lines.every(line => line.getAttribute('d').startsWith('M'));
	})()`)
	for _, rangeValue := range []string{"5h", "24h", "7d", "30d"} {
		if err := chromedp.Run(ctx, chromedp.Click(fmt.Sprintf(`#rangeTabs [data-range="%s"]`, rangeValue), chromedp.ByQuery)); err != nil {
			t.Fatalf("select trend range %s: %v", rangeValue, err)
		}
		if err := waitDashboardBrowser(ctx, fmt.Sprintf(`document.querySelector('#rangeTabs [data-range="%s"]').getAttribute('aria-selected') === 'true' && document.querySelector('#chartFoot').textContent.includes('데이터 포인트')`, rangeValue)); err != nil {
			var state string
			_ = browserEvaluate(ctx, `JSON.stringify({selected: document.querySelector('#rangeTabs [data-range="`+rangeValue+`"]').getAttribute('aria-selected'), foot: document.querySelector('#chartFoot').textContent, status: document.querySelector('#trendDataStatus').textContent})`, &state)
			t.Fatalf("trend range %s did not render: %v (state=%s)", rangeValue, err, state)
		}
	}
	if err := chromedp.Run(ctx, chromedp.Click(`#modeGroup [data-mode="delta"]`)); err != nil {
		t.Fatalf("select delta mode: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#modeGroup [data-mode="delta"]').getAttribute('aria-pressed') === 'true' && document.querySelector('#chartFoot').textContent.includes('데이터 포인트')`); err != nil {
		t.Fatalf("delta trend mode did not render: %v", err)
	}
	assertDashboardBrowser(t, ctx, `document.querySelectorAll('#trendChart .reset-mark').length >= 1 && document.querySelectorAll('#trendChart rect').length >= 1`)
	var hoverPoint [2]float64
	if err := browserEvaluate(ctx, `(() => { const rects = document.querySelectorAll('#trendChart rect'); const rect = rects[1].getBoundingClientRect(); return [rect.left + rect.width / 2, rect.top + rect.height / 2]; })()`, &hoverPoint); err != nil {
		t.Fatalf("locate trend chart hover target: %v", err)
	}
	if err := chromedp.Run(ctx, chromedp.MouseEvent(input.MouseMoved, hoverPoint[0], hoverPoint[1])); err != nil {
		t.Fatalf("hover trend chart: %v", err)
	}
	assertDashboardBrowser(t, ctx, `document.querySelector('#chartTip').classList.contains('open') && getComputedStyle(document.querySelector('#chartCrosshair')).display !== 'none'`)
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('#trendChart').dispatchEvent(new MouseEvent('mouseleave', {bubbles:true}))`, nil)); err != nil {
		t.Fatalf("leave trend chart: %v", err)
	}
	assertDashboardBrowser(t, ctx, `!document.querySelector('#chartTip').classList.contains('open') && getComputedStyle(document.querySelector('#chartCrosshair')).display === 'none'`)
	var chipBefore string
	if err := browserEvaluate(ctx, `document.querySelector('#chipRow [data-slot="chip"]').getAttribute('aria-pressed')`, &chipBefore); err != nil {
		t.Fatalf("read provider chip state: %v", err)
	}
	if err := chromedp.Run(ctx, chromedp.Click(`#chipRow [data-slot="chip"]`, chromedp.ByQuery)); err != nil {
		t.Fatalf("toggle provider chip: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#chipRow [data-slot="chip"]').getAttribute('aria-pressed') !== `+fmt.Sprintf("%q", chipBefore)); err != nil {
		t.Fatalf("provider chip state did not update: %v", err)
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`Array.from(document.querySelectorAll('#chipRow [data-slot="chip"][aria-pressed="true"]')).forEach(button => button.click())`, nil)); err != nil {
		t.Fatalf("disable all trend series: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#trendChart').textContent.includes('이 구간에 수집된 스냅샷이 없습니다') && document.querySelector('#chartFoot').textContent.includes('데이터 포인트 0개')`); err != nil {
		t.Fatalf("empty trend state did not render: %v", err)
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`Array.from(document.querySelectorAll('#chipRow [data-slot="chip"][aria-pressed="false"]')).forEach(button => button.click())`, nil)); err != nil {
		t.Fatalf("restore trend series: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelectorAll('#trendChart rect').length >= 1`); err != nil {
		t.Fatalf("trend series did not restore: %v", err)
	}

	// Activity renders the real coverage grid and its KST labels from /api/activity.
	if err := chromedp.Run(ctx, chromedp.Click(`[data-view="activity"]`)); err != nil {
		t.Fatalf("open activity view: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `!document.querySelector('#view-activity').hidden && document.querySelectorAll('#heatmap .hm-cell').length >= 24`); err != nil {
		t.Fatalf("activity coverage did not load: %v", err)
	}
	assertDashboardBrowser(t, ctx, `document.querySelectorAll('#heatmap .hm-hour').length === 24 && document.querySelector('#hmFoot').textContent.includes('Asia/Seoul')`)

	// The projected-overshoot hatch must carry its metric's severity tone so a
	// projected breach does not read like a healthy projection.
	assertDashboardBrowser(t, ctx, `Array.from(document.querySelectorAll('#cardGrid .metric-section')).every(metric => {
		const projection = metric.querySelector('.gauge-proj');
		if (!projection) return true;
		const severity = metric.dataset.severity;
		const tone = severity === 'danger' ? 'var(--danger)' : severity === 'warn' ? 'var(--warn)' : 'var(--fg)';
		return projection.style.background.includes(tone);
	})`)

	// Mobile layout checkpoints include the responsive sidebar, sheet, and cards;
	// collection remains available from the responsive sidebar navigation.
	if err := chromedp.Run(ctx, chromedp.EmulateViewport(390, 844)); err != nil {
		t.Fatalf("set mobile viewport: %v", err)
	}
	assertDashboardBrowser(t, ctx, `(() => {
		const sidebar = getComputedStyle(document.querySelector('[data-slot="sidebar"]'));
		const grid = getComputedStyle(document.getElementById('cardGrid'));
		const action = document.getElementById('mobileCollectBtn');
		const sheet = getComputedStyle(document.querySelector('[data-slot="sheet"]'));
		return sidebar.position === 'fixed' && sidebar.transform.includes('matrix') && grid.gridTemplateColumns === 'minmax(0px, 1fr)' &&
			action.hidden && sheet.width === '358.797px';
	})()`)

	// Mobile navigation is a real focus/overlay interaction, including Escape restoration.
	if err := chromedp.Run(ctx, chromedp.Click("navToggle", chromedp.ByID), chromedp.Sleep(250*time.Millisecond)); err != nil {
		t.Fatalf("open mobile navigation: %v", err)
	}
	var navOpen bool
	_ = browserEvaluate(ctx, `document.querySelector('#sidebar-nav').classList.contains('open')`, &navOpen)
	if !navOpen {
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.getElementById('navToggle').click()`, nil), chromedp.Sleep(100*time.Millisecond)); err != nil {
			t.Fatalf("fallback open mobile navigation: %v", err)
		}
	}
	assertDashboardBrowser(t, ctx, `document.querySelector('#sidebar-nav').classList.contains('open') && document.querySelector('#navOverlay').classList.contains('open') && document.activeElement.matches('#nav [data-view]')`)
	if err := chromedp.Run(ctx, chromedp.KeyEvent(kb.Escape)); err != nil {
		t.Fatalf("close mobile navigation with Escape: %v", err)
	}
	assertDashboardBrowser(t, ctx, `!document.querySelector('#sidebar-nav').classList.contains('open') && document.activeElement.id === 'navToggle'`)
	if err := chromedp.Run(ctx, chromedp.Click("#navToggle", chromedp.ByID), chromedp.Sleep(250*time.Millisecond)); err != nil {
		t.Fatalf("reopen mobile navigation for overlay dismissal: %v", err)
	}
	if err := chromedp.Run(ctx, chromedp.MouseClickXY(380, 400), chromedp.Sleep(250*time.Millisecond)); err != nil {
		t.Fatalf("dismiss mobile navigation with overlay click: %v", err)
	}
	var navDismissState string
	_ = browserEvaluate(ctx, `JSON.stringify({sidebar:document.querySelector('#sidebar-nav').className,overlay:document.querySelector('#navOverlay').className,active:document.activeElement && document.activeElement.id,point:document.elementFromPoint(380,400)?.id})`, &navDismissState)
	var navClosed bool
	_ = browserEvaluate(ctx, `!document.querySelector('#sidebar-nav').classList.contains('open') && !document.querySelector('#navOverlay').classList.contains('open')`, &navClosed)
	if !navClosed {
		t.Fatalf("mobile overlay dismissal failed: %s", navDismissState)
	}

	// A real pointer click on a visible drawer item must select that view, not
	// land on the overlay beneath it and merely close the drawer.
	if err := chromedp.Run(ctx, chromedp.Click("#navToggle", chromedp.ByID), chromedp.Sleep(250*time.Millisecond)); err != nil {
		t.Fatalf("reopen mobile navigation for item selection: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#sidebar-nav').classList.contains('open')`); err != nil {
		t.Fatalf("mobile navigation did not reopen for item selection: %v", err)
	}
	if err := chromedp.Run(ctx, chromedp.Click(`#nav [data-view="providers"]`, chromedp.ByQuery)); err != nil {
		t.Fatalf("select providers from mobile navigation: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `!document.querySelector('#view-providers').hidden && document.querySelector('#nav [data-view="providers"]').getAttribute('aria-current') === 'page'`); err != nil {
		t.Fatalf("mobile navigation click did not select providers: %v", err)
	}

	// Settings drawer state is loaded through its real APIs. Verify staged
	// cancel, provider enable/disable without navigation, gauge persistence, and
	// keyboard/focus behavior before saving a metric preference.
	if err := chromedp.Run(ctx, chromedp.Focus("settingsBtn", chromedp.ByID)); err != nil {
		t.Fatalf("focus settings button: %v", err)
	}
	if err := browserDOMClick(ctx, "#settingsBtn"); err != nil {
		t.Fatalf("open settings drawer: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#settingsDrawer').classList.contains('open') && document.activeElement.id === 'sheetClose' && document.querySelectorAll('#drawerProviderCards .drawer-provider-card').length >= 3 && document.querySelectorAll('#metricPreferenceProviders .metric-preference-row').length >= 2`); err != nil {
		t.Fatalf("settings drawer did not load: %v", err)
	}
	assertDashboardBrowser(t, ctx, `document.querySelector('#settingsDrawer').getAttribute('aria-hidden') === 'false' && document.activeElement.id === 'sheetClose'`)
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.getElementById('sheetOverlay').click()`, nil), chromedp.Sleep(300*time.Millisecond)); err != nil {
		t.Fatalf("dismiss settings drawer with overlay click: %v", err)
	}
	assertDashboardBrowser(t, ctx, `document.querySelector('#settingsDrawer').getAttribute('aria-hidden') === 'true' && !document.querySelector('#sheetOverlay').classList.contains('open')`)
	if err := chromedp.Run(ctx, chromedp.Focus("settingsBtn", chromedp.ByID)); err != nil {
		t.Fatalf("refocus settings button after overlay dismissal: %v", err)
	}
	if err := chromedp.Run(ctx, chromedp.KeyEvent("s")); err != nil {
		t.Fatalf("open settings with S shortcut: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#settingsDrawer').classList.contains('open') && document.activeElement.id === 'sheetClose'`); err != nil {
		t.Fatalf("S shortcut did not open settings: %v", err)
	}
	if err := chromedp.Run(ctx,
		chromedp.Focus("gaugeModeSwitch", chromedp.ByID),
		chromedp.KeyEvent(" "),
	); err != nil {
		t.Fatalf("toggle gauge mode: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#gaugeModeSwitch').getAttribute('aria-checked') === 'false' && document.querySelector('#metricValueHeader').dataset.gaugeMode === 'remaining'`); err != nil {
		t.Fatalf("remaining gauge mode did not activate in mobile settings: %v", err)
	}
	if err := chromedp.Run(ctx, chromedp.KeyEvent(kb.Escape)); err != nil {
		t.Fatalf("close settings with Escape: %v", err)
	}
	assertDashboardBrowser(t, ctx, `document.querySelector('#settingsDrawer').getAttribute('aria-hidden') === 'true' && document.activeElement.id === 'settingsBtn'`)

	if err := chromedp.Run(ctx, chromedp.Focus("settingsBtn", chromedp.ByID)); err != nil {
		t.Fatalf("refocus settings button: %v", err)
	}
	if err := browserDOMClick(ctx, "#settingsBtn"); err != nil {
		t.Fatalf("reopen settings drawer: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#settingsDrawer').classList.contains('open') && document.activeElement.id === 'sheetClose' && document.querySelectorAll('#metricPreferenceProviders button[role="switch"]').length >= 2`); err != nil {
		t.Fatalf("metric preference controls did not load: %v", err)
	}
	assertDashboardBrowser(t, ctx, `document.querySelector('#metricPreferenceProviders [data-slot="switch"]').tagName === 'BUTTON'`)
	// Reorder controls must be disabled at their list boundaries so the first row
	// offers no "up" and the last row offers no "down".
	assertDashboardBrowser(t, ctx, `Array.from(document.querySelectorAll('#metricPreferenceProviders .metric-preference-list')).every(list => {
		const rows = Array.from(list.children).filter(row => row.classList.contains('metric-preference-row'));
		if (!rows.length) return true;
		const up = row => row.querySelector('.metric-preference-move[aria-label$="위로"]');
		const down = row => row.querySelector('.metric-preference-move[aria-label$="아래로"]');
		return up(rows[0]).disabled && down(rows[rows.length - 1]).disabled &&
			rows.slice(1).every(row => !up(row).disabled) &&
			rows.slice(0, -1).every(row => !down(row).disabled);
	})`)
	var preferenceOrderBefore string
	if err := browserEvaluate(ctx, `Array.from(document.querySelector('.metric-preference-provider .metric-preference-list').children).map(row => row.dataset.metricKey).join('|')`, &preferenceOrderBefore); err != nil {
		t.Fatalf("read metric preference order: %v", err)
	}
	if err := browserDOMClick(ctx, `.metric-preference-provider .metric-preference-move[aria-label$="아래로"]:not(:disabled)`); err != nil {
		t.Fatalf("move metric preference down: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `Array.from(document.querySelector('.metric-preference-provider .metric-preference-list').children).map(row => row.dataset.metricKey).join('|') !== `+fmt.Sprintf("%q", preferenceOrderBefore)); err != nil {
		t.Fatalf("metric preference order did not change: %v", err)
	}
	assertDashboardBrowser(t, ctx, `!document.querySelector('#metricPreferenceSaveButton').disabled && document.querySelector('#prefState').textContent.includes('저장하지 않은')`)
	if err := browserDOMClick(ctx, "#metricPreferenceCancelButton"); err != nil {
		t.Fatalf("cancel reordered metric preference: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `Array.from(document.querySelector('.metric-preference-provider .metric-preference-list').children).map(row => row.dataset.metricKey).join('|') === `+fmt.Sprintf("%q", preferenceOrderBefore)); err != nil {
		t.Fatalf("metric preference order did not restore: %v", err)
	}
	if err := chromedp.Run(ctx,
		chromedp.Focus(`#metricPreferenceProviders button[role="switch"]`, chromedp.ByQuery),
		chromedp.KeyEvent(" "),
	); err != nil {
		t.Fatalf("stage metric preference change with keyboard: %v", err)
	}
	assertDashboardBrowser(t, ctx, `document.activeElement.matches('#metricPreferenceProviders button[role="switch"]') && !document.querySelector('#metricPreferenceSaveButton').disabled && document.querySelector('#prefState').textContent.includes('저장하지 않은')`)
	if err := browserDOMClick(ctx, "#metricPreferenceCancelButton"); err != nil {
		t.Fatalf("cancel staged metric preference: %v", err)
	}
	assertDashboardBrowser(t, ctx, `document.querySelector('#metricPreferenceSaveButton').disabled && document.querySelector('#prefState').textContent === '변경 사항 없음'`)

	// Enable and disable the previously disabled provider without reloading.
	if err := browserDOMClick(ctx, "#drawer-btn-disabled"); err != nil {
		t.Fatalf("enable disabled provider: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#drawer-btn-disabled') && document.querySelector('#drawer-btn-disabled').textContent === '비활성화' && !document.querySelector('[data-provider-id="disabled"]').hidden`); err != nil {
		t.Fatalf("provider enable did not reconcile live DOM: %v", err)
	}
	if err := browserDOMClick(ctx, "#drawer-btn-disabled"); err != nil {
		t.Fatalf("disable provider: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#drawer-btn-disabled') && document.querySelector('#drawer-btn-disabled').textContent === '활성화' && document.querySelector('[data-provider-id="disabled"]').hidden`); err != nil {
		t.Fatalf("provider disable did not reconcile live DOM: %v", err)
	}
	var originallyEnabledProviderButtons []string
	if err := browserEvaluate(ctx, `Array.from(document.querySelectorAll('#drawerProviderCards [data-slot="switch"][aria-checked="true"]')).map(button => button.id)`, &originallyEnabledProviderButtons); err != nil {
		t.Fatalf("read enabled provider controls: %v", err)
	}
	for _, buttonID := range originallyEnabledProviderButtons {
		if err := browserDOMClick(ctx, "#"+buttonID); err != nil {
			t.Fatalf("disable provider through settings %s: %v", buttonID, err)
		}
		if err := waitDashboardBrowser(ctx, fmt.Sprintf(`document.getElementById(%q)?.getAttribute('aria-checked') === 'false'`, buttonID)); err != nil {
			t.Fatalf("provider %s did not disable: %v", buttonID, err)
		}
	}
	assertDashboardBrowser(t, ctx, `document.querySelector('#cardGrid [data-provider-empty-state] [data-slot="alert"]').textContent.includes('표시할 프로바이더가 없습니다')`)
	assertDashboardBrowser(t, ctx, `document.querySelector('#metricTableBody [data-table-empty-state]').textContent.includes('표시할 지표가 없습니다')`)
	for _, buttonID := range originallyEnabledProviderButtons {
		if err := browserDOMClick(ctx, "#"+buttonID); err != nil {
			t.Fatalf("restore provider through settings %s: %v", buttonID, err)
		}
		if err := waitDashboardBrowser(ctx, fmt.Sprintf(`document.getElementById(%q)?.getAttribute('aria-checked') === 'true'`, buttonID)); err != nil {
			t.Fatalf("provider %s did not restore: %v", buttonID, err)
		}
	}

	// Saving is a real PUT followed by the application's reload contract.
	if err := browserDOMClick(ctx, `#metricPreferenceProviders button[role="switch"]`); err != nil {
		t.Fatalf("save metric preference: %v", err)
	}
	if err := browserDOMClick(ctx, "#metricPreferenceSaveButton"); err != nil {
		t.Fatalf("save metric preference: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.readyState === 'complete' && Boolean(document.querySelector('#cardGrid')) && Boolean(document.querySelector('#gaugeModeSwitch'))`); err != nil {
		var reloadState string
		_ = browserEvaluate(ctx, `JSON.stringify({ready:document.readyState,url:location.href,card:Boolean(document.querySelector('#cardGrid')),gauge:Boolean(document.querySelector('#gaugeModeSwitch')),save:document.querySelector('#metricPreferenceSaveButton') && document.querySelector('#metricPreferenceSaveButton').disabled})`, &reloadState)
		t.Fatalf("dashboard did not reload after saving preferences: %v (state=%s)", err, reloadState)
	}

	// Restore a complete UI state from localStorage, then prove a denied
	// storage implementation falls back to safe in-memory defaults on reload.
	if err := browserSetStorageState(ctx, `{"view":"trends","range":"7d","mode":"delta","gaugeMode":"remaining","chartHidden":[],"selectedMetrics":{}}`); err != nil {
		t.Fatalf("seed browser UI state: %v", err)
	}
	if err := browserReload(ctx); err != nil {
		t.Fatalf("reload restored browser state: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `!document.querySelector('#view-trends').hidden && document.querySelector('#rangeTabs [data-range="7d"]').getAttribute('aria-selected') === 'true' && document.querySelector('#modeGroup [data-mode="delta"]').getAttribute('aria-pressed') === 'true' && document.querySelector('#gaugeModeSwitch').getAttribute('aria-checked') === 'false'`); err != nil {
		t.Fatalf("saved browser state was not restored: %v", err)
	}
	storageDeniedScript := `Object.defineProperty(window, 'localStorage', { configurable: true, get() { throw new Error('storage denied'); } });`
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(actionContext context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(storageDeniedScript).Do(actionContext)
		return err
	})); err != nil {
		t.Fatalf("deny browser storage: %v", err)
	}
	if err := browserReload(ctx); err != nil {
		t.Fatalf("reload with denied storage: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `Boolean(document.querySelector('#cardGrid')) && document.querySelector('#view-overview').hidden === false`); err != nil {
		t.Fatalf("storage-denied fallback did not render safely: %v", err)
	}

	// The sidebar collection action remains the single source-of-truth control
	// on mobile; completion polling must refresh the SSR card value.
	var before, after string
	if err := browserEvaluate(ctx, `document.querySelector('[data-provider-id="claude"]').dataset.currentUsage || ''`, &before); err != nil {
		t.Fatalf("read pre-collection usage: %v", err)
	}
	server.SetCollector(collector.NewCollector(server.store, server.openusage, time.Minute, server.logger))
	if err := chromedp.Run(ctx, chromedp.Click("#navToggle", chromedp.ByID), chromedp.Sleep(250*time.Millisecond)); err != nil {
		t.Fatalf("open mobile navigation for collection: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#sidebar-nav').classList.contains('open')`); err != nil {
		t.Fatalf("mobile navigation did not open for collection: %v", err)
	}
	if err := chromedp.Run(ctx, chromedp.KeyEvent("r")); err != nil {
		t.Fatalf("trigger collection with R shortcut: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#collectBtn').dataset.collectionState === 'completed' && document.querySelector('#collectBtn').disabled === false`); err != nil {
		t.Fatalf("collection polling did not reach completion: %v", err)
	}
	if err := browserEvaluate(ctx, `document.querySelector('[data-provider-id="claude"]').dataset.currentUsage || ''`, &after); err != nil {
		t.Fatalf("read post-collection usage: %v", err)
	}
	if before == after {
		t.Fatalf("collection completion did not refresh dashboard usage: before=%q after=%q calls=%d", before, after, collectionCalls.Load())
	}
}

func setupDashboardBrowserServer(t *testing.T) (*Server, *httptest.Server, *atomic.Int64) {
	t.Helper()
	server, _ := setupMetricPreferenceTestServer(t)
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(3 * time.Hour)
	limit := 100.0
	claudeID := mustCreateHTTPTestProvider(t, server, "claude", `{}`)
	kiroID := mustCreateHTTPTestProvider(t, server, "kirocli", `{}`)
	disabledID := mustCreateHTTPTestProvider(t, server, "disabled", `{}`)
	if err := server.store.EnableProviderByName("disabled", false); err != nil {
		t.Fatalf("disable browser fixture provider: %v", err)
	}

	boundaryAt := browserBoundaryTimestamp()
	for _, point := range []struct {
		at   time.Time
		used float64
	}{
		{now.Add(-29 * 24 * time.Hour), 10},
		{now.Add(-8 * 24 * time.Hour), 20},
		{now.Add(-6 * time.Hour), 80},
		{now.Add(-4 * time.Hour), 5},
		{now.Add(-2 * time.Hour), 48},
		{now, 72},
	} {
		mustCreateHTTPTestSnapshot(t, server, &store.UsageSnapshot{ProviderID: claudeID, Metric: "session", Used: point.used, Limit: &limit, ResetAt: &reset, CollectedAt: point.at})
	}
	for _, point := range []struct {
		at   time.Time
		used float64
	}{
		{now.Add(-29 * 24 * time.Hour), 100},
		{now.Add(-8 * 24 * time.Hour), 200},
		{now.Add(-2 * time.Hour), 300},
		// A valid zero latest usage must hydrate over the non-zero SSR value.
		{now, 0},
	} {
		mustCreateHTTPTestSnapshot(t, server, &store.UsageSnapshot{ProviderID: claudeID, Metric: "weekly", Used: point.used, Limit: ptrBrowserFloat(1000), ResetAt: ptrBrowserTime(now.Add(5 * 24 * time.Hour)), CollectedAt: point.at})
	}
	for _, point := range []struct {
		at   time.Time
		used float64
	}{
		{boundaryAt.Add(-8 * 24 * time.Hour), 12},
		{boundaryAt.Add(-2 * time.Hour), 25},
		{boundaryAt, 36},
	} {
		mustCreateHTTPTestSnapshot(t, server, &store.UsageSnapshot{ProviderID: kiroID, Metric: "credits", Used: point.used, Limit: ptrBrowserFloat(100), ResetAt: ptrBrowserTime(boundaryAt.Add(10 * 24 * time.Hour)), CollectedAt: point.at})
	}
	mustCreateHTTPTestSnapshot(t, server, &store.UsageSnapshot{ProviderID: kiroID, Metric: "unlimited", Used: 42, CollectedAt: boundaryAt})
	mustCreateHTTPTestSnapshot(t, server, &store.UsageSnapshot{ProviderID: disabledID, Metric: "session", Used: 18, Limit: &limit, ResetAt: &reset, CollectedAt: now})
	errorMessage := "browser fixture collection error"
	if err := server.store.UpdateProviderStatus(claudeID, &errorMessage); err != nil {
		t.Fatalf("set browser fixture provider error: %v", err)
	}

	var collectionCalls atomic.Int64
	openUsageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/usage" {
			http.NotFound(w, r)
			return
		}
		callNumber := collectionCalls.Add(1)
		resetText := time.Now().UTC().Add(3 * time.Hour).Format(time.RFC3339)
		payload := []openusage.UsageSnapshot{{
			ProviderID:  "claude",
			DisplayName: "Claude",
			FetchedAt:   time.Now().UTC(),
			Lines: []openusage.Line{{
				Type:     "progress",
				Label:    "Session",
				Used:     88,
				Limit:    100,
				ResetsAt: &resetText,
			}, {
				Type:  "progress",
				Label: "Weekly",
				Used:  440 + float64(callNumber)*10,
				Limit: 1000,
			}},
		}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	server.SetOpenUsageClient(openusage.NewClient(openUsageServer.URL))
	server.SetCollector(collector.NewCollector(server.store, openusage.NewClient(openUsageServer.URL), time.Minute, server.logger))
	t.Cleanup(openUsageServer.Close)

	return server, httptest.NewServer(server.mux), &collectionCalls
}

// TestDashboardStaleStatusShouldRecoverAfterCollectionAndIgnoreDisabledProviders
// drives the real client refresh path, because the staleness badge and the
// sidebar health line are computed in the browser rather than server-side.
func TestDashboardStaleStatusShouldRecoverAfterCollectionAndIgnoreDisabledProviders(t *testing.T) {
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

	// Given: an enabled provider whose newest snapshot is older than the stale
	// threshold, plus a disabled provider that was last collected months ago.
	server, _ := setupMetricPreferenceTestServer(t)
	now := time.Now().UTC().Truncate(time.Second)
	limit := 100.0
	reset := now.Add(3 * time.Hour)
	activeID := mustCreateHTTPTestProvider(t, server, "claude", `{}`)
	dormantID := mustCreateHTTPTestProvider(t, server, "dormant", `{}`)
	if err := server.store.EnableProviderByName("dormant", false); err != nil {
		t.Fatalf("disable dormant provider: %v", err)
	}
	mustCreateHTTPTestSnapshot(t, server, &store.UsageSnapshot{ProviderID: activeID, Metric: "session", Used: 30, Limit: &limit, ResetAt: &reset, CollectedAt: now.Add(-3 * time.Hour)})
	mustCreateHTTPTestSnapshot(t, server, &store.UsageSnapshot{ProviderID: dormantID, Metric: "session", Used: 18, Limit: &limit, ResetAt: &reset, CollectedAt: now.Add(-60 * 24 * time.Hour)})

	openUsageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/usage" {
			http.NotFound(w, r)
			return
		}
		resetText := time.Now().UTC().Add(3 * time.Hour).Format(time.RFC3339)
		payload := []openusage.UsageSnapshot{{
			ProviderID:  "claude",
			DisplayName: "Claude",
			FetchedAt:   time.Now().UTC(),
			Lines: []openusage.Line{{
				Type:     "progress",
				Label:    "Session",
				Used:     55,
				Limit:    100,
				ResetsAt: &resetText,
			}},
		}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer openUsageServer.Close()
	server.SetOpenUsageClient(openusage.NewClient(openUsageServer.URL))
	server.SetCollector(collector.NewCollector(server.store, openusage.NewClient(openUsageServer.URL), time.Minute, server.logger))

	localServer := httptest.NewServer(server.mux)
	defer localServer.Close()

	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(),
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-extensions", true),
	)
	defer cancelAllocator()
	ctx, cancelContext := chromedp.NewContext(allocator)
	defer cancelContext()
	ctx, cancelTimeout := context.WithTimeout(ctx, 90*time.Second)
	defer cancelTimeout()
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.Navigate(localServer.URL),
		chromedp.WaitReady("#cardGrid"),
	); err != nil {
		t.Fatalf("navigate dashboard in Chrome: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#overviewStatus').textContent === 'API 연결됨'`); err != nil {
		t.Fatalf("dashboard did not finish its first client refresh: %v", err)
	}

	// Then: the genuinely stale provider and the sidebar both report staleness.
	assertDashboardBrowser(t, ctx, `document.querySelector('[data-provider-id="claude"] .provider-status').textContent.trim() === '데이터 오래됨'`)
	assertDashboardBrowser(t, ctx, `document.getElementById('healthText').textContent === '데이터 오래됨'`)

	// When: the user collects fresh data with the dashboard still open.
	if err := browserEvaluate(ctx, `document.getElementById('collectBtn').click()`, nil); err != nil {
		t.Fatalf("click collect button: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.getElementById('collectBtn').dataset.collectionState === 'completed'`); err != nil {
		t.Fatalf("collection did not complete: %v", err)
	}

	// Then: the provider badge clears without a page reload, and the disabled
	// provider's months-old snapshot does not hold the sidebar in a warning
	// state.
	if err := waitDashboardBrowser(ctx, `document.querySelector('[data-provider-id="claude"] .provider-status').textContent.trim() === '정상'`); err != nil {
		t.Fatalf("stale provider badge did not recover after collection: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.getElementById('healthText').textContent === '수집 정상'`); err != nil {
		t.Fatalf("sidebar health did not recover after collection: %v", err)
	}
}

// TestDashboardRemainingModeShowsProjectionOverlay verifies the client-side
// projection UX: after hydration the provider badge reads "한도 임박" from the
// worst metric severity, and in remaining mode a weak metric's projection
// hatch stays visible (not hidden) with the `overlay` class so it reads on
// top of the remaining-amount fill instead of vanishing behind it.
func TestDashboardRemainingModeShowsProjectionOverlay(t *testing.T) {
	chromePath := os.Getenv("WEBUSAGE_CHROME_BIN")
	if chromePath == "" {
		chromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	}
	if _, err := os.Stat(chromePath); err != nil {
		t.Skipf("Chrome executable %q is unavailable: %v", chromePath, err)
	}

	// Given: claude session(투영 120%, danger) + weekly(reset 직후, weak, 투영 168%).
	server, cleanup := setupTestServer(t)
	defer cleanup()
	providerID := mustCreateHTTPTestProvider(t, server, "claude", `{}`)
	mustEnableHTTPTestProvider(t, server, "claude")
	limit := 100.0
	now := time.Now().UTC()
	mustCreateHTTPTestSnapshots(t, server, []*store.UsageSnapshot{
		{ProviderID: providerID, Metric: "session", Used: 20, Limit: &limit, CollectedAt: now.Add(-2 * time.Hour), ResetAt: ptrTime(now.Add(3 * time.Hour))},
		{ProviderID: providerID, Metric: "session", Used: 60, Limit: &limit, CollectedAt: now, ResetAt: ptrTime(now.Add(3 * time.Hour))},
		{ProviderID: providerID, Metric: "weekly", Used: 4, Limit: &limit, CollectedAt: now.Add(-4 * time.Hour), ResetAt: ptrTime(now.Add(160 * time.Hour))},
		{ProviderID: providerID, Metric: "weekly", Used: 8, Limit: &limit, CollectedAt: now, ResetAt: ptrTime(now.Add(160 * time.Hour))},
	})
	localServer := httptest.NewServer(server.mux)
	defer localServer.Close()

	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(),
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-extensions", true),
	)
	defer cancelAllocator()
	ctx, cancelContext := chromedp.NewContext(allocator)
	defer cancelContext()
	ctx, cancelTimeout := context.WithTimeout(ctx, 90*time.Second)
	defer cancelTimeout()

	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.Navigate(localServer.URL),
		chromedp.WaitReady("#cardGrid"),
	); err != nil {
		t.Fatalf("navigate dashboard in Chrome: %v", err)
	}
	// 남은량 기준으로 리로드해 applyGaugeMode가 남은량 경로를 타게 한다.
	if err := browserSetStorageState(ctx, `{"gaugeMode":"remaining"}`); err != nil {
		t.Fatalf("set remaining gauge mode: %v", err)
	}
	if err := browserReload(ctx); err != nil {
		t.Fatalf("reload dashboard in remaining mode: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#overviewStatus').textContent === 'API 연결됨'`); err != nil {
		t.Fatalf("dashboard did not finish its remaining-mode refresh: %v", err)
	}

	// Then: 카드 배지는 worst danger 세션에서 "한도 임박", 약한 weekly 빗금은
	// 숨겨지지 않고 overlay 클래스로 채움 위에 보인다.
	assertDashboardBrowser(t, ctx, `document.querySelector('[data-provider-id="claude"] .provider-status').textContent.trim() === '한도 임박'`)
	assertDashboardBrowser(t, ctx, `(() => {
		const metric = document.querySelector('[data-provider-id="claude"] [data-metric="weekly"]');
		const projection = metric && metric.querySelector('.gauge-proj');
		return projection && !projection.hidden && projection.classList.contains('overlay') && projection.classList.contains('weak');
	})()`)
}

// TestDashboardHealthStatusDoesNotMistakeSeverityForFailure ensures the
// sidebar health status reports "수집 정상" when a provider is at danger
// severity (한도 임박) but has no collection error — the danger badge must
// not be counted as a collection failure (which would surface the empty-title
// "알 수 없는 오류가 발생했습니다." fallback).
func TestDashboardHealthStatusDoesNotMistakeSeverityForFailure(t *testing.T) {
	chromePath := os.Getenv("WEBUSAGE_CHROME_BIN")
	if chromePath == "" {
		chromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	}
	if _, err := os.Stat(chromePath); err != nil {
		t.Skipf("Chrome executable %q is unavailable: %v", chromePath, err)
	}

	// Given: claude session(투영 120%, danger) + weekly(weak). 수집 에러는 없다.
	server, cleanup := setupTestServer(t)
	defer cleanup()
	providerID := mustCreateHTTPTestProvider(t, server, "claude", `{}`)
	mustEnableHTTPTestProvider(t, server, "claude")
	limit := 100.0
	now := time.Now().UTC()
	mustCreateHTTPTestSnapshots(t, server, []*store.UsageSnapshot{
		{ProviderID: providerID, Metric: "session", Used: 20, Limit: &limit, CollectedAt: now.Add(-2 * time.Hour), ResetAt: ptrTime(now.Add(3 * time.Hour))},
		{ProviderID: providerID, Metric: "session", Used: 60, Limit: &limit, CollectedAt: now, ResetAt: ptrTime(now.Add(3 * time.Hour))},
		{ProviderID: providerID, Metric: "weekly", Used: 4, Limit: &limit, CollectedAt: now.Add(-4 * time.Hour), ResetAt: ptrTime(now.Add(160 * time.Hour))},
		{ProviderID: providerID, Metric: "weekly", Used: 8, Limit: &limit, CollectedAt: now, ResetAt: ptrTime(now.Add(160 * time.Hour))},
	})
	localServer := httptest.NewServer(server.mux)
	defer localServer.Close()

	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(),
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-extensions", true),
	)
	defer cancelAllocator()
	ctx, cancelContext := chromedp.NewContext(allocator)
	defer cancelContext()
	ctx, cancelTimeout := context.WithTimeout(ctx, 90*time.Second)
	defer cancelTimeout()

	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.Navigate(localServer.URL),
		chromedp.WaitReady("#cardGrid"),
	); err != nil {
		t.Fatalf("navigate dashboard in Chrome: %v", err)
	}
	if err := waitDashboardBrowser(ctx, `document.querySelector('#overviewStatus').textContent === 'API 연결됨'`); err != nil {
		t.Fatalf("dashboard did not finish its first client refresh: %v", err)
	}

	// Then: 사이드바 상태는 수집 실패가 아니라 정상이어야 하고, 한도 임박(danger)
	// 배지의 빈 title이 "알 수 없는 오류"로 표시되지 않아야 한다.
	assertDashboardBrowser(t, ctx, `document.getElementById('healthText').textContent === '수집 정상'`)
	assertDashboardBrowser(t, ctx, `!document.getElementById('healthNote').textContent.includes('알 수 없는 오류')`)
}

func ptrBrowserFloat(value float64) *float64 { return &value }

func ptrBrowserTime(value time.Time) *time.Time { return &value }

func browserBoundaryTimestamp() time.Time {
	now := time.Now().UTC()
	boundary := time.Date(now.Year(), now.Month(), now.Day(), 15, 30, 0, 0, time.UTC)
	if boundary.After(now) {
		boundary = boundary.Add(-24 * time.Hour)
	}
	return boundary
}

func browserBoundaryDateText(value time.Time) string {
	kst := value.Add(9 * time.Hour)
	return fmt.Sprintf("%d월 %d일 00:30", kst.Month(), kst.Day())
}

func waitDashboardBrowser(ctx context.Context, expression string) error {
	deadline := time.Now().Add(15 * time.Second)
	var ready bool
	for time.Now().Before(deadline) {
		if err := browserEvaluate(ctx, expression, &ready); err == nil && ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for browser condition: %s", expression)
}

func assertDashboardBrowser(t *testing.T, ctx context.Context, expression string) {
	t.Helper()
	var result bool
	if err := browserEvaluate(ctx, expression, &result); err != nil {
		t.Fatalf("evaluate browser assertion %s: %v", expression, err)
	}
	if !result {
		t.Fatalf("browser assertion failed: %s", expression)
	}
}

func browserEvaluate(ctx context.Context, expression string, result interface{}) error {
	return chromedp.Run(ctx, chromedp.Evaluate(expression, result))
}

func browserDOMClick(ctx context.Context, selector string) error {
	key := kb.Enter
	if strings.Contains(selector, "checkbox") {
		key = " "
	}
	return chromedp.Run(ctx,
		chromedp.ScrollIntoView(selector, chromedp.ByQuery),
		chromedp.Focus(selector, chromedp.ByQuery),
		chromedp.KeyEvent(key),
	)
}

func browserReload(ctx context.Context) error {
	err := chromedp.Run(ctx, chromedp.Reload())
	if err != nil && !strings.Contains(err.Error(), "ERR_ABORTED") && !strings.Contains(err.Error(), "Not attached to an active page") {
		return err
	}
	return nil
}

func browserSetStorageState(ctx context.Context, state string) error {
	return browserEvaluate(ctx, fmt.Sprintf(`localStorage.setItem('webusage.ui.v1', %q)`, strings.ReplaceAll(state, "`", "\\`")), nil)
}
