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

// TestDashboardVisualParity is the deterministic design-file oracle. It loads
// the supplied design source and the live SSR dashboard in the same Chrome
// context, at the same viewport dimensions, activates every applicable view,
// and compares stable shell nodes. Provider values, timestamps, API-owned
// status text, and fixture-dependent cardinality are intentionally masked;
// layout, typography, colour, spacing, borders, geometry, visibility,
// responsive state, repeated card/metric structure, and stable product copy
// remain observable.
func TestDashboardVisualParity(t *testing.T) {
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
	designPath, err := os.Getwd()
	if err != nil {
		t.Fatalf("get dashboard test working directory: %v", err)
	}
	designURL := "file://" + designPath + "/../../webusage-dashboard.html"

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

	for _, viewport := range []struct {
		name   string
		width  int
		height int
	}{
		{name: "desktop", width: 1440, height: 1000},
		{name: "mobile", width: 390, height: 844},
	} {
		t.Run(viewport.name, func(t *testing.T) {
			states := []string{"overview", "overview-remaining", "overview-empty", "providers", "providers-sorted", "providers-empty", "trends", "trends-delta", "trends-chip-disabled", "trends-empty", "activity", "settings", "settings-dirty", "collection-toast"}
			if viewport.name == "mobile" {
				states = append(states, "mobile-nav")
			}
			for _, state := range states {
				t.Run(state, func(t *testing.T) {
					if err := chromedp.Run(ctx, chromedp.EmulateViewport(int64(viewport.width), int64(viewport.height))); err != nil {
						t.Fatalf("set %s viewport: %v", viewport.name, err)
					}
					if err := navigateDashboardVisual(ctx, designURL); err != nil {
						t.Fatalf("navigate design source: %v", err)
					}
					if err := prepareDashboardVisualState(ctx, true, state); err != nil {
						t.Fatalf("activate design %s state: %v", state, err)
					}
					design := captureDashboardVisual(t, ctx, true, state)

					if err := navigateDashboardVisual(ctx, localServer.URL); err != nil {
						t.Fatalf("navigate live dashboard: %v", err)
					}
					if err := prepareDashboardVisualState(ctx, false, state); err != nil {
						t.Fatalf("activate live %s state: %v", state, err)
					}
					live := captureDashboardVisual(t, ctx, false, state)
					compareDashboardVisual(t, viewport.name, state, design, live)
				})
			}
		})
	}
}

func navigateDashboardVisual(ctx context.Context, url string) error {
	if err := chromedp.Run(ctx, chromedp.Navigate(url), chromedp.WaitReady("#cardGrid")); err != nil {
		return err
	}
	var cleared bool
	if err := browserEvaluate(ctx, `(() => { try { localStorage.removeItem('webusage.ui.v1'); } catch (error) {} return true; })()`, &cleared); err != nil {
		return err
	}
	if err := chromedp.Run(ctx, chromedp.Reload(), chromedp.WaitReady("#cardGrid")); err != nil {
		return err
	}
	return waitDashboardBrowser(ctx, `document.readyState === 'complete' && document.querySelectorAll('#cardGrid [data-slot="card"]').length > 0`)
}

func prepareDashboardVisualState(ctx context.Context, design bool, state string) error {
	switch state {
	case "overview":
		return nil
	case "overview-empty", "providers-empty":
		if design {
			if err := prepareDashboardVisualState(ctx, true, "settings"); err != nil {
				return err
			}
			for {
				var enabled int
				if err := browserEvaluate(ctx, `document.querySelectorAll('#providerToggles [data-slot="switch"][aria-checked="true"]').length`, &enabled); err != nil {
					return err
				}
				if enabled == 0 {
					break
				}
				if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('#providerToggles [data-slot="switch"][aria-checked="true"]').click()`, nil)); err != nil {
					return err
				}
			}
			if err := chromedp.Run(ctx, chromedp.Evaluate(`(() => { document.getElementById('sheet').classList.remove('open'); document.getElementById('sheetOverlay').classList.remove('open'); document.getElementById('toaster').replaceChildren(); })()`, nil)); err != nil {
				return err
			}
			if err := chromedp.Run(ctx, chromedp.Sleep(350*time.Millisecond)); err != nil {
				return err
			}
		} else {
			if err := chromedp.Run(ctx, chromedp.Evaluate(`(() => { const grid = document.getElementById('cardGrid'); grid.querySelectorAll('.provider-card').forEach(card => card.hidden = true); grid.querySelector('[data-provider-empty-state]')?.remove(); grid.insertAdjacentHTML('beforeend', '<div data-slot="card" data-provider-empty-state style="grid-column:1/-1"><div data-slot="card-content"><div data-slot="alert"><svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><use href="#i-alert"/></svg><div><div class="at">표시할 프로바이더가 없습니다</div>설정에서 프로바이더를 하나 이상 활성화하세요.</div></div></div></div>'); const body = document.getElementById('metricTableBody'); body.querySelectorAll('tr').forEach(row => row.remove()); const emptyRow = document.createElement('tr'); emptyRow.dataset.tableEmptyState = 'true'; const cell = document.createElement('td'); cell.colSpan = 8; cell.style.padding = 'var(--space-8)'; cell.style.textAlign = 'center'; cell.style.color = 'var(--muted)'; cell.textContent = '표시할 지표가 없습니다. 설정에서 프로바이더나 지표를 켜세요.'; emptyRow.appendChild(cell); body.appendChild(emptyRow); document.getElementById('tableCount').textContent = '0개 지표 · 0개 프로바이더'; })()`, nil)); err != nil {
				return err
			}
		}
		if err := waitDashboardBrowser(ctx, `Boolean(document.querySelector('#cardGrid > [data-provider-empty-state], #cardGrid > [data-slot="card"]:only-child [data-slot="alert"]'))`); err != nil {
			return err
		}
		if state == "providers-empty" {
			if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('#nav [data-view="providers"]').click()`, nil)); err != nil {
				return err
			}
			return waitDashboardBrowser(ctx, `!document.querySelector('#view-providers').hidden && Boolean(document.querySelector('#metricTableBody tr:only-child'))`)
		}
		return nil
	case "overview-remaining":
		if err := chromedp.Run(ctx, chromedp.Click("#settingsBtn", chromedp.ByID)); err != nil {
			return err
		}
		switchSelector := "#gaugeModeSwitch"
		if design {
			switchSelector = "#gaugeSwitch"
		}
		if err := waitDashboardBrowser(ctx, fmt.Sprintf(`document.querySelector(%q) && document.querySelector(%q).getAttribute('aria-checked') === 'true'`, switchSelector, switchSelector)); err != nil {
			return err
		}
		if err := chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`document.querySelector(%q).click()`, switchSelector), nil)); err != nil {
			return err
		}
		if err := waitDashboardBrowser(ctx, fmt.Sprintf(`document.querySelector(%q).getAttribute('aria-checked') === 'false'`, switchSelector)); err != nil {
			return err
		}
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.getElementById('sheetClose').click()`, nil)); err != nil {
			return err
		}
		return waitDashboardBrowser(ctx, `!document.querySelector('#settingsDrawer, #sheet').classList.contains('open')`)
	case "providers":
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('#nav [data-view="providers"]').click()`, nil)); err != nil {
			return err
		}
		return waitDashboardBrowser(ctx, `!document.querySelector('#view-providers').hidden && document.querySelectorAll('#metricTableBody tr').length > 0`)
	case "providers-sorted":
		if err := prepareDashboardVisualState(ctx, design, "providers"); err != nil {
			return err
		}
		return chromedp.Run(ctx, chromedp.Click(`#metricTable th[data-sort="used"]`, chromedp.ByQuery), chromedp.Sleep(100*time.Millisecond))
	case "trends":
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('#nav [data-view="trends"]').click()`, nil)); err != nil {
			return err
		}
		return waitDashboardBrowser(ctx, `!document.querySelector('#view-trends').hidden && document.querySelectorAll('#chipRow > *').length > 0 && Boolean(document.querySelector('#chart, #trendChart'))`)
	case "trends-delta":
		if err := prepareDashboardVisualState(ctx, design, "trends"); err != nil {
			return err
		}
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('#modeGroup [data-mode="delta"]').click()`, nil)); err != nil {
			return err
		}
		return waitDashboardBrowser(ctx, `document.querySelector('#modeGroup [data-mode="delta"]').getAttribute('aria-pressed') === 'true'`)
	case "trends-chip-disabled":
		if err := prepareDashboardVisualState(ctx, design, "trends"); err != nil {
			return err
		}
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('#chipRow [data-slot="chip"][aria-pressed="true"]').click()`, nil)); err != nil {
			return err
		}
		return waitDashboardBrowser(ctx, `document.querySelector('#chipRow [data-slot="chip"]').getAttribute('aria-pressed') === 'false'`)
	case "trends-empty":
		if err := prepareDashboardVisualState(ctx, design, "trends"); err != nil {
			return err
		}
		for {
			var enabled int
			if err := browserEvaluate(ctx, `document.querySelectorAll('#chipRow [data-slot="chip"][aria-pressed="true"]').length`, &enabled); err != nil {
				return err
			}
			if enabled == 0 {
				break
			}
			if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('#chipRow [data-slot="chip"][aria-pressed="true"]').click()`, nil)); err != nil {
				return err
			}
		}
		chartSelector := "#trendChart"
		if design {
			chartSelector = "#chart"
		}
		return waitDashboardBrowser(ctx, fmt.Sprintf(`document.querySelector(%q).textContent.includes('이 구간에 수집된 스냅샷이 없습니다')`, chartSelector))
	case "activity":
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('#nav [data-view="activity"]').click()`, nil)); err != nil {
			return err
		}
		return waitDashboardBrowser(ctx, `!document.querySelector('#view-activity').hidden && document.querySelectorAll('#heatmap .hm-cell').length >= 24`)
	case "settings":
		if err := chromedp.Run(ctx, chromedp.Click("#settingsBtn", chromedp.ByID)); err != nil {
			return err
		}
		selector := "#settingsDrawer"
		if design {
			selector = "#sheet"
		}
		if err := waitDashboardBrowser(ctx, fmt.Sprintf(`document.querySelector(%q).classList.contains('open') && document.activeElement.id === 'sheetClose'`, selector)); err != nil {
			return err
		}
		return chromedp.Run(ctx, chromedp.Sleep(350*time.Millisecond))
	case "settings-dirty":
		if err := prepareDashboardVisualState(ctx, design, "settings"); err != nil {
			return err
		}
		selector := `#metricPreferenceProviders [data-slot="switch"]`
		if design {
			selector = `#prefEditor [data-slot="switch"]`
		}
		if err := waitDashboardBrowser(ctx, fmt.Sprintf(`Boolean(document.querySelector(%q))`, selector)); err != nil {
			return err
		}
		if err := chromedp.Run(ctx, chromedp.Click(selector, chromedp.ByQuery)); err != nil {
			return err
		}
		return waitDashboardBrowser(ctx, `document.querySelector('#prefState').textContent.includes('저장하지 않은')`)
	case "collection-toast":
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.getElementById('collectBtn').click()`, nil)); err != nil {
			return err
		}
		if err := waitDashboardBrowser(ctx, `document.querySelectorAll('.toaster [data-slot="toast"]').length >= 1`); err != nil {
			return err
		}
		return chromedp.Run(ctx, chromedp.Sleep(350*time.Millisecond))
	case "mobile-nav":
		if err := chromedp.Run(ctx, chromedp.Click("#navToggle", chromedp.ByID)); err != nil {
			return err
		}
		if err := waitDashboardBrowser(ctx, `document.querySelector('#sidebar-nav').classList.contains('open') && document.querySelector('#navOverlay').classList.contains('open')`); err != nil {
			return err
		}
		// Let the 300ms drawer slide settle so the trigger it covers is compared
		// in its resting state rather than mid-transition.
		return chromedp.Run(ctx, chromedp.Sleep(350*time.Millisecond))
	default:
		return fmt.Errorf("unknown visual state %q", state)
	}
}

type dashboardVisualSnapshot struct {
	State    string `json:"state"`
	Viewport struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"viewport"`
	Nodes    map[string]dashboardVisualNode   `json:"nodes"`
	Repeated map[string][]dashboardVisualNode `json:"repeated"`
	Copy     map[string]string                `json:"copy"`
	Order    []string                         `json:"order"`
}

type dashboardVisualNode struct {
	Present  bool               `json:"present"`
	Visible  bool               `json:"visible"`
	Text     string             `json:"text"`
	Style    map[string]string  `json:"style"`
	Geometry map[string]float64 `json:"geometry"`
}

func captureDashboardVisual(t *testing.T, ctx context.Context, design bool, state string) dashboardVisualSnapshot {
	t.Helper()
	mappings := []string{
		"body|body|body",
		"app|.app|.app",
		"sidebar|[data-slot=sidebar]|[data-slot=sidebar]",
		"topbar|[data-slot=topbar]|[data-slot=topbar]",
		"view-overview|#view-overview|#view-overview",
		"view-providers|#view-providers|#view-providers",
		"view-trends|#view-trends|#view-trends",
		"view-activity|#view-activity|#view-activity",
		"overview-head|#view-overview .sec-head|#view-overview .sec-head",
		"overview-title|#view-overview .sec-title|#view-overview .sec-title",
		"overview-desc|#view-overview .sec-desc|#view-overview .sec-desc",
		"overview-legend|#overviewLegend|#overviewLegend",
		"card-grid|#cardGrid|#cardGrid",
		"overview-empty-card|#cardGrid > [data-slot=card]:only-child|#cardGrid > [data-provider-empty-state]",
		"overview-empty-alert|#cardGrid > [data-slot=card]:only-child [data-slot=alert]|#cardGrid > [data-provider-empty-state] [data-slot=alert]",
		"providers-head|#view-providers > .sec-head|#view-providers > .sec-head",
		"providers-table-head|#metricTable thead|#metricTable thead",
		"providers-card|#view-providers > [data-slot=card]|#view-providers > [data-slot=card]",
		"providers-empty-row|#metricTableBody tr:only-child|#metricTableBody [data-table-empty-state]",
		"trends-head|#view-trends > .sec-head|#view-trends > .sec-head",
		"trends-card|#view-trends > [data-slot=card]|#view-trends > [data-slot=card]",
		"trends-mode|#modeGroup|#modeGroup",
		"trends-range|#rangeTabs|#rangeTabs",
		"trends-title|#chartTitle|#chartTitle",
		"trends-desc|#chartDesc|#chartDesc",
		"trends-badge|#view-trends > [data-slot=card] [data-slot=badge]|#trendDataStatus",
		"trends-footer|#chartFoot|#chartFoot",
		"trends-empty-primary|#chart .axis-text:first-of-type|#trendChart .axis-text:first-of-type",
		"trends-empty-secondary|#chart .axis-text:last-of-type|#trendChart .axis-text:last-of-type",
		"activity-head|#view-activity > .sec-head|#view-activity > .sec-head",
		"activity-card|#view-activity > [data-slot=card]|#view-activity > [data-slot=card]",
		"activity-total|#hmTotal|#hmTotal",
		"activity-footer|#hmFoot|#hmFoot",
		"activity-alert|#activityAlert|#activityAlert",
		"sheet|#sheet|#settingsDrawer",
		"sheet-overlay|#sheetOverlay|#sheetOverlay",
		"nav-overlay|#navOverlay|#navOverlay",
		"sheet-header|#sheet [data-slot=sheet-header]|#settingsDrawer [data-slot=sheet-header]",
		"sheet-body|#sheet [data-slot=sheet-body]|#settingsDrawer [data-slot=sheet-body]",
		"sheet-footer|#sheet [data-slot=sheet-footer]|#settingsDrawer [data-slot=sheet-footer]",
		"provider-settings|#providerToggles|#drawerProviderCards",
		"preference-settings|#prefEditor|#metricPreferenceProviders",
		"sidebar-menu|#nav|#nav",
		"nav-button|[data-slot=sidebar-menu-button]|[data-slot=sidebar-menu-button]",
		"sidebar-trigger|#navToggle|#navToggle",
		"settings-action|#settingsBtn|#settingsBtn",
		"collect-button|#collectBtn|#collectBtn",
		"collect-shortcut|#collectBtn kbd|#collectBtn kbd",
		"snapshot-note|.snapshot-note|.snapshot-note",
		"health-note|#healthNote|#healthNote",
		"chart-tooltip|#chartTip|#chartTip",
		"heatmap-legend|.hm-legend|.hm-legend",
		"preference-cancel|#prefCancel|#metricPreferenceCancelButton",
		"preference-save|#prefSave|#metricPreferenceSaveButton",
		"toaster|.toaster|.toaster",
	}
	repeatedMappings := []string{
		"provider-cards|#cardGrid [data-slot=card]|#cardGrid [data-slot=card]",
		"provider-card-headers|#cardGrid [data-slot=card-header]|#cardGrid [data-slot=card-header]",
		"provider-card-titles|#cardGrid [data-slot=card-title]|#cardGrid [data-slot=card-title]",
		"provider-card-descriptions|#cardGrid [data-slot=card-description]|#cardGrid [data-slot=card-description]",
		"provider-card-badges|#cardGrid [data-slot=card-header] [data-slot=badge]|#cardGrid [data-slot=card-header] [data-slot=badge]",
		"provider-card-content|#cardGrid [data-slot=card-content]|#cardGrid [data-slot=card-content]",
		"provider-metrics|#cardGrid .metric|#cardGrid .metric",
		"metric-tops|#cardGrid .metric-top|#cardGrid .metric-top",
		"metric-gauges|#cardGrid [data-slot=gauge]|#cardGrid [data-slot=gauge]",
		"metric-foots|#cardGrid .metric-foot|#cardGrid .metric-foot",
		"provider-footers|#cardGrid [data-slot=card-footer]|#cardGrid [data-slot=card-footer]",
		"provider-table-rows|#metricTableBody tr|#metricTableBody tr",
		"trend-mode-buttons|#modeGroup [data-slot=toggle-group-item]|#modeGroup [data-slot=toggle-group-item]",
		"trend-range-buttons|#rangeTabs [data-slot=tabs-trigger]|#rangeTabs [data-slot=tabs-trigger]",
		"trend-chips|#chipRow [data-slot=chip]|#chipRow [data-slot=chip]",
		"activity-days|#heatmap .hm-day|#heatmap .hm-day",
		"activity-hours|#heatmap .hm-hour|#heatmap .hm-hour",
		"activity-cells|#heatmap .hm-cell|#heatmap .hm-cell",
		"activity-legend-cells|.hm-legend .hm-cell|.hm-legend .hm-cell",
		"metric-cycle-badges|#cardGrid .metric-top [data-slot=badge]|#cardGrid .metric-top [data-slot=badge]",
		"table-mini-gauges|#metricTableBody .mini-gauge|#metricTableBody .mini-gauge",
		"settings-provider-rows|#providerToggles .set-row|#drawerProviderCards .set-row",
		"settings-provider-switches|#providerToggles [data-slot=switch]|#drawerProviderCards [data-slot=switch]",
		"preference-providers|#prefEditor .pref-provider|#metricPreferenceProviders .metric-preference-provider",
		"preference-items|#prefEditor .pref-item|#metricPreferenceProviders .pref-item",
		"preference-switches|#prefEditor [data-slot=switch]|#metricPreferenceProviders [data-slot=switch]",
		"preference-order|#prefEditor .pref-order > *|#metricPreferenceProviders .metric-preference-move",
		"sheet-footer-controls|#sheet [data-slot=sheet-footer] > *|#settingsDrawer [data-slot=sheet-footer] > *",
		"toasts|.toaster [data-slot=toast]|.toaster [data-slot=toast]",
	}
	copyMappings := []string{
		"brand|.brand|.brand",
		"nav|#nav|#nav",
		"collect|#collectBtn|#collectBtn",
		"overview-title|#view-overview .sec-title|#view-overview .sec-title",
		"overview-desc|#view-overview .sec-desc|#view-overview .sec-desc",
		"providers-title|#view-providers .sec-title|#view-providers .sec-title",
		"providers-desc|#view-providers .sec-desc|#view-providers .sec-desc",
		"providers-footer|#view-providers [data-slot=card-footer]|#view-providers [data-slot=card-footer]",
		"trends-title|#view-trends .sec-title|#view-trends .sec-title",
		"trends-desc|#view-trends .sec-desc|#view-trends .sec-desc",
		"trends-card-title|#chartTitle|#chartTitle",
		"trends-card-desc|#chartDesc|#chartDesc",
		"trends-badge|#view-trends > [data-slot=card] [data-slot=badge]|#trendDataStatus",
		"activity-title|#view-activity .sec-title|#view-activity .sec-title",
		"activity-desc|#view-activity .sec-desc|#view-activity .sec-desc",
		"activity-alert-heading|#activityAlert [data-slot=alert] .at|#activityAlert [data-slot=alert] .at",
		"settings-title|#sheet .set-title|#settingsDrawer .set-title",
		"settings-help|#sheet .set-help|#settingsDrawer .set-help",
		"sheet-cancel|#sheet [data-slot=sheet-footer] button:first-of-type|#settingsDrawer [data-slot=sheet-footer] button:first-of-type",
		"sheet-save|#sheet [data-slot=sheet-footer] button:last-of-type|#settingsDrawer [data-slot=sheet-footer] button:last-of-type",
		"page-title|#pageTitle|#pageTitle",
		"page-desc|#pageDesc|#pageDesc",
	}
	mappingJSON, _ := json.Marshal(mappings)
	repeatedJSON, _ := json.Marshal(repeatedMappings)
	copyJSON, _ := json.Marshal(copyMappings)
	designLiteral := "false"
	if design {
		designLiteral = "true"
	}
	expression := fmt.Sprintf(`(() => {
const isDesign = %s;
const mappings = %s;
const repeatedMappings = %s;
const copyMappings = %s;
document.documentElement.style.overflow = 'hidden';
document.body.style.overflow = 'hidden';
const styleKeys = ['display','visibility','position','fontFamily','fontSize','fontWeight','lineHeight','letterSpacing','color','backgroundColor','opacity','borderTopWidth','borderRightWidth','borderBottomWidth','borderLeftWidth','borderTopColor','borderRightColor','borderBottomColor','borderLeftColor','borderRadius','paddingTop','paddingRight','paddingBottom','paddingLeft','marginTop','marginRight','marginBottom','marginLeft','gap','gridTemplateColumns','width','height','minHeight','maxWidth','transform','overflow','transitionProperty','transitionDuration','transitionTimingFunction'];
const cleanText = text => String(text || '').replace(/\s+/g, ' ').trim();
const textMasked = new Set(['body','app','sidebar','topbar','view-overview','view-providers','view-trends','view-activity','overview-head','card-grid','providers-head','providers-table-head','providers-card','trends-head','trends-card','trends-mode','trends-range','trends-footer','activity-head','activity-card','activity-total','activity-footer','activity-alert','sheet','sheet-header','sheet-body','sheet-footer','sidebar-menu','provider-settings','preference-settings','snapshot-note','health-note','chart-tooltip','toaster']);
const repeatedTextMasked = new Set(['provider-cards','provider-card-headers','provider-card-titles','provider-card-descriptions','provider-card-badges','provider-card-content','provider-metrics','metric-tops','metric-foots','provider-table-rows','trend-chips','activity-days','activity-hours','activity-cells','activity-legend-cells','metric-cycle-badges','settings-provider-rows','settings-provider-switches','preference-providers','preference-items','preference-switches','preference-order','toasts']);
const visibleText = node => {
  const clone = node.cloneNode(true);
  clone.querySelectorAll('[hidden],[aria-hidden="true"]').forEach(child => child.remove());
  return cleanText(clone.textContent);
};
const normalizedText = (node, key) => {
  const text = visibleText(node);
  if (textMasked.has(key) || repeatedTextMasked.has(key)) return '';
  if (key === 'provider-footers') return text.replace(/^마지막 수집.*$/, '마지막 수집');
  return text;
};
const describe = (node, key) => {
  if (!node) return {present:false, visible:false, text:'', style:{}, geometry:{}};
  const computed = getComputedStyle(node);
  const rect = node.getBoundingClientRect();
  const style = {};
  styleKeys.forEach(key => style[key] = computed[key]);
  const hidden = node.hidden || computed.display === 'none' || computed.visibility === 'hidden' || node.getClientRects().length === 0;
  return {present:true, visible:!hidden, text:normalizedText(node, key), style, geometry:{x:rect.x,y:rect.y,width:rect.width,height:rect.height}};
};
const nodes = {};
mappings.forEach(pair => {
  const [key, sourceSelector, liveSelector] = pair.split('|');
  const selector = isDesign ? sourceSelector : liveSelector;
  nodes[key] = describe(document.querySelector(selector), key);
});
const repeated = {};
repeatedMappings.forEach(pair => {
  const [key, sourceSelector, liveSelector] = pair.split('|');
  const selector = isDesign ? sourceSelector : liveSelector;
  repeated[key] = Array.from(document.querySelectorAll(selector)).filter(node => node.getClientRects().length > 0).map(node => describe(node, key));
});
const copy = {};
copyMappings.forEach(pair => {
  const [key, sourceSelector, liveSelector] = pair.split('|');
  const node = document.querySelector(isDesign ? sourceSelector : liveSelector);
  if (key === 'brand' && node) {
    copy[key] = ['.brand-mark', '.brand-name', '.brand-sub'].map(selector => cleanText(node.querySelector(selector)?.textContent)).join('|');
  } else if (key === 'nav' && node) {
    copy[key] = Array.from(node.querySelectorAll('[data-view]')).map(button => {
      const clone = button.cloneNode(true);
      clone.querySelectorAll('.count').forEach(count => count.remove());
      return cleanText(clone.textContent);
    }).join('|');
  } else {
    copy[key] = node ? visibleText(node) : '';
  }
});
const order = Array.from(document.querySelectorAll('main.view')).map(node => node.id);
return JSON.stringify({state:%q,viewport:{width:innerWidth,height:innerHeight},nodes,repeated,copy,order});
})()`, designLiteral, string(mappingJSON), string(repeatedJSON), string(copyJSON), state)
	var encoded string
	if err := browserEvaluate(ctx, expression, &encoded); err != nil {
		t.Fatalf("capture %s visual snapshot: %v", map[bool]string{true: "design", false: "live"}[design], err)
	}
	var snapshot dashboardVisualSnapshot
	if err := json.Unmarshal([]byte(encoded), &snapshot); err != nil {
		t.Fatalf("decode %s visual snapshot: %v; raw=%s", map[bool]string{true: "design", false: "live"}[design], err, encoded)
	}
	return snapshot
}

func compareDashboardVisual(t *testing.T, viewport, state string, design, live dashboardVisualSnapshot) {
	t.Helper()
	isTransitionProperty := func(property string) bool {
		return property == "transitionProperty" || property == "transitionDuration" || property == "transitionTimingFunction"
	}
	transitionNodes := map[string]bool{"providers-card": true, "trends-card": true, "activity-card": true, "nav-button": true}
	transitionRepeated := map[string]bool{"provider-cards": true, "trend-mode-buttons": true, "trend-range-buttons": true, "trend-chips": true, "settings-provider-switches": true, "preference-switches": true, "sheet-footer-controls": true}
	// These dimensions are intentionally data-dependent: the live dashboard
	// renders the provider/metric cardinality and labels returned by its APIs,
	// while the source file carries a fixed prototype fixture. All shell and
	// control geometry remains compared below; only the documented data-bearing
	// extents are masked.
	maskedStyle := map[string]map[string]bool{
		"body":                 {"overflow": true},
		"app":                  {"height": true},
		"view-overview":        {"height": true},
		"view-providers":       {"height": true},
		"view-trends":          {"height": true},
		"view-activity":        {"height": true},
		"card-grid":            {"height": true},
		"providers-card":       {"height": true, "width": true},
		"providers-head":       {"height": true, "width": true},
		"providers-table-head": {"height": true, "width": true},
		"trends-card":          {"height": true},
		"activity-card":        {"height": true},
		"activity-total":       {"width": true},
		"activity-footer":      {"height": true},
		"activity-alert":       {"height": true},
		"trends-footer":        {"height": true},
		"sheet":                {"height": true},
		"sheet-body":           {"height": true},
		"provider-settings":    {"height": true},
		"preference-settings":  {"height": true},
		"providers-empty-row":  {"width": true},
		"nav-button":           {"color": true, "backgroundColor": true, "borderTopColor": true, "borderRightColor": true, "borderBottomColor": true, "borderLeftColor": true},
		"health-note":          {"height": true},
		"snapshot-note":        {"width": true},
		"settings-action":      {"backgroundColor": true, "color": true, "borderTopColor": true, "borderRightColor": true, "borderBottomColor": true, "borderLeftColor": true},
		"chart-tooltip":        {"width": true, "height": true},
		"toaster":              {"width": true, "height": true},
	}
	maskedGeometry := map[string]map[string]bool{
		"app":                    {"height": true},
		"view-overview":          {"height": true},
		"view-providers":         {"height": true},
		"view-trends":            {"height": true},
		"view-activity":          {"height": true},
		"card-grid":              {"height": true},
		"providers-card":         {"height": true, "width": true, "y": true},
		"providers-head":         {"height": true, "width": true, "y": true},
		"providers-table-head":   {"height": true, "width": true, "x": true, "y": true},
		"trends-card":            {"height": true},
		"activity-card":          {"height": true},
		"activity-total":         {"x": true, "width": true},
		"activity-footer":        {"height": true, "y": true},
		"activity-alert":         {"height": true, "y": true},
		"trends-footer":          {"height": true, "y": true},
		"sheet":                  {"height": true},
		"sheet-body":             {"height": true, "y": true},
		"provider-settings":      {"height": true, "y": true},
		"preference-settings":    {"height": true, "y": true},
		"providers-empty-row":    {"x": true, "width": true},
		"trends-empty-primary":   {"y": true},
		"trends-empty-secondary": {"y": true},
		"health-note":            {"height": true, "y": true},
		"snapshot-note":          {"width": true, "y": true},
		"chart-tooltip":          {"width": true, "height": true, "x": true, "y": true},
		"heatmap-legend":         {"y": true},
		"preference-cancel":      {"x": true, "y": true},
		"preference-save":        {"x": true, "y": true},
		"toaster":                {"x": true, "y": true, "width": true, "height": true},
	}
	maskedRepeatedStyle := map[string]map[string]bool{
		"provider-card-badges":       {"width": true, "color": true, "backgroundColor": true, "borderTopColor": true, "borderRightColor": true, "borderBottomColor": true, "borderLeftColor": true},
		"provider-cards":             {"height": true, "x": true},
		"provider-card-headers":      {"height": true, "x": true},
		"provider-card-titles":       {"width": true},
		"provider-card-descriptions": {"width": true},
		"provider-card-content":      {"height": true},
		"provider-metrics":           {"height": true, "borderTopWidth": true, "borderTopColor": true, "paddingTop": true},
		"metric-foots":               {"height": true},
		"provider-footers":           {"height": true, "marginTop": true},
		"settings-provider-rows":     {"height": true},
		"preference-providers":       {"height": true},
		"preference-items":           {"height": true},
		"activity-cells":             {"backgroundColor": true},
		"trend-chips":                {"width": true},
		"provider-table-rows":        {"height": true, "width": true},
		"metric-cycle-badges":        {"width": true},
		"table-mini-gauges":          {},
		"activity-legend-cells":      {},
		"settings-provider-switches": {"backgroundColor": true, "color": true, "borderTopColor": true, "borderRightColor": true, "borderBottomColor": true, "borderLeftColor": true, "overflow": true},
		"preference-switches":        {"backgroundColor": true, "color": true, "borderTopColor": true, "borderRightColor": true, "borderBottomColor": true, "borderLeftColor": true, "overflow": true},
		"preference-order":           {"opacity": true},
		"toasts":                     {"width": true, "height": true, "transform": true},
	}
	maskedRepeatedGeometry := map[string]map[string]bool{
		"provider-card-badges":       {"x": true, "y": true, "width": true},
		"provider-cards":             {"height": true, "y": true},
		"provider-card-headers":      {"height": true, "y": true},
		"provider-card-titles":       {"width": true, "y": true, "x": true},
		"provider-card-descriptions": {"width": true, "y": true, "x": true},
		"provider-card-content":      {"height": true, "y": true, "x": true},
		"provider-metrics":           {"height": true, "y": true, "x": true},
		"metric-tops":                {"y": true, "x": true},
		"metric-gauges":              {"y": true, "x": true},
		"metric-foots":               {"y": true, "x": true},
		"provider-footers":           {"y": true, "x": true},
		"settings-provider-rows":     {"height": true, "y": true},
		"settings-provider-switches": {"y": true},
		"preference-providers":       {"height": true, "y": true},
		"preference-items":           {"height": true, "y": true},
		"preference-switches":        {"y": true},
		"preference-order":           {"y": true, "x": true},
		"provider-table-rows":        {"height": true, "width": true, "x": true, "y": true},
		"metric-cycle-badges":        {"x": true, "y": true, "width": true},
		"table-mini-gauges":          {"x": true, "y": true},
		"activity-legend-cells":      {"x": true, "y": true},
		"trend-chips":                {"width": true, "x": true, "y": true},
		"toasts":                     {"x": true, "y": true, "width": true, "height": true},
	}
	if design.State != live.State || design.Viewport != live.Viewport {
		t.Errorf("%s/%s state or viewport drift: design state=%q viewport=%+v live state=%q viewport=%+v", viewport, state, design.State, design.Viewport, live.State, live.Viewport)
	}
	if design.Viewport != live.Viewport {
		t.Fatalf("%s viewport drift: design=%+v live=%+v", viewport, design.Viewport, live.Viewport)
	}
	if strings.Join(design.Order, "|") != strings.Join(live.Order, "|") {
		t.Fatalf("%s section order drift: design=%v live=%v", viewport, design.Order, live.Order)
	}
	for key, expected := range design.Nodes {
		if key == "toaster" && state != "collection-toast" {
			continue
		}
		if (key == "provider-settings" || key == "preference-settings") && state != "settings" && state != "settings-dirty" {
			continue
		}
		if strings.HasPrefix(key, "overview-empty-") && state != "overview-empty" && state != "providers-empty" {
			continue
		}
		if key == "providers-empty-row" && state != "providers-empty" {
			continue
		}
		if strings.HasPrefix(key, "trends-empty-") && state != "trends-empty" {
			continue
		}
		actual, ok := live.Nodes[key]
		if !ok {
			t.Errorf("%s missing mapped node %q", viewport, key)
			continue
		}
		if expected.Present != actual.Present || expected.Visible != actual.Visible {
			t.Errorf("%s %s visibility drift: design present=%v visible=%v live present=%v visible=%v", viewport, key, expected.Present, expected.Visible, actual.Present, actual.Visible)
		}
		if expected.Present && actual.Present && expected.Visible && actual.Visible {
			if expected.Text != "" && expected.Text != actual.Text {
				t.Errorf("%s/%s %s text drift: design=%q live=%q", viewport, state, key, expected.Text, actual.Text)
			}
			for property, want := range expected.Style {
				if isTransitionProperty(property) && !transitionNodes[key] {
					continue
				}
				if maskedStyle[key][property] {
					continue
				}
				if got := actual.Style[property]; got != want {
					t.Errorf("%s %s %s drift: design=%q live=%q", viewport, key, property, want, got)
				}
			}
			for property, want := range expected.Geometry {
				if maskedGeometry[key][property] {
					continue
				}
				got := actual.Geometry[property]
				if want == 0 && got == 0 {
					continue
				}
				if delta := want - got; delta > 0.5 || delta < -0.5 {
					t.Errorf("%s %s geometry.%s drift: design=%.2f live=%.2f", viewport, key, property, want, got)
				}
			}
		}
	}
	for key, expectedNodes := range design.Repeated {
		if key == "toasts" && state != "collection-toast" {
			continue
		}
		if strings.HasPrefix(key, "settings-provider-") || strings.HasPrefix(key, "preference-") || key == "sheet-footer-controls" {
			if state != "settings" && state != "settings-dirty" {
				continue
			}
		}
		actualNodes := live.Repeated[key]
		exactCardinality := map[string]bool{"trend-mode-buttons": true, "trend-range-buttons": true, "activity-days": true, "activity-hours": true, "sheet-footer-controls": true}
		if exactCardinality[key] && len(expectedNodes) != len(actualNodes) {
			t.Errorf("%s/%s repeated %s cardinality drift: design=%d live=%d", viewport, state, key, len(expectedNodes), len(actualNodes))
		}
		if len(expectedNodes) == 0 {
			if len(actualNodes) > 0 {
				t.Errorf("%s/%s repeated %s unexpectedly rendered %d live nodes", viewport, state, key, len(actualNodes))
			}
			continue
		}
		if len(actualNodes) == 0 {
			t.Errorf("%s/%s repeated %s missing all live nodes; design rendered %d", viewport, state, key, len(expectedNodes))
			continue
		}
		for index, actual := range actualNodes {
			expected := expectedNodes[0]
			if index < len(expectedNodes) {
				expected = expectedNodes[index]
			}
			if index >= len(expectedNodes) {
				expected = expectedNodes[len(expectedNodes)-1]
			}
			if expected.Present != actual.Present || expected.Visible != actual.Visible {
				t.Errorf("%s/%s repeated %s[%d] visibility drift: design present=%v visible=%v live present=%v visible=%v", viewport, state, key, index, expected.Present, expected.Visible, actual.Present, actual.Visible)
			}
			if !expected.Present || !actual.Present || !expected.Visible || !actual.Visible {
				continue
			}
			for property, want := range expected.Style {
				if isTransitionProperty(property) && !transitionRepeated[key] {
					continue
				}
				if maskedRepeatedStyle[key][property] {
					continue
				}
				if got := actual.Style[property]; got != want {
					t.Errorf("%s/%s repeated %s[%d] %s drift: design=%q live=%q", viewport, state, key, index, property, want, got)
				}
			}
			for property, want := range expected.Geometry {
				if maskedRepeatedGeometry[key][property] {
					continue
				}
				got := actual.Geometry[property]
				if want == 0 && got == 0 {
					continue
				}
				if delta := want - got; delta > 0.5 || delta < -0.5 {
					t.Errorf("%s/%s repeated %s[%d] geometry.%s drift: design=%.2f live=%.2f", viewport, state, key, index, property, want, got)
				}
			}
			if expected.Text != "" && expected.Text != actual.Text {
				t.Errorf("%s/%s repeated %s[%d] text drift: design=%q live=%q", viewport, state, key, index, expected.Text, actual.Text)
			}
		}
	}
	for child, parent := range map[string]string{
		"provider-card-headers":      "provider-cards",
		"provider-card-badges":       "provider-cards",
		"provider-card-content":      "provider-cards",
		"provider-footers":           "provider-cards",
		"settings-provider-switches": "settings-provider-rows",
		"preference-switches":        "preference-items",
	} {
		if (state == "overview-empty" || state == "providers-empty") && parent == "provider-cards" {
			continue
		}
		if len(live.Repeated[parent]) > 0 && len(live.Repeated[child]) != len(live.Repeated[parent]) {
			t.Errorf("%s/%s repeated %s structural cardinality drift: %d nodes for %d %s nodes", viewport, state, child, len(live.Repeated[child]), len(live.Repeated[parent]), parent)
		}
	}
	if items := len(live.Repeated["preference-items"]); items > 0 && len(live.Repeated["preference-order"]) != items*2 {
		t.Errorf("%s/%s repeated preference-order structural cardinality drift: got %d controls for %d items, want %d", viewport, state, len(live.Repeated["preference-order"]), items, items*2)
	}
	for key, want := range design.Copy {
		switch key {
		case "providers-footer":
			if state != "providers" {
				continue
			}
		case "trends-card-title", "trends-card-desc", "trends-badge":
			if state != "trends" {
				continue
			}
		case "activity-alert-heading":
			if state != "activity" {
				continue
			}
		case "settings-title", "settings-help", "sheet-cancel", "sheet-save":
			if state != "settings" {
				continue
			}
		}
		if got := live.Copy[key]; got != want {
			t.Errorf("%s/%s %s copy drift: design=%q live=%q", viewport, state, key, want, got)
		}
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
