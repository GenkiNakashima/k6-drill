package runner

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MetricName は閾値の名前（`http_req_duration{name:heavy}` など）を分解したもの．
type MetricName struct {
	Base string
	Tags map[string]string
}

// ParseMetricName は閾値の名前を指標名とタグの組に分ける．
func ParseMetricName(s string) MetricName {
	s = strings.TrimSpace(s)
	m := MetricName{Base: s, Tags: map[string]string{}}
	i := strings.Index(s, "{")
	if i < 0 || !strings.HasSuffix(s, "}") {
		return m
	}
	m.Base = strings.TrimSpace(s[:i])
	for _, kv := range strings.Split(s[i+1:len(s)-1], ",") {
		k, v, ok := strings.Cut(kv, ":")
		if !ok {
			continue
		}
		m.Tags[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return m
}

// Key は空白やタグの順番の違いを吸収した比較用の文字列．
func (m MetricName) Key() string {
	if len(m.Tags) == 0 {
		return m.Base
	}
	keys := make([]string, 0, len(m.Tags))
	for k := range m.Tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ":" + m.Tags[k]
	}
	return m.Base + "{" + strings.Join(parts, ",") + "}"
}

// Match は点のタグが閾値のタグをすべて含むか．
func (m MetricName) Match(tags map[string]string) bool {
	for k, v := range m.Tags {
		if tags[k] != v {
			return false
		}
	}
	return true
}

// NormalizeExpr は式の空白を取り除く．`p(95) < 1000` と `p(95)<1000` を同じと見なすため．
func NormalizeExpr(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// Expr は閾値の式（`p(95)<1000` など）を分解したもの．
type Expr struct {
	Agg   string // avg, min, max, med, count, rate, value, p
	P     float64
	Op    string
	Value float64
}

var exprRe = regexp.MustCompile(`^\s*(avg|min|max|med|count|rate|value|p\(\s*(\d+(?:\.\d+)?)\s*\))\s*(<=|>=|===|==|!=|<|>)\s*(-?\d+(?:\.\d+)?)\s*$`)

// ParseExpr は閾値の式を読む．
func ParseExpr(s string) (Expr, error) {
	m := exprRe.FindStringSubmatch(s)
	if m == nil {
		return Expr{}, fmt.Errorf("閾値の式 %q を読めません", s)
	}
	e := Expr{Agg: m[1], Op: m[3]}
	if m[2] != "" {
		e.Agg = "p"
		e.P, _ = strconv.ParseFloat(m[2], 64)
	}
	e.Value, _ = strconv.ParseFloat(m[4], 64)
	return e, nil
}

// Label は集計方法の表示名（`p(95)` など）．
func (e Expr) Label() string {
	if e.Agg == "p" {
		return "p(" + strconv.FormatFloat(e.P, 'f', -1, 64) + ")"
	}
	return e.Agg
}

// Holds は値が式を満たすか．
func (e Expr) Holds(v float64) bool {
	switch e.Op {
	case "<":
		return v < e.Value
	case "<=":
		return v <= e.Value
	case ">":
		return v > e.Value
	case ">=":
		return v >= e.Value
	case "==", "===":
		return v == e.Value
	case "!=":
		return v != e.Value
	}
	return false
}

// Aggregate は k6 と同じ方法で値を集計する．点が0件なら0を返す（k6 と同じ）．
func Aggregate(e Expr, metricType string, values []float64, seconds float64) float64 {
	n := len(values)
	if n == 0 {
		return 0
	}
	switch e.Agg {
	case "count":
		if metricType == "counter" {
			return sum(values)
		}
		return float64(n)
	case "rate":
		if metricType == "counter" {
			if seconds <= 0 {
				return 0
			}
			return sum(values) / seconds
		}
		nz := 0
		for _, v := range values {
			if v != 0 {
				nz++
			}
		}
		return float64(nz) / float64(n)
	case "value":
		return values[n-1]
	case "avg":
		return sum(values) / float64(n)
	}
	s := append([]float64(nil), values...)
	sort.Float64s(s)
	switch e.Agg {
	case "min":
		return s[0]
	case "max":
		return s[n-1]
	case "med":
		return percentile(s, 50)
	case "p":
		return percentile(s, e.P)
	}
	return 0
}

func sum(v []float64) float64 {
	t := 0.0
	for _, x := range v {
		t += x
	}
	return t
}

// percentile は k6 の TrendSink.P と同じく線形補間で求める．
func percentile(sorted []float64, pct float64) float64 {
	n := len(sorted)
	if n == 1 {
		return sorted[0]
	}
	idx := pct / 100 * float64(n-1)
	lo := math.Floor(idx)
	hi := math.Ceil(idx)
	if lo == hi {
		return sorted[int(lo)]
	}
	return sorted[int(lo)] + (idx-lo)*(sorted[int(hi)]-sorted[int(lo)])
}

// FormatValue は集計値を k6 の集計表示に近い形にする．
func FormatValue(e Expr, metricType, contains string, v float64) string {
	label := e.Label()
	switch {
	case e.Agg == "rate" && metricType == "rate":
		return fmt.Sprintf("%s=%.2f%%", label, v*100)
	case contains == "time":
		return label + "=" + formatMS(v)
	}
	return label + "=" + strconv.FormatFloat(v, 'f', -1, 64)
}

func formatMS(ms float64) string {
	switch {
	case ms == 0:
		return "0s"
	case ms < 1:
		return fmt.Sprintf("%.2fµs", ms*1000)
	case ms < 1000:
		return fmt.Sprintf("%.2fms", ms)
	}
	return fmt.Sprintf("%.2fs", ms/1000)
}

// SummaryThreshold は k6 の終了時の集計表示の THRESHOLDS 欄から読んだ1行．
type SummaryThreshold struct {
	Metric string
	Expr   string
	OK     bool
	Actual string
}

var summaryLineRe = regexp.MustCompile(`^([✓✗])\s+'(.+)'\s*(.*)$`)

// ParseSummaryThresholds は標準出力の THRESHOLDS 欄から閾値ごとの合否と実測値を取り出す．
// 同じ指標の閾値があると，タグで絞った閾値は `{name:heavy}` だけの行としてその下に字下げして出る．
//
//	█ THRESHOLDS
//
//	  http_req_duration
//	  ✓ 'p(95)<1000' p(95)=22.94ms
//
//	    {name:heavy}
//	    ✗ 'p(95)<1000' p(95)=3.39s
func ParseSummaryThresholds(stdout string) []SummaryThreshold {
	var out []SummaryThreshold
	in := false
	metric, base := "", ""
	for _, line := range strings.Split(stdout, "\n") {
		t := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if strings.Contains(t, "█") {
			in = strings.Contains(t, "THRESHOLDS")
			continue
		}
		if !in || t == "" {
			continue
		}
		if m := summaryLineRe.FindStringSubmatch(t); m != nil {
			out = append(out, SummaryThreshold{Metric: metric, Expr: m[2], OK: m[1] == "✓", Actual: strings.TrimSpace(m[3])})
			continue
		}
		if strings.HasPrefix(t, "{") {
			metric = base + t
			continue
		}
		metric = t
		base = ParseMetricName(t).Base
	}
	return out
}
