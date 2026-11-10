package analyze

import (
	"testing"
)

func mustParse(t *testing.T, code string) *Model {
	t.Helper()
	m, err := Parse(code)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRequestsAndURL(t *testing.T) {
	m := mustParse(t, `
import http from 'k6/http';
import { check, sleep } from 'k6';
const BASE_URL = __ENV.BASE_URL;
const params = { headers: { 'Content-Type': 'application/json' }, tags: { name: 'login' } };
export const options = { vus: 5, duration: '10s' };
export default function () {
  http.post(BASE_URL + '/api/login', JSON.stringify({ user: 'drill' }), params);
  for (let i = 0; i < 3; i++) {
    const id = i + 1;
    http.get(`+"`${BASE_URL}/api/items/${id}?x=1`"+`);
  }
  http.get('http://localhost:9999/api/ping');
  sleep(1);
}`)
	if len(m.Requests) != 3 {
		t.Fatalf("%+v", m.Requests)
	}
	login, item, ping := m.Requests[0], m.Requests[1], m.Requests[2]
	if login.Method != "POST" || login.Path != "/api/login" || !login.UsesBaseURL || login.Body != "json" ||
		login.Headers["Content-Type"] != "application/json" || login.Tags["name"] != "login" || login.Func != "default" {
		t.Fatalf("login: %+v", login)
	}
	if item.Path != "/api/items/{}" || item.Query != "x=1" || !item.InLoop {
		t.Fatalf("item: %+v", item)
	}
	if ping.UsesBaseURL || ping.Host != "localhost:9999" || ping.Path != "/api/ping" {
		t.Fatalf("ping: %+v", ping)
	}
	if m.Exec.MaxVUs != 5 || len(m.Sleeps) != 1 || m.EnvVars[0] != "BASE_URL" {
		t.Fatalf("%+v", m)
	}
}

func TestZeroSampleThreshold(t *testing.T) {
	src := func(tag string) string {
		return `
import http from 'k6/http';
export const options = { thresholds: { 'http_req_duration{name:heavy}': ['p(95)<1000'] } };
export default function () {
  http.get(` + "`${__ENV.BASE_URL}/api/heavy`" + tag + `);
}`
	}
	m := mustParse(t, src(", { tags: { name: 'Heavy' } }"))
	if !m.HasProblem("zero_sample") {
		t.Fatalf("Heavy と heavy の違いを見逃した: %+v", m.Problems)
	}
	m = mustParse(t, src(""))
	if !m.HasProblem("zero_sample") {
		t.Fatalf("タグなしのリクエストは name が URL になる: %+v", m.Problems)
	}
	m = mustParse(t, src(", { tags: { name: 'heavy' } }"))
	if m.HasProblem("zero_sample") {
		t.Fatalf("%+v", m.Problems)
	}
	res := m.Measures(m.Thresholds[0])
	if !res.Certain || len(res.Requests) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestScenarioTags(t *testing.T) {
	m := mustParse(t, `
import http from 'k6/http';
export const options = {
  scenarios: {
    browse: { executor: 'constant-vus', vus: 5, duration: '10s', exec: 'browse' },
    upload: { executor: 'per-vu-iterations', vus: 2, iterations: 5, exec: 'upload' },
  },
  thresholds: {
    'http_req_duration{scenario:browse}': ['p(95)<500'],
    'http_req_duration{scenario:uplaod}': ['p(95)<500'],
  },
};
export function browse() { http.get(` + "`${__ENV.BASE_URL}/api/items`" + `); }
export const upload = () => { http.post(` + "`${__ENV.BASE_URL}/api/files`" + `, {}); };
`)
	if got := m.Measures(m.Thresholds[0]); !got.Certain || len(got.Requests) != 1 {
		t.Fatalf("browse: %+v", got)
	}
	if !m.HasProblem("zero_sample") || m.Requests[1].Func != "upload" {
		t.Fatalf("uplaod の綴り違いを見逃した: %+v", m.Problems)
	}
	if m.HasProblem("exec_not_exported") || m.Exec.MaxVUs != 7 {
		t.Fatalf("%+v", m.Problems)
	}
}

func TestProblems(t *testing.T) {
	m := mustParse(t, `
import http from 'k6/http';
const options = { thresholds: { http_req_duration: 'p95<300' } };
http.get('http://x/api/ping');
export default function () {
  const img = open('./a.png', 'b');
  const base = process.env.BASE_URL;
}`)
	for _, c := range []string{"options_not_exported", "threshold_not_array", "threshold_expr", "open_in_vu", "process", "http_in_init"} {
		if !m.HasProblem(c) {
			t.Errorf("%s を見逃した: %+v", c, m.Problems)
		}
	}
	m = mustParse(t, "export default function main() { const f = () => open('x'); }")
	if !m.Exports["default"] || m.Opens[0].Func != "default" {
		t.Fatalf("%+v %+v", m.Exports, m.Opens)
	}
}

func TestChecksAndSetup(t *testing.T) {
	m := mustParse(t, `
import http from 'k6/http';
import { check } from 'k6';
export function setup() {
  const res = http.post('x');
  return { token: res.json('token') };
}
export default function (data) {
  const res = http.get('y', { headers: { Authorization: `+"`Bearer ${data.token}`"+` } });
  check(res, { 'status is 200': (r) => r.status === 200, 'has 10': (r) => r.json().length === 10 });
}`)
	if !m.SetupReturns || m.DefaultParams != 1 || len(m.Checks) != 1 || len(m.Checks[0].Names) != 2 {
		t.Fatalf("%+v", m)
	}
	if m.Checks[0].Bodies[0] != "(r) => r.status === 200" || m.Requests[1].Headers["Authorization"] == "" {
		t.Fatalf("%+v %+v", m.Checks, m.Requests)
	}
	if _, err := Parse("export default function ( {"); err == nil {
		t.Fatal("構文の誤りを見逃した")
	}
}
