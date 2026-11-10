package judge

import (
	"os"
	"strings"
	"testing"

	"k6-drill/internal/analyze"
	"k6-drill/internal/exercise"
	"k6-drill/internal/runner"
)

func loadSet(t *testing.T) *exercise.Set {
	t.Helper()
	set, err := exercise.Load(os.DirFS("../../exercises"))
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func check(t *testing.T, e *exercise.Exercise, code string) StaticVerdict {
	t.Helper()
	in := StaticInput{Exercise: e, Code: code}
	if e.Judge.Static.ThresholdsUnchanged {
		in.Starter, _ = analyze.Parse(e.Starter)
	}
	return EvaluateStatic(in)
}

// 受け入れ条件：すべてのコードの問題で，初期コードのままでは不合格，solution では合格になる（静的な採点）．
func TestStarterFailsSolutionPasses(t *testing.T) {
	for _, e := range loadSet(t).List {
		if !e.HasCode() {
			continue
		}
		t.Run(e.ID, func(t *testing.T) {
			if v := check(t, e, e.Starter); v.Passed {
				t.Errorf("初期コードが合格した")
			} else if len(v.Hints) == 0 {
				t.Errorf("初期コードの不合格にヒントが1つも出ない: %+v", v.Failures)
			}
			if v := check(t, e, e.Solution); !v.Passed {
				t.Errorf("模範解答が不合格: %+v", v.Failures)
			}
		})
	}
}

// 応用問題の「よくある誤り」を，採点が見抜けるか．
func TestTraps(t *testing.T) {
	set := loadSet(t)
	cases := []struct {
		id, from, to string
		pass         bool
		wantCode     string
	}{
		// A01：タグの名前は自由
		{"A01", "name: 'heavy'", "endpoint: 'heavy'", false, "thresholds"},
		{"A01", "'http_req_duration{name:heavy}'", "'http_req_duration{endpoint:heavy}'", false, "problem.zero_sample"},
		// A01：heavy と items に同じタグを付けると，heavy だけを測る閾値ではない
		{"A01", "{ tags: { name: 'items' } }", "{ tags: { name: 'heavy' } }", false, "thresholds"},
		// A01：上限を緩めすぎ
		{"A01", "'http_req_duration{name:heavy}': ['p(95)<1000']", "'http_req_duration{name:heavy}': ['p(95)<10000']", false, "thresholds"},
		// A02：閾値を緩めて合わせるのは不可，閾値側のタグを合わせるのも不可
		{"A02", "['p(95)<1000']", "['p(95)<10000']", false, "thresholds_unchanged"},
		// A03：ヘッダーなし
		{"A03", "headers: { 'X-User-ID': 'student-1' }, ", "", false, "requests"},
		// A04：シナリオ名の綴り違いは測定0件
		{"A04", "'http_req_duration{scenario:upload}'", "'http_req_duration{scenario:uplaod}'", false, "problem.zero_sample"},
		// A04：exec の関数を export していない
		{"A04", "export function upload()", "function upload()", false, "problem.exec_not_exported"},
		// A07：閾値を変えた
		{"A07", "vus: 20", "vus: 5", false, "min_vus"},
		// A09：preAllocatedVUs がない
		{"A09", "preAllocatedVUs: 20,\n", "", false, "problem.arrival_prealloc"},
		// B07：Content-Type なし
		{"B07", "{ headers: { 'Content-Type': 'application/json' } },", "", false, "requests"},
		// B12：default でもログインしている
		{"B12", "export default function (data) {\n", "export default function (data) {\n  http.post(`${BASE_URL}/api/login`, '{}');\n", false, "requests"},
		// B05：p95 は k6 が読めない
		{"B05", "'p(95)<500'", "'p95<500'", false, "problem.threshold_expr"},
	}
	for _, c := range cases {
		e := set.Get(c.id)
		if !strings.Contains(e.Solution, c.from) {
			t.Fatalf("%s: 置き換え元 %q が模範解答にない", c.id, c.from)
		}
		code := strings.Replace(e.Solution, c.from, c.to, 1)
		v := check(t, e, code)
		if v.Passed != c.pass {
			t.Errorf("%s %q→%q: passed=%v failures=%+v", c.id, c.from, c.to, v.Passed, v.Failures)
			continue
		}
		if c.wantCode != "" {
			found := false
			for _, f := range v.Failures {
				found = found || f.Code == c.wantCode
			}
			if !found {
				t.Errorf("%s %q→%q: %s が出ない: %+v", c.id, c.from, c.to, c.wantCode, v.Failures)
			}
		}
	}
}

// 正しいが模範解答と違う書き方も合格する．
func TestAlternativeSolutions(t *testing.T) {
	set := loadSet(t)
	alt := map[string]string{
		"A01": strings.NewReplacer("name: 'heavy'", "endpoint: 'heavy'", "{name:heavy}", "{endpoint:heavy}").Replace(set.Get("A01").Solution),
		"B07": strings.Replace(set.Get("B07").Solution, "'Content-Type'", "'content-type'", 1),
		"B02": strings.Replace(set.Get("B02").Solution, "duration: '10s'", "duration: '10000ms'", 1),
		"A06": strings.Replace(set.Get("A06").Solution, "'p(95)<3000'", "'p(95)<5000', 'avg<2000'", 1),
	}
	for id, code := range alt {
		if v := check(t, set.Get(id), code); !v.Passed {
			t.Errorf("%s: 別解が不合格: %+v", id, v.Failures)
		}
	}
}

func TestSyntaxError(t *testing.T) {
	e := loadSet(t).Get("B01")
	v := check(t, e, "export default function () {")
	if v.Passed || v.Failures[0].Code != "syntax" || v.Analysis.SyntaxErr == "" {
		t.Fatalf("%+v", v)
	}
}

func TestAnalysisSummary(t *testing.T) {
	e := loadSet(t).Get("A02")
	v := check(t, e, e.Starter)
	if len(v.Analysis.Thresholds) != 1 || !v.Analysis.Thresholds[0].Zero || len(v.Analysis.Requests) != 1 {
		t.Fatalf("%+v", v.Analysis)
	}
	v = check(t, e, e.Solution)
	th := v.Analysis.Thresholds[0]
	if th.Zero || len(th.Targets) != 1 || !strings.HasPrefix(th.Targets[0], "GET /api/heavy") {
		t.Fatalf("%+v", th)
	}
	if v.Analysis.Load.MaxVUs != 20 || v.Analysis.Load.Duration != "20s" {
		t.Fatalf("%+v", v.Analysis.Load)
	}
}

// 実機実行（拡張機能）の判定．
func TestRunVerdict(t *testing.T) {
	ex := loadSet(t).Get("A02")
	res := &runner.Result{
		Phase: "run", ExitCode: 0,
		Thresholds: []runner.ThresholdResult{{Name: "http_req_duration{name:heavy}", Expr: "p(95)<1000", OK: true, Actual: "p(95)=0s", Count: 0, Tagged: true}},
	}
	v := EvaluateRun(Input{Exercise: ex, Result: res})
	if v.Passed || !v.ZeroSample || v.ExitNote != "0 は「何かを検証できた」ことを意味しない" {
		t.Fatalf("%+v", v)
	}
	res.ExitCode = 99
	res.Thresholds[0] = runner.ThresholdResult{Name: "http_req_duration{name:heavy}", Expr: "p(95)<1000", OK: false, Count: 100, Tagged: true}
	if v := EvaluateRun(Input{Exercise: ex, Result: res}); !v.Passed {
		t.Fatalf("%+v", v.Failures)
	}
	v = EvaluateRun(Input{Exercise: ex, Result: &runner.Result{Phase: "inspect", ExitCode: 104, Error: "k6 inspect が失敗しました"}})
	if v.Passed || v.Failures[0].Code != "run.not_executed" {
		t.Fatalf("%+v", v)
	}
}
