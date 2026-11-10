package runner

import (
	"strings"
	"testing"
	"time"
)

const summaryV1 = `
  █ THRESHOLDS

    http_req_duration
    ✓ 'p(95)<1000' p(95)=23.12ms

    http_req_duration{name:heavy}
    ✗ 'p(95)<1000' p(95)=3.41s

    http_req_failed
    ✓ 'rate<0.01' rate=0.00%


  █ TOTAL RESULTS

    checks_total.......................: 3100    150.2/s
    ✓ items 200
`

func TestParseSummaryThresholds(t *testing.T) {
	got := ParseSummaryThresholds(summaryV1)
	if len(got) != 3 {
		t.Fatalf("got %d: %+v", len(got), got)
	}
	h := got[1]
	if h.Metric != "http_req_duration{name:heavy}" || h.Expr != "p(95)<1000" || h.OK || h.Actual != "p(95)=3.41s" {
		t.Fatalf("heavy: %+v", h)
	}
	if !got[0].OK || got[2].Metric != "http_req_failed" {
		t.Fatalf("%+v", got)
	}
}

func TestParseMetricName(t *testing.T) {
	a := ParseMetricName("http_req_duration{ name: heavy }")
	b := ParseMetricName("http_req_duration{name:heavy}")
	if a.Key() != b.Key() || a.Base != "http_req_duration" {
		t.Fatalf("%q vs %q", a.Key(), b.Key())
	}
	if ParseMetricName("http_req_duration{name:Heavy}").Key() == b.Key() {
		t.Fatal("大文字と小文字は区別する")
	}
	if !b.Match(map[string]string{"name": "heavy", "url": "x"}) || b.Match(map[string]string{"name": "Heavy"}) {
		t.Fatal("match")
	}
}

func TestExprAndAggregate(t *testing.T) {
	e, err := ParseExpr("p(95) < 1000")
	if err != nil || e.Agg != "p" || e.P != 95 || e.Value != 1000 {
		t.Fatalf("%+v %v", e, err)
	}
	vals := []float64{}
	for i := 1; i <= 100; i++ {
		vals = append(vals, float64(i))
	}
	if v := Aggregate(e, "trend", vals, 0); v < 95 || v > 96 {
		t.Fatalf("p95=%v", v)
	}
	if v := Aggregate(e, "trend", nil, 0); v != 0 || !e.Holds(v) {
		t.Fatal("測定0件は値0として合格する（k6 と同じ）")
	}
	r, _ := ParseExpr("rate<0.01")
	if v := Aggregate(r, "rate", []float64{0, 0, 1, 0}, 0); v != 0.25 {
		t.Fatalf("rate=%v", v)
	}
	if _, err := ParseExpr("p95<300"); err == nil {
		t.Fatal("p95 は読めないはず")
	}
}

func TestParseInspect(t *testing.T) {
	in, err := ParseInspect([]byte(`{
		"vus": 20, "duration": "20s",
		"thresholds": {"http_req_duration{name:heavy}": ["p(95)<1000"], "http_req_failed": [{"threshold": "rate<0.01", "abortOnFail": true}]},
		"scenarios": {"default": {"executor": "constant-vus", "vus": 20, "duration": "20s", "gracefulStop": "30s"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if in.MaxVUs != 20 || in.Duration != 20*time.Second || len(in.Scenarios) != 1 || in.Scenarios[0].Exec != "default" {
		t.Fatalf("%+v", in)
	}
	if in.Thresholds["http_req_failed"][0] != "rate<0.01" {
		t.Fatalf("%+v", in.Thresholds)
	}

	in, _ = ParseInspect([]byte(`{"stages": [{"duration": "5s", "target": 10}, {"duration": "10s", "target": 30}, {"duration": "5s", "target": 0}]}`))
	if in.MaxVUs != 30 || in.Duration != 20*time.Second || len(in.AllStages()) != 3 {
		t.Fatalf("%+v", in)
	}

	in, _ = ParseInspect([]byte(`{"scenarios": {
		"browse": {"executor": "constant-vus", "vus": 5, "duration": "10s", "exec": "browse"},
		"upload": {"executor": "per-vu-iterations", "vus": 2, "iterations": 5, "exec": "upload", "maxDuration": "10m0s"}
	}, "maxVUs": 7}`))
	if in.MaxVUs != 7 || in.Duration != 10*time.Second || in.Scenarios[1].Exec != "upload" {
		t.Fatalf("%+v", in)
	}
}

func TestReadPoints(t *testing.T) {
	src := strings.Join([]string{
		`{"type":"Metric","data":{"name":"http_req_duration","type":"trend","contains":"time"},"metric":"http_req_duration"}`,
		`{"metric":"http_reqs","type":"Point","data":{"time":"2026-11-09T10:00:00Z","value":1,"tags":{"url":"http://localhost:8090/api/heavy","name":"Heavy","status":"200","method":"GET"}}}`,
		`{"metric":"http_req_duration","type":"Point","data":{"time":"2026-11-09T10:00:00Z","value":3000,"tags":{"url":"http://localhost:8090/api/heavy","name":"Heavy","status":"200"}}}`,
		`{"metric":"http_req_duration","type":"Point","data":{"time":"2026-11-09T10:00:01Z","value":20,"tags":{"url":"http://localhost:8090/api/items","name":"items","status":"200"}}}`,
		`{"metric":"checks","type":"Point","data":{"time":"2026-11-09T10:00:01Z","value":1,"tags":{"check":"status 200"}}}`,
	}, "\n")
	p, err := ReadPoints(strings.NewReader(src), []string{"http_req_duration{name:heavy}", "http_req_duration"})
	if err != nil {
		t.Fatal(err)
	}
	ths := buildThresholds([]string{"http_req_duration", "http_req_duration{name:heavy}"},
		map[string][]string{"http_req_duration": {"p(95)<1000"}, "http_req_duration{name:heavy}": {"p(95)<1000"}}, p, nil)
	if ths[0].Count != 2 || ths[0].OK {
		t.Fatalf("aggregate: %+v", ths[0])
	}
	if ths[1].Count != 0 || !ths[1].OK || ths[1].Actual != "p(95)=0s" {
		t.Fatalf("zero sample: %+v", ths[1])
	}
	if p.HTTPReqs != 1 || p.ChecksTotal != 1 || p.ChecksPassed != 1 || p.Seconds != 1 {
		t.Fatalf("%+v", p)
	}
}

func TestLimitedBuffer(t *testing.T) {
	var b limitedBuffer
	chunk := strings.Repeat("x", 1024)
	for i := 0; i < 2000; i++ {
		b.Write([]byte(chunk))
	}
	b.Write([]byte("END"))
	s := b.String()
	if len(s) > outputLimit+outputLimit/4+200 || !strings.HasSuffix(s, "END") || !strings.Contains(s, "省略") {
		t.Fatalf("len=%d", len(s))
	}
}

// k6 v2 は同じ指標の閾値があると，タグで絞った閾値を字下げした `{...}` の行で出す．
const summaryV2Nested = `
  █ THRESHOLDS 

    http_req_duration
    ✓ 'p(95)<1000' p(95)=22.94ms

      {name:heavy}
      ✗ 'p(95)<1000' p(95)=3.39s


  █ TOTAL RESULTS 
`

func TestParseSummaryNestedSubmetric(t *testing.T) {
	got := ParseSummaryThresholds(summaryV2Nested)
	if len(got) != 2 || got[1].Metric != "http_req_duration{name:heavy}" || got[1].OK || got[0].Metric != "http_req_duration" {
		t.Fatalf("%+v", got)
	}
}

// name タグを付けると k6 は url タグを name の値で上書きするので，PathTag のパスを URL として使う．
func TestReadPointsUsesPathTag(t *testing.T) {
	src := strings.Join([]string{
		`{"metric":"http_reqs","type":"Point","data":{"time":"2026-11-09T10:00:00Z","value":1,"tags":{"url":"heavy","name":"heavy","status":"200","drill_path":"/api/heavy"}}}`,
		`{"metric":"http_req_duration","type":"Point","data":{"time":"2026-11-09T10:00:00Z","value":3000,"tags":{"url":"heavy","name":"heavy","status":"200","drill_path":"/api/heavy"}}}`,
	}, "\n")
	p, err := ReadPoints(strings.NewReader(src), []string{"http_req_duration{name:heavy}"})
	if err != nil {
		t.Fatal(err)
	}
	if st := p.thresholdStats["http_req_duration{name:heavy}"]; st.urls["/api/heavy"] != 1 {
		t.Fatalf("%+v", st.urls)
	}
	if g := p.Requests[0].Tags; g["url"] != "/api/heavy" || g["drill_path"] != "" || g["name"] != "heavy" {
		t.Fatalf("%+v", g)
	}
}

func TestHasDefaultExport(t *testing.T) {
	for code, want := range map[string]bool{
		"export default function () {}":                           true,
		"function f() {}\nexport { f as default };":               true,
		"export function browse() {}\nexport const options = {};": false,
	} {
		if got, err := hasDefaultExport(code); err != nil || got != want {
			t.Errorf("%q: got %v %v", code, got, err)
		}
	}
}
