package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ClaudeSeo/webusage/internal/collector"
	"github.com/ClaudeSeo/webusage/internal/store"
)

func TestDashboardDesignContract(t *testing.T) {
	// Given: the server renders the dashboard from the repository templates and has live provider data.
	server, cleanup := setupTestServer(t)
	defer cleanup()
	providerID := mustCreateHTTPTestProvider(t, server, "design-provider", `{}`)
	mustEnableHTTPTestProvider(t, server, "design-provider")
	limit := 321.0
	mustCreateHTTPTestSnapshot(t, server, &store.UsageSnapshot{
		ProviderID:  providerID,
		Metric:      "design_metric",
		Used:        73.0,
		Limit:       &limit,
		CollectedAt: time.Now().UTC(),
	})

	// When: the dashboard is requested.
	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodGet, "/", nil))

	// Then: the light shell and live-data interaction contract are present in the SSR document.
	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("dashboard status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	for _, required := range []string{
		`data-slot="sidebar"`,
		`id="view-overview"`,
		`id="view-providers"`,
		`id="view-trends"`,
		`id="view-activity"`,
		`fetch('/api/current')`,
		`fetch('/api/providers')`,
		`fetch('/api/trends`,
		`fetch('/api/activity`,
		`fetch('/api/collect'`,
		`fetch('/api/metric-preferences')`,
		`/api/providers/`,
		`localStorage`,
		`aria-expanded`,
		`aria-sort="none"`,
		`contenteditable="true"`,
		`design-provider`,
		`data-label="design_metric"`,
		`data-used="73"`,
		`data-limit="321"`,
		`#metricTableBody tr`,
		`table-used`,
		`table-percent`,
		`table-gauge-fill`,
		`row.dataset.gaugeMode`,
		`state.gaugeMode === 'remaining'`,
		`const cumulativeValues`,
		`untouchedCumulativeValues`,
		`state.selectedMetrics[providerName]`,
		`selectedMetric(providerObj, providerName)`,
		`width: 44px; height: 44px`,
		`min-height: 44px`,
		`#settingsBtn { min-width: 44px`,
		`.sidebar-foot .btn, .sidebar-foot [data-slot="button"] { height: 44px; }`,
		`requestAnimationFrame(() => sidebar.querySelector('[data-view]')?.focus())`,
		`lastFocusedElement = document.activeElement`,
		`lastFocusedElement.focus()`,
		`event.key === 'Escape'`,
		`document.getElementById('navOverlay').addEventListener('click'`,
		`document.getElementById('sheetOverlay').addEventListener('click'`,
		`window.addEventListener('resize'`,
		`closeNav(false)`,
		`document.documentElement.classList.remove('nav-open')`,
		`document.documentElement.classList.remove('sheet-open')`,
		`--bg: #ffffff;`,
		`--accent: #10a37f;`,
		`class="app"`,
		`data-slot="topbar"`,
		`data-slot="sheet"`,
		`@media (max-width: 960px)`,
		`id="metricValueHeader"`,
		`data-gauge-label="usage"`,
		`data-remaining-label="남음"`,
		`id="metricPercentHeader"`,
		`data-remaining-label="남은 비율"`,
		`Number(row.dataset.limit) - Number(row.dataset.used)`,
		`metricValueHeader.dataset.gaugeMode`,
		`metricPercentHeader.dataset.gaugeMode`,
		`100 - Number(row.dataset.pct)`,
		`const remainingWithoutLimit = state.gaugeMode === 'remaining' && !hasLimit`,
		`if (remainingWithoutLimit)`,
		`fill.style.width = '0%'`,
		`const metricRemainingWithoutLimit = state.gaugeMode === 'remaining' && !hasLimit`,
		`if (metricRemainingWithoutLimit)`,
		`metric.querySelector('.metric-progress')`,
		`return null`,
		`fill.classList.remove('warn', 'danger')`,
		`if (value && !hasLimit)`,
		`formatNumber(used)`,
		`사용량 ${formatNumber(used)}`,
		`leftMissing ? 1 : -1`,
		`Array.prototype.sort.call(sortedLabels`,
		`new Date(left).getTime()`,
		`const previousCumulativeValue`,
		`provider.enabled ? 'disable' : 'enable'`,
		`{ method: 'POST' }`,
		`method: 'PUT'`,
		`data-range="5h"`,
		`data-range="24h"`,
		`data-range="7d"`,
		`data-range="30d"`,
		`data-mode="cumulative"`,
		`data-mode="delta"`,
		`setAttribute('aria-pressed'`,
		`aria-current`,
		`ascending`,
		`descending`,
		`event.key.toLowerCase() === 'r'`,
		`event.key.toLowerCase() === 's'`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("dashboard missing design/live-data contract %q", required)
		}
	}
	for _, forbidden := range []string{"2026-08-03", "const SNAP", "const PROVIDERS"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("dashboard contains prototype fixture %q", forbidden)
		}
	}
}

func TestDashboardLiveParityContract(t *testing.T) {
	// Given: projection-rich SSR data, an initially disabled provider, and multiple snapshots.
	server, cleanup := setupTestServer(t)
	defer cleanup()
	providerID := mustCreateHTTPTestProvider(t, server, "claude", `{}`)
	mustEnableHTTPTestProvider(t, server, "claude")
	disabledID := mustCreateHTTPTestProvider(t, server, "disabled-provider", `{}`)
	if err := server.store.EnableProviderByName("disabled-provider", false); err != nil {
		t.Fatalf("disable provider: %v", err)
	}
	limit := 100.0
	now := time.Now().UTC()
	mustCreateHTTPTestSnapshots(t, server, []*store.UsageSnapshot{
		{ProviderID: providerID, Metric: "session", Used: 20, Limit: &limit, CollectedAt: now.Add(-2 * time.Hour), ResetAt: ptrTime(now.Add(3 * time.Hour))},
		{ProviderID: providerID, Metric: "session", Used: 60, Limit: &limit, CollectedAt: now, ResetAt: ptrTime(now.Add(3 * time.Hour))},
	})
	disabledLimit := 50.0
	mustCreateHTTPTestSnapshot(t, server, &store.UsageSnapshot{
		ProviderID:  disabledID,
		Metric:      "session",
		Used:        5,
		Limit:       &disabledLimit,
		CollectedAt: now,
		ResetAt:     ptrTime(now.Add(3 * time.Hour)),
	})
	errorMessage := "private token should never render"
	if err := server.store.UpdateProviderStatus(providerID, &errorMessage); err != nil {
		t.Fatalf("UpdateProviderStatus() error = %v", err)
	}

	// When: the live dashboard and each public API are requested from the real server.
	body := requestDashboardContractResponse(t, server, nethttp.MethodGet, "/")
	currentInitial := requestDashboardContractResponse(t, server, nethttp.MethodGet, "/api/current")
	providersInitial := requestDashboardContractResponse(t, server, nethttp.MethodGet, "/api/providers")
	trendsInitial := requestDashboardContractResponse(t, server, nethttp.MethodGet, "/api/trends?range=5h")
	activityResponse := requestDashboardContractResponse(t, server, nethttp.MethodGet, "/api/activity")
	preferencesResponse := requestDashboardContractResponse(t, server, nethttp.MethodGet, "/api/metric-preferences")

	// Then: SSR projection values are concrete and the table has exactly eight columns.
	if !regexp.MustCompile(`(?s)data-provider-id="claude".*?data-metric="session"[^>]*data-used="60"`).MatchString(body) {
		t.Fatal("SSR did not project latest claude usage=60")
	}
	if !regexp.MustCompile(`(?s)data-provider-id="claude".*?data-metric="session"[^>]*data-limit="100"`).MatchString(body) {
		t.Fatal("SSR did not project claude limit=100")
	}
	if !regexp.MustCompile(`(?s)data-provider-id="claude".*?data-metric="session"[^>]*data-percent="60"`).MatchString(body) {
		t.Fatal("SSR did not project claude percent=60")
	}
	projectionMatch := regexp.MustCompile(`data-projected-percent="([^"]+)"`).FindStringSubmatch(body)
	if len(projectionMatch) != 2 || projectionMatch[1] == "" {
		t.Fatal("SSR projection percent was empty")
	}
	projectionPercent, err := strconv.ParseFloat(projectionMatch[1], 64)
	if err != nil || projectionPercent <= 0 {
		t.Fatalf("SSR projection percent = %q, want positive number", projectionMatch[1])
	}
	theadStart := strings.Index(body, "<thead>")
	if theadStart < 0 {
		t.Fatal("SSR metric table header missing")
	}
	theadEnd := strings.Index(body[theadStart:], "</thead>")
	if theadEnd < 0 {
		t.Fatal("SSR metric table header is unterminated")
	}
	if got := len(regexp.MustCompile(`<th(?:\s|>)`).FindAllString(body[theadStart:theadStart+theadEnd], -1)); got != 8 {
		t.Fatalf("SSR metric table headers = %d, want exactly 8", got)
	}
	if strings.Contains(body, "private token should never render") {
		t.Fatal("dashboard rendered raw provider error")
	}

	// Capture terminal collection responses and real provider state transitions for the client harness.
	server.SetCollector(collector.NewCollector(server.store, nil, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil))))
	acceptedBody := requestDashboardContractResponse(t, server, nethttp.MethodPost, "/api/collect")
	var accepted struct {
		CollectionID string `json:"collection_id"`
		StatusURL    string `json:"status_url"`
	}
	if err := json.Unmarshal([]byte(acceptedBody), &accepted); err != nil || accepted.CollectionID == "" || accepted.StatusURL == "" {
		t.Fatalf("invalid collection acceptance response: %s", acceptedBody)
	}
	status := waitForCollectionStatus(t, server, accepted.CollectionID)
	statusBody, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal collection status: %v", err)
	}
	enableBody := requestDashboardContractResponse(t, server, nethttp.MethodPost, "/api/providers/disabled-provider/enable")
	providersEnabled := requestDashboardContractResponse(t, server, nethttp.MethodGet, "/api/providers")
	currentEnabled := requestDashboardContractResponse(t, server, nethttp.MethodGet, "/api/current")
	trendsEnabled := requestDashboardContractResponse(t, server, nethttp.MethodGet, "/api/trends?range=5h")
	disableBody := requestDashboardContractResponse(t, server, nethttp.MethodPost, "/api/providers/disabled-provider/disable")
	providersDisabled := requestDashboardContractResponse(t, server, nethttp.MethodGet, "/api/providers")
	currentDisabled := requestDashboardContractResponse(t, server, nethttp.MethodGet, "/api/current")

	if err := runDashboardClientParityHarness(t, dashboardClientParityInput{
		HTML:                       body,
		CurrentInitial:             currentInitial,
		CurrentEnabled:             currentEnabled,
		CurrentDisabled:            currentDisabled,
		ProvidersInitial:           providersInitial,
		ProvidersEnabled:           providersEnabled,
		ProvidersDisabled:          providersDisabled,
		TrendsInitial:              trendsInitial,
		TrendsEnabled:              trendsEnabled,
		Activity:                   activityResponse,
		Preferences:                preferencesResponse,
		AcceptedCollection:         acceptedBody,
		CollectionStatusTerminal:   string(statusBody),
		CollectionStatusCollecting: `{"status":"running","terminal":false,"done":false}`,
		EnableProvider:             enableBody,
		DisableProvider:            disableBody,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestDashboardWeakEstimateRendersDimmedForecast verifies the post-prototype
// projection UX: a weak estimate (관측 구간이 짧아 외삽 근거가 약한 추정) no
// longer swaps the projection slot for a "표본 부족" label and "페이스 계산
// 불가" is never shown. Instead the forecast hatch and "리셋 시점 추정 X%" stay
// visible but dimmed, the gauge fill is colored by severity, and the provider
// badge reports "한도 임박" from the worst metric severity.
func TestDashboardWeakEstimateRendersDimmedForecast(t *testing.T) {
	// Given: claude session(투영 120%, danger) + weekly(reset 직후라 weak, 투영 168%).
	// weekly의 resetAt을 now+160h로 두면 cycleStart=now-8h가 돼 두 스냅샷이 주기 안에
	// 들어와 HasProjection=true가 되면서도 160 > 4×6 가드에 걸려 WeakEstimate=true.
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

	// When: the dashboard is server-rendered.
	body := requestDashboardContractResponse(t, server, nethttp.MethodGet, "/")

	// Then: legacy labels are gone, the forecast stays (dimmed for weak),
	// the fill is severity-colored, the weak hatch carries a `weak` class,
	// and the provider badge reports "한도 임박" from the worst danger metric.
	if strings.Contains(body, "표본 부족") {
		t.Fatal("weak estimate still renders the legacy '표본 부족' label")
	}
	if strings.Contains(body, "페이스 계산 불가") {
		t.Fatal("dashboard renders the hidden '페이스 계산 불가' label")
	}
	if !strings.Contains(body, "한도 임박") {
		t.Fatal("provider badge missing '한도 임박' for worst danger severity")
	}
	if !strings.Contains(body, "metric-projection right weak") {
		t.Fatal("weak projection missing the dimmed 'weak' class")
	}
	if !strings.Contains(body, "gauge-fill metric-progress danger") {
		t.Fatal("danger metric fill not severity-colored")
	}
	if !strings.Contains(body, "gauge-proj weak") {
		t.Fatal("weak projection hatch missing the 'weak' class")
	}
}

func requestDashboardContractResponse(t *testing.T, server *Server, method, path string) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	if recorder.Code < nethttp.StatusOK || recorder.Code >= nethttp.StatusMultipleChoices {
		t.Fatalf("%s %s status = %d; body=%s", method, path, recorder.Code, recorder.Body.String())
	}
	return recorder.Body.String()
}

type dashboardClientParityInput struct {
	HTML                       string
	CurrentInitial             string
	CurrentEnabled             string
	CurrentDisabled            string
	ProvidersInitial           string
	ProvidersEnabled           string
	ProvidersDisabled          string
	TrendsInitial              string
	TrendsEnabled              string
	Activity                   string
	Preferences                string
	AcceptedCollection         string
	CollectionStatusCollecting string
	CollectionStatusTerminal   string
	EnableProvider             string
	DisableProvider            string
}

func runDashboardClientParityHarness(t *testing.T, input dashboardClientParityInput) error {
	t.Helper()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return fmt.Errorf("dashboard parity requires node: %w", err)
	}
	encodedInput, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode dashboard parity input: %w", err)
	}
	harnessSource := strings.ReplaceAll(dashboardClientParityHarnessSource, `\\`, `\`)
	harness := strings.Replace(harnessSource, "__INPUT__", string(encodedInput), 1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, nodePath, "-")
	command.Stdin = strings.NewReader(harness)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("dashboard parity node harness timed out: %w", ctx.Err())
	}
	if err != nil {
		return fmt.Errorf("dashboard parity node harness failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func TestDashboardSSRIncludesDisabledProviderNodesForLiveReconciliation(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()
	providerID := mustCreateHTTPTestProvider(t, server, "disabled-provider", `{}`)
	if err := server.store.EnableProviderByName("disabled-provider", false); err != nil {
		t.Fatalf("disable provider: %v", err)
	}
	limit := 100.0
	mustCreateHTTPTestSnapshot(t, server, &store.UsageSnapshot{
		ProviderID:  providerID,
		Metric:      "session",
		Used:        25,
		Limit:       &limit,
		CollectedAt: time.Now().UTC(),
	})

	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodGet, "/", nil))
	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("dashboard status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `data-provider-id="disabled-provider"`) {
		t.Fatal("dashboard omitted disabled provider card needed for enable reconciliation")
	}
	if !strings.Contains(body, `data-provider="disabled-provider"`) {
		t.Fatal("dashboard omitted disabled provider metric row needed for enable reconciliation")
	}
	cardStart := strings.Index(body, `<article class="provider-card interactive" data-slot="card" data-provider-id="disabled-provider"`)
	if cardStart < 0 {
		t.Fatal("disabled provider card opening tag missing")
	}
	cardEnd := strings.Index(body[cardStart:], ">")
	if cardEnd < 0 || !strings.Contains(body[cardStart:cardStart+cardEnd], " hidden") {
		t.Fatal("disabled provider card must be hidden on first paint")
	}
}

const dashboardClientParityHarnessSource = `
const input = __INPUT__;
const vm = require('vm');

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function camelCase(value) {
  return value.replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());
}

class ClassList {
  constructor(node) { this.node = node; }
  values() { return new Set((this.node._className || '').split(/\\s+/).filter(Boolean)); }
  sync(values) { this.node._className = Array.from(values).join(' '); this.node.attrs.class = this.node._className; }
  add(...names) { const values = this.values(); names.forEach(name => values.add(name)); this.sync(values); }
  remove(...names) { const values = this.values(); names.forEach(name => values.delete(name)); this.sync(values); }
  contains(name) { return this.values().has(name); }
  toggle(name, force) { const values = this.values(); const next = force === undefined ? !values.has(name) : Boolean(force); if (next) values.add(name); else values.delete(name); this.sync(values); return next; }
}

class Node {
  constructor(tagName) {
    this.tagName = String(tagName || '').toLowerCase();
    this.children = [];
    this.parentNode = null;
    this.attrs = Object.create(null);
    this._dataset = Object.create(null);
    this._className = '';
    this._text = '';
    this.style = {};
    this.hidden = false;
    this.eventHandlers = Object.create(null);
    this.tabIndex = 0;
    this.clientWidth = 900;
    this.classList = new ClassList(this);
    const node = this;
    this.dataset = new Proxy(this._dataset, {
      get(target, property) { return target[property]; },
      set(target, property, value) { target[property] = String(value); node.attrs['data-' + String(property).replace(/[A-Z]/g, letter => '-' + letter.toLowerCase())] = String(value); return true; }
    });
  }
  get id() { return this.attrs.id || ''; }
  set id(value) { this.setAttribute('id', value); }
  get className() { return this._className; }
  set className(value) { this._className = String(value || ''); this.attrs.class = this._className; }
  get textContent() { return this._text || this.children.map(child => child.textContent || '').join(''); }
  set textContent(value) { this._text = String(value ?? ''); this.children = []; }
  get innerHTML() { return this._innerHTML || this.children.map(child => child.textContent || '').join(''); }
  set innerHTML(value) {
    this._innerHTML = String(value ?? '');
    this.children = [];
    if (this.tagName === 'svg') parseFragment(this, this._innerHTML);
  }
  get value() { return this._value || ''; }
  set value(value) { this._value = String(value ?? ''); }
  append(...items) { items.forEach(item => { const child = typeof item === 'string' ? new TextNode(item) : item; if (!child) return; child.parentNode = this; this.children.push(child); }); }
  appendChild(item) { this.append(item); return item; }
  replaceChildren(...items) { this.children = []; this._text = ''; this.append(...items); }
  remove() { if (!this.parentNode) return; this.parentNode.children = this.parentNode.children.filter(child => child !== this); this.parentNode = null; }
  setAttribute(name, value) {
    const key = String(name);
    const stringValue = String(value);
    this.attrs[key] = stringValue;
    if (key === 'class') this._className = stringValue;
    if (key === 'hidden') this.hidden = true;
    if (key.startsWith('data-')) this._dataset[camelCase(key.slice(5))] = stringValue;
  }
  getAttribute(name) { return this.attrs[String(name)] ?? null; }
  removeAttribute(name) { delete this.attrs[String(name)]; if (name === 'hidden') this.hidden = false; }
  addEventListener(type, handler) { (this.eventHandlers[type] ||= []).push(handler); }
  dispatchEvent(event) {
    const payload = event || {};
    payload.type ||= 'event'; payload.target ||= this; payload.currentTarget = this;
    payload.preventDefault ||= (() => { payload.defaultPrevented = true; });
    payload.stopPropagation ||= (() => { payload.cancelBubble = true; });
    (this.eventHandlers[payload.type] || []).forEach(handler => handler(payload));
    if (!payload.cancelBubble && this.parentNode) this.parentNode.dispatchEvent(payload);
    return !payload.defaultPrevented;
  }
  click() { this.dispatchEvent({ type: 'click', target: this }); }
  focus() { if (this.ownerDocument) this.ownerDocument.activeElement = this; }
  getBoundingClientRect() { return { left: 0, top: 0, width: this.clientWidth || 900, height: 320 }; }
  closest(selector) { let current = this; while (current) { if (matchesSelector(current, selector)) return current; current = current.parentNode; } return null; }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
  querySelectorAll(selector) {
    const result = [];
    walk(this, child => { if (child !== this && matchesSelector(child, selector)) result.push(child); });
    return result;
  }
}

class TextNode extends Node {
  constructor(text) { super('#text'); this._text = String(text || ''); }
}

class Document extends Node {
  constructor(html) {
    super('#document');
    this.ownerDocument = this;
    this.activeElement = null;
    this.eventHandlers = Object.create(null);
    parseHTML(this, html);
    this.documentElement = this.querySelector('html');
    this.body = this.querySelector('body');
  }
  createElement(tagName) { const node = new Node(tagName); node.ownerDocument = this; return node; }
  createElementNS(_, tagName) { return this.createElement(tagName); }
  createTextNode(text) { const node = new TextNode(text); node.ownerDocument = this; return node; }
  getElementById(id) { return this.querySelector('#' + id); }
}

function walk(node, callback) {
  node.children.forEach(child => { callback(child); walk(child, callback); });
}

function parseAttributes(node, source) {
	  const pattern = /([:\\w-]+)(?:\\s*=\\s*(?:"([^"]*)"|'([^']*)'|([^\\s"'=<>]+)))?/g;
  let match;
  while ((match = pattern.exec(source))) node.setAttribute(match[1], match[2] ?? match[3] ?? match[4] ?? '');
}

function parseHTML(root, html) {
  const withoutScripts = html.replace(/<script[\\s\\S]*?<\\/script>/gi, '');
  const tokenPattern = /<\\/?([a-zA-Z][\\w:-]*)([^>]*)>/g;
  const voidTags = new Set(['meta', 'link', 'input', 'img', 'br', 'hr', 'area', 'base', 'embed', 'param', 'source', 'track', 'wbr']);
  const stack = [root];
  let match;
  while ((match = tokenPattern.exec(withoutScripts))) {
    const token = match[0];
    const closing = token.startsWith('</');
    const tag = match[1].toLowerCase();
    if (closing) { if (stack.length > 1) stack.pop(); continue; }
    const node = new Node(tag); node.ownerDocument = root.ownerDocument || root;
    parseAttributes(node, match[2] || '');
    stack[stack.length - 1].appendChild(node);
    if (!voidTags.has(tag) && !token.endsWith('/>')) stack.push(node);
  }
}

function parseFragment(root, markup) {
  const tokenPattern = /<\\/?([a-zA-Z][\\w:-]*)([^>]*)>/g;
  const stack = [root];
  let match;
  while ((match = tokenPattern.exec(markup))) {
    const token = match[0];
    if (token.startsWith('</')) { if (stack.length > 1) stack.pop(); continue; }
    const node = new Node(match[1]); node.ownerDocument = root.ownerDocument;
    parseAttributes(node, match[2] || '');
    stack[stack.length - 1].appendChild(node);
    if (!token.endsWith('/>')) stack.push(node);
  }
}

function matchesSimple(node, selector) {
  if (!node || node.tagName === '#text') return false;
  let value = selector.trim();
  const tagMatch = value.match(/^([a-zA-Z][\\w-]*)/);
  if (tagMatch && node.tagName !== tagMatch[1].toLowerCase()) return false;
  const idMatch = value.match(/#([\\w-]+)/);
  if (idMatch && node.id !== idMatch[1]) return false;
  const classPattern = /\\.([\\w-]+)/g;
  let classMatch;
  while ((classMatch = classPattern.exec(value))) if (!node.classList.contains(classMatch[1])) return false;
  const attrPattern = /\\[([:\\w-]+)(?:=["']?([^\\]"']+)["']?)?\\]/g;
  let attrMatch;
  while ((attrMatch = attrPattern.exec(value))) {
    const actual = node.getAttribute(attrMatch[1]);
    if (actual === null) return false;
    if (attrMatch[2] !== undefined && actual !== attrMatch[2]) return false;
  }
  return true;
}

function matchesSelector(node, selector) {
  return selector.split(',').some(part => {
    const pieces = part.trim().split(/\\s+/).filter(Boolean);
    if (!pieces.length || !matchesSimple(node, pieces[pieces.length - 1])) return false;
    let ancestor = node.parentNode;
    for (let index = pieces.length - 2; index >= 0; index--) {
      while (ancestor && !matchesSimple(ancestor, pieces[index])) ancestor = ancestor.parentNode;
      if (!ancestor) return false;
      ancestor = ancestor.parentNode;
    }
    return true;
  });
}

function makeStorage(kind, seed) {
  const values = new Map();
  const writes = [];
  if (kind === 'corrupt') values.set('webusage.ui.v1', '{corrupt');
  if (typeof seed === 'string' && seed) values.set('webusage.ui.v1', seed);
  return {
    writes,
    getItem(key) { if (kind === 'throwing') throw new Error('storage read blocked'); return values.has(key) ? values.get(key) : null; },
    setItem(key, value) { if (kind === 'throwing') throw new Error('storage write blocked'); values.set(key, String(value)); writes.push(String(value)); },
    removeItem(key) { if (kind === 'throwing') throw new Error('storage remove blocked'); values.delete(key); },
    snapshot(key) { return values.has(key) ? values.get(key) : null; }
  };
}

function parseJSON(value) { return JSON.parse(value || '{}'); }
function clone(value) { return JSON.parse(JSON.stringify(value)); }
function response(body, status = 200) { return { ok: status >= 200 && status < 300, status, json: async () => clone(body) }; }

async function settle(count = 20) { for (let index = 0; index < count; index++) await new Promise(resolve => setTimeout(resolve, 0)); }

function makeFetch(fetchLog) {
  const current = [input.CurrentInitial, input.CurrentEnabled, input.CurrentDisabled, input.CurrentInitial, input.CurrentDisabled].map(parseJSON);
  const providers = [input.ProvidersInitial, input.ProvidersInitial, input.ProvidersEnabled, input.ProvidersEnabled, input.ProvidersDisabled, input.ProvidersDisabled, input.ProvidersDisabled].map(parseJSON);
  const trends = [input.TrendsInitial, input.TrendsEnabled, input.TrendsEnabled, input.TrendsInitial, input.TrendsInitial].map(parseJSON);
  let currentIndex = 0; let providerIndex = 0; let trendIndex = 0; let collectionStatusIndex = 0;
  return async function fetch(url, options = {}) {
    const path = String(url).split('?')[0];
    const method = String(options.method || 'GET').toUpperCase();
    const request = { method, path };
    fetchLog.push(request);
    if (method === 'POST' && path === '/api/collect') return response(parseJSON(input.AcceptedCollection));
    if (method === 'POST' && path === '/api/providers/disabled-provider/enable') return response(parseJSON(input.EnableProvider));
    if (method === 'POST' && path === '/api/providers/disabled-provider/disable') return response(parseJSON(input.DisableProvider));
    if (path === '/api/collect/status') {
      const status = parseJSON(collectionStatusIndex++ === 0 ? input.CollectionStatusCollecting : input.CollectionStatusTerminal);
      request.terminal = status.terminal === true || status.done === true || ['completed', 'failed'].includes(status.status);
      return response(status);
    }
    if (path === '/api/current') return response(current[Math.min(currentIndex++, current.length - 1)]);
    if (path === '/api/providers') return response(providers[Math.min(providerIndex++, providers.length - 1)]);
    if (path === '/api/trends') return response(trends[Math.min(trendIndex++, trends.length - 1)]);
    if (path === '/api/activity') return response(parseJSON(input.Activity));
    if (path === '/api/metric-preferences') return response(parseJSON(input.Preferences));
    throw new Error('unexpected fetch ' + method + ' ' + url);
  };
}

function extractClientSource(html) {
  const scripts = Array.from(html.matchAll(/<script(?:[^>]*)>([\\s\\S]*?)<\\/script>/gi)).map(match => match[1]);
  const source = scripts.find(script => script.includes('(function ()'));
  assert(source, 'inline dashboard client script missing');
  return source;
}

async function boot(kind, seed) {
  const document = new Document(input.HTML);
  const storage = makeStorage(kind, seed);
  const fetchLog = [];
  const clientSetTimeout = setTimeout;
  const window = {
    localStorage: storage,
    setTimeout: clientSetTimeout,
    clearTimeout,
    requestAnimationFrame: callback => setTimeout(callback, 0),
    matchMedia: () => ({ matches: false }),
    addEventListener: () => {},
    location: { reload: () => fetchLog.push({ method: 'RELOAD', path: 'reload' }) }
  };
  const context = vm.createContext({
    console,
    document,
    window,
    CSS: { escape: value => String(value).replace(/[^a-zA-Z0-9_-]/g, character => '\\\\' + character) },
    fetch: makeFetch(fetchLog),
    setTimeout: clientSetTimeout,
    clearTimeout,
    requestAnimationFrame: window.requestAnimationFrame,
    Intl,
    Date,
    Number,
    String,
    Boolean,
    Array,
    Object,
    JSON,
    Math,
    Promise,
    Error
  });
  vm.runInContext(extractClientSource(input.HTML), context, { timeout: 5000 });
  await settle(30);
  return { document, storage, fetchLog };
}

async function main() {
  const initialProviders = parseJSON(input.ProvidersInitial).providers || [];
  assert(initialProviders.some(provider => provider.provider_id === 'disabled-provider' && provider.enabled === false), 'real providers API did not expose disabled provider');
  const initialCurrent = parseJSON(input.CurrentInitial);
  assert(initialCurrent.claude && initialCurrent.claude.current_usage === 60, 'real current API did not expose SSR latest usage');

  const run = await boot('normal');
  const document = run.document;
  const disabledCard = document.querySelector('[data-provider-id="disabled-provider"]');
  const disabledRow = document.querySelector('#metricTableBody tr[data-provider="disabled-provider"]');
  assert(disabledCard && disabledCard.hidden, 'disabled provider card was not hidden in the live DOM');
  assert(disabledRow && disabledRow.hidden, 'disabled provider table row was not hidden in the live DOM');

  document.getElementById('settingsBtn').click();
  await settle(20);
  let providerButton = document.getElementById('drawer-btn-disabled-provider');
  assert(providerButton && providerButton.textContent === '활성화', 'disabled provider action did not render enable control');
  providerButton.click();
  await settle(30);
  assert(!disabledCard.hidden && !disabledRow.hidden, 'enable action did not reconcile card and table row visibility');
  providerButton = document.getElementById('drawer-btn-disabled-provider');
  assert(providerButton && providerButton.textContent === '비활성화', 'enabled provider action did not render disable control');
  providerButton.click();
  await settle(30);
  assert(disabledCard.hidden && disabledRow.hidden, 'disable action left provider card or row visible');

  document.querySelector('#nav [data-view="trends"]').click();
  await settle(30);
  assert(document.querySelectorAll('#trendChart .series-line').length > 0, 'trend client did not render SVG series output: status=' + document.getElementById('trendDataStatus').textContent + ' empty=' + document.getElementById('chartEmpty').hidden + ' svgChildren=' + document.getElementById('trendChart').children.length + ' chips=' + document.getElementById('chipRow').children.length);
  document.querySelector('#rangeTabs [data-range="24h"]').click();
  await settle(20);
  document.querySelector('#modeGroup [data-mode="delta"]').click();
  await settle(20);
  let saved = JSON.parse(run.storage.writes.at(-1));
  assert(saved.view === 'trends' && saved.range === '24h' && saved.mode === 'delta', 'view/range/mode state was not persisted');
  assert(document.querySelectorAll('#trendChart rect').length > 0, 'delta mode did not render chart bars');

  let chip = document.querySelector('#chipRow [data-provider="claude"]');
  assert(chip, 'provider chip did not render');
  chip.click();
  await settle(10);
  saved = JSON.parse(run.storage.writes.at(-1));
  chip = document.querySelector('#chipRow [data-provider="claude"]');
  assert(saved.chartHidden.includes('claude') && chip.getAttribute('aria-pressed') === 'false', 'provider chip state was not persisted');

  document.getElementById('gaugeModeSwitch').click();
  await settle(10);
  saved = JSON.parse(run.storage.writes.at(-1));
  assert(saved.gaugeMode === 'remaining' && document.getElementById('gaugeModeSwitch').getAttribute('aria-checked') === 'false', 'gauge mode state was not persisted');
  assert(document.querySelector('#metricTableBody tr[data-provider="claude"]').dataset.gaugeMode === 'remaining', 'gauge mode did not reconcile table row state');

  const collectionStart = run.fetchLog.length;
  document.getElementById('collectBtn').click();
  await new Promise(resolve => setTimeout(resolve, 650));
  await settle(35);
  assert(document.getElementById('collectBtn').dataset.collectionState === 'completed', 'terminal collection did not complete');
  assert(document.getElementById('overviewStatus').textContent === 'API 연결됨', 'terminal collection did not refresh dashboard data');

  const refreshPaths = new Set(['/api/current', '/api/providers', '/api/trends', '/api/activity']);
  const statusRequests = run.fetchLog.filter((entry, index) => index >= collectionStart && entry.path === '/api/collect/status');
  assert(statusRequests.length >= 2 && statusRequests[0].terminal === false, 'collection status mock did not expose a collecting response before terminal');
  const terminalIndex = run.fetchLog.findIndex((entry, index) => index >= collectionStart && entry.path === '/api/collect/status' && entry.terminal === true);
  assert(terminalIndex >= 0, 'collection status mock did not expose terminal response');
  const preTerminalRefresh = run.fetchLog.slice(collectionStart, terminalIndex).filter(entry => refreshPaths.has(entry.path) || entry.method === 'RELOAD');
  assert(preTerminalRefresh.length === 0, 'dashboard refreshed before collection terminal: ' + JSON.stringify(preTerminalRefresh));
  const postTerminalPaths = run.fetchLog.slice(terminalIndex + 1).map(entry => entry.path);
  for (const path of ['/api/current', '/api/providers', '/api/trends']) {
    assert(postTerminalPaths.includes(path), 'dashboard did not refresh ' + path + ' after collection terminal');
  }

  const activityStart = run.fetchLog.length;
  document.querySelector('#nav [data-view="activity"]').click();
  await settle(20);
  assert(run.fetchLog.slice(activityStart).some(entry => entry.path === '/api/activity'), 'activity navigation did not call /api/activity');
  assert(run.fetchLog.slice(terminalIndex + 1).some(entry => entry.path === '/api/activity'), 'dashboard activity refresh occurred before terminal');
  assert(document.querySelectorAll('#heatmap .hm-cell').length > 0, 'activity API did not render grid cells');
  assert(document.getElementById('hmTotal').textContent.includes('스냅샷'), 'activity summary did not render');

  document.querySelector('#nav [data-view="trends"]').click();
  await settle(30);
  assert(document.getElementById('view-trends').hidden === false, 'trend view did not restore after activity navigation');
  chip = document.querySelector('#chipRow [data-provider="claude"]');
  assert(chip && chip.getAttribute('aria-pressed') === 'false', 'hidden provider chip was not retained after activity navigation');
  saved = JSON.parse(run.storage.writes.at(-1));
  assert(saved.view === 'trends' && saved.range === '24h' && saved.mode === 'delta' && saved.chartHidden.includes('claude') && saved.gaugeMode === 'remaining', 'final persisted UI state was incomplete');

  const validPayload = run.storage.snapshot('webusage.ui.v1');
  assert(validPayload, 'normal boot did not seed a persisted UI payload');
  const restored = await boot('normal', validPayload);
  const restoredDocument = restored.document;
  assert(restoredDocument.getElementById('view-trends').hidden === false, 'valid saved view was not restored in the DOM');
  assert(restoredDocument.querySelector('#rangeTabs [data-range="24h"]').getAttribute('aria-selected') === 'true', 'valid saved range was not restored');
  assert(restoredDocument.querySelector('#modeGroup [data-mode="delta"]').getAttribute('aria-pressed') === 'true', 'valid saved chart mode was not restored');
  const restoredChip = restoredDocument.querySelector('#chipRow [data-provider="claude"]');
  assert(restoredChip && restoredChip.getAttribute('aria-pressed') === 'false', 'valid saved provider-chip visibility was not restored');
  assert(restoredDocument.getElementById('gaugeModeSwitch').getAttribute('aria-checked') === 'false', 'valid saved gauge mode was not restored');

  function assertSafeDefaults(result, label) {
    const safeDocument = result.document;
    assert(safeDocument.getElementById('view-overview').hidden === false, label + ' storage changed the default view');
    assert(safeDocument.querySelector('#rangeTabs [data-range="7d"]').getAttribute('aria-selected') === 'true', label + ' storage changed the default range');
    assert(safeDocument.querySelector('#modeGroup [data-mode="cumulative"]').getAttribute('aria-pressed') === 'true', label + ' storage changed the default chart mode');
    assert(safeDocument.getElementById('gaugeModeSwitch').getAttribute('aria-checked') === 'true', label + ' storage changed the default gauge mode');
  }
  assertSafeDefaults(await boot('corrupt'), 'corrupt');
  assertSafeDefaults(await boot('throwing'), 'throwing');
}

main().catch(error => { console.error(error && error.stack || error); process.exitCode = 1; });
`

func ptrTime(value time.Time) *time.Time { return &value }

func TestDashboardProviderCardDescribesFirstRenderedMetricCycle(t *testing.T) {
	// Given: codex, whose provider-wide configuration names a 5-hour session as
	// its headline cycle, reports only a weekly metric.
	server, cleanup := setupTestServer(t)
	defer cleanup()
	providerID := mustCreateHTTPTestProvider(t, server, "codex", `{}`)
	mustEnableHTTPTestProvider(t, server, "codex")
	limit := 100.0
	mustCreateHTTPTestSnapshot(t, server, &store.UsageSnapshot{
		ProviderID:  providerID,
		Metric:      "weekly",
		Used:        96,
		Limit:       &limit,
		CollectedAt: time.Now().UTC(),
	})

	// When: the dashboard is rendered.
	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodGet, "/", nil))
	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("dashboard status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()

	// Then: the card description states the cycle of the metric it actually
	// renders, not the configured headline cycle of a metric that is absent.
	description := regexp.MustCompile(`<p data-slot="card-description"[^>]*>([^<]*)</p>`).FindStringSubmatch(body)
	if len(description) != 2 {
		t.Fatal("provider card description missing from dashboard")
	}
	if got, want := description[1], "주간 · 한도 있음"; got != want {
		t.Fatalf("provider card description = %q, want %q", got, want)
	}
}
