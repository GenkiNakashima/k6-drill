package app

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"k6-drill/internal/exercise"
	"k6-drill/internal/judge"
	"k6-drill/internal/runner"
	"k6-drill/internal/target"
)

// 拡張機能「k6 で実行」を本物の k6 で確かめる（採点そのものは静的解析で，judge パッケージのテストで確かめる）．
// k6 が見つからないときは飛ばす．k6 の場所は環境変数 K6_DRILL_K6 でも指定できる．
//
//	go test ./internal/app -run Acceptance -v -timeout 30m
func newAcceptanceApp(t *testing.T) *App {
	t.Helper()
	if testing.Short() {
		t.Skip("-short では k6 を動かさない")
	}
	k6 := os.Getenv("K6_DRILL_K6")
	if k6 == "" {
		p, err := exec.LookPath("k6")
		if err != nil {
			t.Skip("k6 が見つからない")
		}
		k6 = p
	}
	tgt := target.New()
	ts := httptest.NewServer(tgt.Handler())
	t.Cleanup(ts.Close)

	exFS := os.DirFS("../../exercises")
	set, err := exercise.Load(exFS)
	if err != nil {
		t.Fatal(err)
	}
	r := &runner.Runner{K6: k6, BaseURL: ts.URL, Assets: exFS, Reset: tgt.Reset}
	ver, err := r.Version(context.Background())
	if err != nil || runner.MajorVersion(ver) < 1 {
		t.Skipf("k6 v1 以上を実行できない: %v %s", err, ver)
	}
	t.Logf("k6: %s", ver)
	return &App{Set: set, Runner: r}
}

func evaluate(t *testing.T, a *App, e *exercise.Exercise, code string) *judge.Verdict {
	t.Helper()
	v, err := a.RunK6(context.Background(), e, code)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func logVerdict(t *testing.T, v *judge.Verdict) {
	t.Helper()
	t.Logf("exit=%d failures=%+v", v.ExitCode, v.Failures)
	for _, th := range v.Result.Thresholds {
		t.Logf("  %s %s ok=%v actual=%s count=%d summary=%v", th.Name, th.Expr, th.OK, th.Actual, th.Count, th.FromSummary)
	}
}

func thresholdByName(v *judge.Verdict, name string) *runner.ThresholdResult {
	for i, th := range v.Result.Thresholds {
		if th.Name == name {
			return &v.Result.Thresholds[i]
		}
	}
	return nil
}

// starterFailsOnRun は，初期コードが実機でも judge.run を満たさないはずの問題（偽りの合格などを実機で見せる問題）．
// ほかの問題の初期コードは，実機では動くが状況に合わない（静的な採点で不合格になる）．
var starterFailsOnRun = map[string]bool{
	"B02": true, "B04": true, "B05": true, "B08": true, "B09": true,
	"A01": true, "A02": true, "A03": true, "A04": true, "A05": true, "A06": true, "A07": true,
}

// judge.run のある問題で，solution は実機でも合格になる．
func TestAcceptanceStarterFailsSolutionPasses(t *testing.T) {
	a := newAcceptanceApp(t)
	for _, e := range a.Set.List {
		if !e.HasCode() || e.Judge.Run == nil {
			continue
		}
		t.Run(e.ID, func(t *testing.T) {
			v := evaluate(t, a, e, e.Starter)
			if v.Passed && starterFailsOnRun[e.ID] {
				logVerdict(t, v)
				t.Errorf("初期コードが合格した")
			}
			s := evaluate(t, a, e, e.Solution)
			if !s.Passed {
				logVerdict(t, s)
				t.Errorf("模範解答が不合格になった\n%s\n%s", s.Result.Stdout, s.Result.Stderr)
			}
			// 個別の受け入れ条件
			switch e.ID {
			case "A01":
				agg := thresholdByName(v, "http_req_duration")
				if v.ExitCode != 0 || agg == nil || !agg.OK {
					logVerdict(t, v)
					t.Errorf("A01 の初期コードは集約閾値が合格するはず")
				}
				agg = thresholdByName(s, "http_req_duration")
				tagged := thresholdByName(s, "http_req_duration{name:heavy}")
				if agg == nil || !agg.OK || tagged == nil || tagged.OK {
					logVerdict(t, s)
					t.Errorf("A01 の模範解答ではタグ付き閾値だけが不合格になるはず")
				}
			case "A02", "A03":
				if !v.ZeroSample {
					logVerdict(t, v)
					t.Errorf("%s の初期コードには測定0件の閾値が出るはず", e.ID)
				}
			}
			if (e.ID == "A03" || e.ID == "A05") && v.ExitCode != 0 {
				logVerdict(t, v)
				t.Errorf("%s の初期コードは終了コード 0 になるはず", e.ID)
			}
		})
	}
}

// VU 100 超と 60 秒超の設定は実行を断る．
func TestAcceptanceLimits(t *testing.T) {
	a := newAcceptanceApp(t)
	for name, code := range map[string]string{
		"vus":      "export const options = { vus: 101, duration: '5s' };\nexport default function () {}",
		"duration": "export const options = { vus: 1, duration: '61s' };\nexport default function () {}",
		"stages":   "export const options = { stages: [{ duration: '40s', target: 5 }, { duration: '30s', target: 0 }] };\nexport default function () {}",
	} {
		res, err := a.Runner.Run(context.Background(), code, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.Phase != "limit" || res.Error == "" {
			t.Errorf("%s: 実行を断らなかった: %+v", name, res)
		}
	}
}
