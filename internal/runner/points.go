package runner

import (
	"bufio"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RequestGroup は同じタグを持つ http_reqs の点の件数．
type RequestGroup struct {
	Tags  map[string]string `json:"tags"`
	Count int               `json:"count"`
}

// Points は `--out json` の計測値から数えたもの．
type Points struct {
	MetricTypes    map[string]metricInfo
	HTTPReqs       int
	ChecksTotal    int
	ChecksPassed   int
	CheckNames     map[string]bool
	Requests       []RequestGroup
	Seconds        float64
	thresholdStats map[string]*thresholdStat
}

type metricInfo struct {
	Type     string
	Contains string
}

type thresholdStat struct {
	name   MetricName
	values []float64
	urls   map[string]int
}

type jsonLine struct {
	Type   string `json:"type"`
	Metric string `json:"metric"`
	Data   struct {
		Name     string            `json:"name"`
		Type     string            `json:"type"`
		Contains string            `json:"contains"`
		Time     time.Time         `json:"time"`
		Value    float64           `json:"value"`
		Tags     map[string]string `json:"tags"`
	} `json:"data"`
}

// ReadPoints は計測値を1行ずつ読み，閾値ごとの測定件数と値，http_reqs の件数，check の件数を数える．
// thresholds は閾値の名前（inspect の options.thresholds のキー）．
func ReadPoints(r io.Reader, thresholds []string) (*Points, error) {
	p := &Points{
		MetricTypes:    map[string]metricInfo{},
		CheckNames:     map[string]bool{},
		thresholdStats: map[string]*thresholdStat{},
	}
	byBase := map[string][]*thresholdStat{}
	for _, t := range thresholds {
		mn := ParseMetricName(t)
		if p.thresholdStats[mn.Key()] != nil {
			continue
		}
		st := &thresholdStat{name: mn, urls: map[string]int{}}
		p.thresholdStats[mn.Key()] = st
		byBase[mn.Base] = append(byBase[mn.Base], st)
	}
	groups := map[string]*RequestGroup{}
	var first, last time.Time

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var l jsonLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if l.Type == "Metric" {
			p.MetricTypes[l.Data.Name] = metricInfo{l.Data.Type, l.Data.Contains}
			continue
		}
		if l.Type != "Point" {
			continue
		}
		if first.IsZero() || l.Data.Time.Before(first) {
			first = l.Data.Time
		}
		if l.Data.Time.After(last) {
			last = l.Data.Time
		}
		tags := l.Data.Tags
		url := tags["url"]
		if path, ok := tags[PathTag]; ok {
			url = path
		}
		switch l.Metric {
		case "http_reqs":
			p.HTTPReqs += int(l.Data.Value)
			// 判定では url をパスとして扱う（PathTag の説明を参照）
			g := make(map[string]string, len(tags))
			for k, v := range tags {
				g[k] = v
			}
			delete(g, PathTag)
			g["url"] = url
			k := tagKey(g)
			if grp := groups[k]; grp != nil {
				grp.Count++
			} else {
				groups[k] = &RequestGroup{Tags: g, Count: 1}
			}
		case "checks":
			p.ChecksTotal++
			if l.Data.Value != 0 {
				p.ChecksPassed++
			}
			if n := tags["check"]; n != "" {
				p.CheckNames[n] = true
			}
		}
		for _, st := range byBase[l.Metric] {
			if st.name.Match(tags) {
				st.values = append(st.values, l.Data.Value)
				st.urls[url]++
			}
		}
	}
	if !first.IsZero() {
		p.Seconds = last.Sub(first).Seconds()
	}
	for _, g := range groups {
		p.Requests = append(p.Requests, *g)
	}
	sort.Slice(p.Requests, func(i, j int) bool { return p.Requests[i].Count > p.Requests[j].Count })
	return p, sc.Err()
}

func tagKey(tags map[string]string) string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(strconv.Quote(k))
		b.WriteString(strconv.Quote(tags[k]))
	}
	return b.String()
}
