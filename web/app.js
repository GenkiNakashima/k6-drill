'use strict';

// ---- 小さな道具 ----

const $ = (id) => document.getElementById(id);

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

async function api(method, path, body) {
  const opt = { method, headers: { 'X-K6-Drill': '1' } };
  if (body !== undefined) {
    opt.headers['Content-Type'] = 'application/json';
    opt.body = JSON.stringify(body);
  }
  const res = await fetch(path, opt);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `${res.status} ${res.statusText}`);
  return data;
}

// ---- Markdown（問題文と解説に要る分だけ） ----

function inline(s) {
  const parts = s.split(/(`[^`]+`)/);
  return parts.map((p) => {
    if (p.startsWith('`') && p.endsWith('`') && p.length > 1) return `<code>${esc(p.slice(1, -1))}</code>`;
    let h = esc(p);
    h = h.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
    h = h.replace(/\[([^\]]+)\]\((https?:[^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');
    h = h.replace(/(^|[\s（(：，、])(https?:\/\/[^\s<）)，、]+)/g, '$1<a href="$2" target="_blank" rel="noopener">$2</a>');
    return h;
  }).join('');
}

function markdown(src) {
  const lines = String(src || '').replace(/\r/g, '').split('\n');
  const out = [];
  let i = 0;
  const isTableRow = (l) => /^\s*\|.*\|\s*$/.test(l);
  while (i < lines.length) {
    const l = lines[i];
    if (/^```/.test(l)) {
      const buf = [];
      for (i++; i < lines.length && !/^```/.test(lines[i]); i++) buf.push(lines[i]);
      i++;
      out.push(`<pre><code>${esc(buf.join('\n'))}</code></pre>`);
      continue;
    }
    const h = l.match(/^(#{1,4})\s+(.*)$/);
    if (h) { out.push(`<h3>${inline(h[2])}</h3>`); i++; continue; }
    if (isTableRow(l) && i + 1 < lines.length && /^\s*\|[\s:|-]+\|\s*$/.test(lines[i + 1])) {
      const cells = (r) => r.trim().slice(1, -1).split('|').map((c) => inline(c.trim()));
      let t = '<table><thead><tr>' + cells(l).map((c) => `<th>${c}</th>`).join('') + '</tr></thead><tbody>';
      for (i += 2; i < lines.length && isTableRow(lines[i]); i++) {
        t += '<tr>' + cells(lines[i]).map((c) => `<td>${c}</td>`).join('') + '</tr>';
      }
      out.push(t + '</tbody></table>');
      continue;
    }
    if (/^\s*([-*]|\d+\.)\s+/.test(l)) {
      const ordered = /^\s*\d+\./.test(l);
      const items = [];
      while (i < lines.length && /^\s*([-*]|\d+\.)\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*([-*]|\d+\.)\s+/, ''));
        i++;
      }
      const tag = ordered ? 'ol' : 'ul';
      out.push(`<${tag}>` + items.map((it) => `<li>${inline(it)}</li>`).join('') + `</${tag}>`);
      continue;
    }
    if (l.trim() === '') { i++; continue; }
    const buf = [];
    while (i < lines.length && lines[i].trim() !== '' && !/^```|^#{1,4}\s|^\s*([-*]|\d+\.)\s+/.test(lines[i]) && !isTableRow(lines[i])) {
      buf.push(lines[i]);
      i++;
    }
    out.push(`<p>${buf.map(inline).join('<br>')}</p>`);
  }
  return out.join('\n');
}

// ---- 状態 ----

const state = {
  list: [],
  current: null, // 開いている問題の詳細
  saveTimer: null,
  runTimer: null,
  answers: [],
  k6ok: false,
};

// ---- 起動時の確認 ----

async function checkStatus() {
  const s = await api('GET', '/api/status');
  $('target-url').textContent = s.target_url;
  state.k6ok = !!s.k6_ok && !!s.target_ok;
  const b = $('status-banner');
  if (s.k6_ok) {
    b.hidden = true;
    return;
  }
  // 採点に k6 は要らない．拡張機能を使いたい人のために，入れ方だけを折りたたんで出す
  const names = { darwin: 'Mac', windows: 'Windows', linux: 'Ubuntu' };
  const order = [s.os, ...['darwin', 'windows', 'linux'].filter((o) => o !== s.os)];
  let h = `<details class="k6info"><summary>採点に k6 は要りません．「k6 で実行（拡張）」を使うには k6（v1 以上）が必要です（${esc(s.k6_error || 'k6 が見つかりません')}）</summary>`;
  for (const o of order) h += `<div><strong>${names[o]}</strong><pre>${esc(s.install[o].join('\n'))}</pre></div>`;
  h += `<p>詳しくは <a href="${esc(s.install_doc)}" target="_blank" rel="noopener">${esc(s.install_doc)}</a>．入れたら k6ドリルを起動し直してください．</p></details>`;
  b.className = 'banner info';
  b.innerHTML = h;
  b.hidden = false;
}

// ---- 問題一覧 ----

const STATUS_MARK = { not_started: '○', trying: '◐', passed: '●' };
const STATUS_LABEL = { not_started: '未着手', trying: '挑戦中', passed: '合格' };

async function loadList() {
  const d = await api('GET', '/api/exercises');
  state.list = d.exercises;
  const pct = d.total ? Math.round((d.done / d.total) * 100) : 0;
  $('progress-fill').style.width = pct + '%';
  $('progress-text').textContent = `${d.done} / ${d.total} 問合格・残り約 ${d.remaining_minutes} 分（任意問題を除く）`;
  for (const level of ['basic', 'advanced']) {
    const ol = $('list-' + level);
    ol.innerHTML = '';
    for (const e of d.exercises.filter((x) => x.level === level)) {
      const li = document.createElement('li');
      const a = document.createElement('a');
      a.href = '#' + e.id;
      a.className = (e.locked ? 'locked ' : '') + (state.current && state.current.exercise.id === e.id ? 'current' : '');
      a.title = STATUS_LABEL[e.status];
      a.innerHTML = `<span class="st ${e.status}">${e.locked ? '🔒' : STATUS_MARK[e.status]}</span>
        <span>${esc(e.id)} ${esc(e.title)}</span>
        ${e.optional ? '<span class="opt">任意</span>' : ''}
        <span class="mins">${e.minutes}分</span>`;
      li.appendChild(a);
      ol.appendChild(li);
    }
  }
  const anyLocked = d.exercises.some((e) => e.locked);
  $('advanced-note').hidden = !anyLocked;
}

// ---- 問題の表示 ----

async function openExercise(id) {
  if (!id) return;
  let d;
  try {
    d = await api('GET', '/api/exercises/' + encodeURIComponent(id));
  } catch (err) {
    alertBanner(err.message);
    return;
  }
  state.current = d;
  const e = d.exercise;
  $('welcome').hidden = true;
  $('recipes').hidden = true;
  $('exercise').hidden = false;
  $('ex-id').textContent = e.id;
  $('ex-title').textContent = e.title;
  $('ex-meta').textContent = `目安 ${e.minutes} 分` + (e.optional ? '・任意' : '') + `・${STATUS_LABEL[d.progress.status]}`;
  $('ex-docs').innerHTML = (e.doc || []).map((u) => `<a href="${esc(u)}" target="_blank" rel="noopener">参照：${esc(u.replace(/^https:\/\/grafana\.com\/docs\/k6\/latest\//, ''))}</a>`).join('');
  $('ex-statement').innerHTML = markdown(e.statement);
  renderTips(e.tips || []);

  renderQuiz(d);

  const hasCode = e.type === 'code';
  $('code').hidden = !hasCode;
  if (hasCode) setCode(d.progress.code || e.starter);
  $('result').hidden = true;
  $('result').innerHTML = '';
  $('run-result').hidden = true;
  $('run-result').innerHTML = '';
  $('run').hidden = !(hasCode && state.k6ok);
  renderExplanation(d);
  await loadList();
  window.scrollTo(0, 0);
}

// ヒントは1つずつ開く．前のヒントを開くまで，次のヒントは開けない
function renderTips(tips) {
  $('tips').hidden = tips.length === 0;
  const box = $('tips-list');
  box.innerHTML = '';
  tips.forEach((t, i) => {
    const d = document.createElement('details');
    d.innerHTML = `<summary>ヒント${i + 1}${t.title ? '：' + esc(t.title) : ''}</summary><div class="md">${markdown(t.text)}</div>`;
    if (i > 0) d.classList.add('locked');
    d.addEventListener('toggle', () => {
      const next = box.children[i + 1];
      if (d.open && next) next.classList.remove('locked');
    });
    d.querySelector('summary').addEventListener('click', (ev) => {
      if (d.classList.contains('locked')) ev.preventDefault();
    });
    box.appendChild(d);
  });
}

function renderExplanation(d) {
  const ex = $('explanation');
  ex.hidden = !d.explanation;
  if (d.explanation) $('explanation-body').innerHTML = markdown(d.explanation);
  const sol = $('solution');
  sol.hidden = !d.solution;
  if (d.solution) {
    $('solution-scenario').textContent = d.exercise.scenario ? '状況：' + d.exercise.scenario : '';
    $('solution-code').textContent = d.solution;
  }
  $('reveal').hidden = !d.can_reveal;
}

function setMeta(d) {
  $('ex-meta').textContent = `目安 ${d.exercise.minutes} 分` + (d.exercise.optional ? '・任意' : '') + `・${STATUS_LABEL[d.progress.status]}`;
}

function alertBanner(msg) {
  const b = $('status-banner');
  b.hidden = false;
  b.textContent = msg;
}

// ---- 選択式 ----

function renderQuiz(d, results) {
  const qs = d.quiz || [];
  $('quiz').hidden = qs.length === 0;
  if (!qs.length) return;
  if (!results) state.answers = qs.map(() => -1);
  const box = $('quiz-questions');
  box.innerHTML = '';
  qs.forEach((q, qi) => {
    const div = document.createElement('div');
    div.className = 'question';
    let h = `<h4>問${qi + 1}．${inline(q.question)}</h4>`;
    if (q.code) h += `<pre>${esc(q.code)}</pre>`;
    q.choices.forEach((c, ci) => {
      let cls = 'choice';
      let explain = '';
      if (results) {
        const r = results[qi];
        if (ci === r.correct_index) cls += ' correct';
        else if (ci === state.answers[qi]) cls += ' wrong';
        explain = `<span class="explain">${ci === r.correct_index ? '正解．' : ''}${inline(r.explains[ci] || '')}</span>`;
      }
      h += `<label class="${cls}"><input type="radio" name="q${qi}" value="${ci}" ${state.answers[qi] === ci ? 'checked' : ''}> ${inline(c.text)}${explain}</label>`;
    });
    div.innerHTML = h;
    div.addEventListener('change', (ev) => { state.answers[qi] = Number(ev.target.value); });
    box.appendChild(div);
  });
  const sum = $('quiz-summary');
  if (!results) {
    sum.textContent = d.progress.quiz_passed ? 'この設問には合格しています．' : '';
  }
}

async function checkQuiz() {
  if (state.answers.some((a) => a < 0)) {
    $('quiz-summary').textContent = 'すべての設問に答えてください．';
    return;
  }
  const id = state.current.exercise.id;
  const d = await api('POST', `/api/exercises/${id}/quiz`, { answers: state.answers });
  state.current = { ...state.current, ...d };
  renderQuiz(d, d.quiz_results);
  setMeta(d);
  const n = d.quiz_results.filter((r) => r.correct).length;
  $('quiz-summary').textContent = d.quiz_all_correct
    ? `全問正解です（${n} / ${d.quiz_results.length}）．` + (state.current.exercise.type === 'code' && !d.progress.code_passed ? '続けて下のコードを直してください．' : '')
    : `${n} / ${d.quiz_results.length} 問正解．解説を読んで，もう一度選んでください．`;
  renderExplanation(d);
  await loadList();
}

// ---- エディタ ----

function setCode(code) {
  $('code-input').value = code;
  updateGutter();
}

function updateGutter() {
  const ta = $('code-input');
  const n = ta.value.split('\n').length;
  let s = '';
  for (let i = 1; i <= n; i++) s += i + '\n';
  $('gutter').textContent = s;
  $('gutter').scrollTop = ta.scrollTop;
}

function scheduleSave() {
  clearTimeout(state.saveTimer);
  $('save-state').textContent = '';
  state.saveTimer = setTimeout(async () => {
    if (!state.current) return;
    try {
      await api('PUT', `/api/exercises/${state.current.exercise.id}/code`, { code: $('code-input').value });
      $('save-state').textContent = '書きかけのコードを保存しました';
    } catch (err) {
      $('save-state').textContent = '保存できませんでした：' + err.message;
    }
  }, 800);
}

function onKeyDown(ev) {
  const ta = ev.target;
  if (ev.key === 'Tab') {
    ev.preventDefault();
    const { selectionStart: s, selectionEnd: e, value: v } = ta;
    if (ev.shiftKey) {
      const ls = v.lastIndexOf('\n', s - 1) + 1;
      if (v.slice(ls, ls + 2) === '  ') {
        ta.value = v.slice(0, ls) + v.slice(ls + 2);
        ta.selectionStart = ta.selectionEnd = Math.max(ls, s - 2);
      }
    } else {
      ta.value = v.slice(0, s) + '  ' + v.slice(e);
      ta.selectionStart = ta.selectionEnd = s + 2;
    }
    onInput();
  } else if (ev.key === 'Enter' && !ev.isComposing) {
    if (ev.metaKey || ev.ctrlKey) {
      ev.preventDefault();
      checkCode();
      return;
    }
    // 前の行の字下げを引き継ぐ
    const { selectionStart: s, value: v } = ta;
    const ls = v.lastIndexOf('\n', s - 1) + 1;
    const indent = v.slice(ls).match(/^ */)[0];
    const extra = /[{[(]\s*$/.test(v.slice(ls, s)) ? '  ' : '';
    ev.preventDefault();
    ta.setRangeText('\n' + indent + extra, s, ta.selectionEnd, 'end');
    onInput();
  }
}

function onInput() {
  updateGutter();
  scheduleSave();
}

// ---- 採点（静的解析） ----

async function checkCode() {
  if (!state.current || $('check').disabled) return;
  const id = state.current.exercise.id;
  $('check').disabled = true;
  try {
    const d = await api('POST', `/api/exercises/${id}/check`, { code: $('code-input').value });
    if (state.current.exercise.id !== id) return;
    state.current = { ...state.current, ...d };
    renderStatic(d.verdict);
    renderExplanation(d);
    setMeta(d);
    await loadList();
  } catch (err) {
    $('result').hidden = false;
    $('result').innerHTML = `<div class="verdict fail">採点できません</div><p>${esc(err.message)}</p>`;
  } finally {
    $('check').disabled = false;
  }
}

async function revealSolution() {
  const id = state.current.exercise.id;
  try {
    const d = await api('POST', `/api/exercises/${id}/reveal`);
    state.current = { ...state.current, ...d };
    renderExplanation(d);
    $('solution').scrollIntoView({ behavior: 'smooth', block: 'start' });
  } catch (err) {
    alertBanner(err.message);
  }
}

function tagsHtml(tags) {
  const keys = Object.keys(tags || {});
  if (!keys.length) return '<span class="muted">なし</span>';
  return keys.map((k) => `<span class="tag">${esc(k)}: ${esc(tags[k])}</span>`).join('');
}

const BODY_LABEL = { json: 'JSON', object: 'フォーム', file: 'ファイル（multipart）', string: '文字列', other: '式' };

function renderStatic(v) {
  const a = v.analysis || {};
  const ex = state.current.exercise;
  const needQuiz = (state.current.quiz || []).length > 0 && !state.current.progress.quiz_passed;
  let h = `<div class="verdict ${v.passed ? 'pass' : 'fail'}">${v.passed ? '合格' : '不合格'}</div>`;
  if (v.passed && needQuiz) h += '<p>コードは合格です．上の設問にも答えると，この問題が合格になります．</p>';
  if (v.failures.length) {
    h += '<h3>満たしていない条件</h3><ul class="failures">' + v.failures.map((f) => `<li>${inline(f.message)}</li>`).join('') + '</ul>';
  }
  if (v.hints && v.hints.length) {
    h += '<h3>ヒント</h3><ul class="hints">' + v.hints.map((t) => `<li>${inline(t)}</li>`).join('') + '</ul>';
  }
  if (!v.passed && state.current.can_reveal) {
    h += '<p class="muted small">行き詰まったら「模範コードを見る」で，この状況での書き方を確かめられます．</p>';
  }

  h += '<div class="analysis"><h3>コードから読み取ったこと</h3>';
  if (a.syntax_error) {
    h += `<p class="failures">構文の誤り：<code>${esc(a.syntax_error)}</code></p></div>`;
    $('result').innerHTML = h;
    $('result').hidden = false;
    return;
  }
  const l = a.load || {};
  let load = `VU の最大 <strong>${l.max_vus}</strong>`;
  if (l.duration) load += `・時間 <strong>${esc(l.duration)}</strong>`;
  if (l.stages && l.stages.length) {
    load += '・stages ' + l.stages.map((st) => `${(st.duration / 1e9).toFixed(0)}秒で${st.target}`).join(' → ');
  }
  h += `<p class="stats">負荷：${load}</p>`;
  if (l.scenarios && l.scenarios.length) {
    h += '<p class="stats">シナリオ：' + l.scenarios.map((sc) => `<span class="tag">${esc(sc.name)}</span>${esc(sc.executor)}・exec ${esc(sc.exec)}・最大 ${sc.max_vus} VU`).join('　') + '</p>';
  }

  const reqs = a.requests || [];
  if (reqs.length) {
    h += '<table class="th"><thead><tr><th>行</th><th>場所</th><th>リクエスト</th><th>タグ</th><th>ヘッダー</th><th>本文</th></tr></thead><tbody>';
    for (const r of reqs) {
      const url = (r.uses_base_url ? '<span class="muted">${BASE_URL}</span>' : r.host ? `<span class="muted">${esc(r.host)}</span>` : '') + esc(r.path) + (r.query ? esc('?' + r.query) : '');
      h += `<tr><td class="num">${r.line}</td><td>${esc(r.place)}${r.in_loop ? '<br><span class="muted small">繰り返しの中</span>' : ''}</td>
        <td><code>${esc(r.method)}</code> <code>${url}</code></td><td>${tagsHtml(r.tags)}</td><td>${tagsHtml(r.headers)}</td>
        <td>${esc(BODY_LABEL[r.body] || '')}</td></tr>`;
    }
    h += '</tbody></table>';
  } else {
    h += '<p class="muted">HTTP リクエストがありません．</p>';
  }

  const th = a.thresholds || [];
  if (th.length) {
    h += '<table class="th"><thead><tr><th>閾値</th><th>式</th><th>この閾値が測るリクエスト</th></tr></thead><tbody>';
    for (const t of th) {
      let target;
      if (t.all) target = '<span class="muted">すべて（タグで絞っていない）</span>';
      else if (t.zero) target = '<span class="zero-note">0件：どのリクエストにも当たらない（何も測らずに合格する）</span>';
      else if (t.targets && t.targets.length) target = t.targets.map(esc).join('<br>') + (t.certain ? '' : '<br><span class="muted small">（ほかにも当たるかは静的には決めきれない）</span>');
      else target = '<span class="muted">静的には決めきれない</span>';
      h += `<tr class="${t.zero ? 'zero' : ''}"><td><code>${esc(t.name)}</code></td><td>${(t.exprs || []).map((e) => `<code>${esc(e)}</code>`).join(' ')}</td><td>${target}</td></tr>`;
    }
    h += '</tbody></table>';
  } else {
    h += '<p class="muted">閾値（thresholds）がありません．閾値がないと，k6 は何が起きても終了コード 0 で終わります．</p>';
  }
  const checks = a.checks || [];
  if (checks.length) {
    h += '<p class="stats">check：' + checks.map((c) => (c.names || []).map((n) => `<span class="tag">${esc(n)}</span>`).join('') + `<span class="muted small">（${c.line} 行目）</span>`).join('　') + '</p>';
  }
  h += '</div>';
  $('result').innerHTML = h;
  $('result').hidden = false;
  $('result').scrollIntoView({ behavior: 'smooth', block: 'start' });
}

// ---- k6 で実行（拡張機能） ----

async function runK6() {
  if (!state.current || $('run').disabled) return;
  const id = state.current.exercise.id;
  const btn = $('run');
  btn.disabled = true;
  $('running').hidden = false;
  const start = Date.now();
  $('elapsed').textContent = '0';
  state.runTimer = setInterval(() => { $('elapsed').textContent = Math.floor((Date.now() - start) / 1000); }, 250);
  try {
    const d = await api('POST', `/api/exercises/${id}/run`, { code: $('code-input').value });
    if (state.current.exercise.id !== id) return;
    renderRun(d.run);
  } catch (err) {
    $('run-result').hidden = false;
    $('run-result').innerHTML = `<h3 class="title">k6 での実行結果（拡張）</h3><p>${esc(err.message)}</p>`;
  } finally {
    clearInterval(state.runTimer);
    btn.disabled = false;
    $('running').hidden = true;
  }
}

function renderRun(v) {
  const r = v.result || {};
  let h = '<h3 class="title">k6 での実行結果（拡張．合否には数えません）</h3>';
  h += `<div class="verdict ${v.passed ? 'pass' : 'fail'}">${v.passed ? '実機でも期待どおり' : '実機では期待どおりでない'}</div>`;
  if (v.failures.length) {
    h += '<ul class="failures">' + v.failures.map((f) => `<li>${inline(f.message)}</li>`).join('') + '</ul>';
  }
  if (v.hints && v.hints.length) {
    h += '<ul class="hints">' + v.hints.map((t) => `<li>${inline(t)}</li>`).join('') + '</ul>';
  }
  const th = r.thresholds || [];
  if (th.length) {
    h += '<table class="th"><thead><tr><th>閾値</th><th>式</th><th>実測値</th><th>測定件数</th><th>合否</th></tr></thead><tbody>';
    for (const t of th) {
      const zero = t.count === 0;
      h += `<tr class="${zero ? 'zero' : ''}">
        <td><code>${esc(t.name)}</code></td><td><code>${esc(t.expr)}</code></td><td>${esc(t.actual || '')}</td>
        <td class="num">${t.count}${zero ? '<span class="zero-note">0件：この閾値は何も測っていない</span>' : ''}</td>
        <td><span class="badge ${t.ok ? 'ok' : 'ng'}">${t.ok ? '合格' : '不合格'}</span></td></tr>`;
    }
    h += '</tbody></table>';
    if (v.zero_sample) h += '<p class="zero-note">測定件数0の閾値は，速いから合格したのではなく，1件も測らずに合格しています．</p>';
  }
  h += `<div class="exit ${v.exit_code === 0 ? 'zero' : ''}" style="margin-top:10px"><span class="code">${v.exit_code}</span>
    <span>${esc(v.exit_meaning)}</span><span class="note">${esc(v.exit_note)}</span></div>`;
  if (r.phase === 'run') {
    const rate = r.checks_total ? ((r.checks_passed / r.checks_total) * 100).toFixed(1) + '%' : '—';
    h += `<p class="stats">送ったリクエスト（http_reqs）：${r.http_reqs} 件　check：${r.checks_passed} / ${r.checks_total}（${rate}）　実行時間：${(r.seconds || 0).toFixed(1)} 秒</p>`;
  }
  const out = [r.stdout, r.stderr].filter(Boolean).join('\n--- 標準エラー ---\n');
  h += `<details class="output"><summary>k6 の出力全文</summary><pre>${esc(out || '（出力なし）')}</pre></details>`;
  $('run-result').innerHTML = h;
  $('run-result').hidden = false;
  $('run-result').scrollIntoView({ behavior: 'smooth', block: 'start' });
}

// ---- レシピ集 ----

async function openRecipes() {
  const d = await api('GET', '/api/recipes');
  let h = '';
  for (const r of d.recipes) {
    h += `<div class="recipe"><h3>${esc(r.scenario)}</h3><p class="muted small"><a href="#${esc(r.id)}">${esc(r.id)} ${esc(r.title)}</a></p>`;
    h += r.open ? `<pre class="code">${esc(r.code)}</pre>` : '<p class="locked">まだ解いていません．</p>';
    h += '</div>';
  }
  $('recipe-list').innerHTML = h;
  $('welcome').hidden = true;
  $('exercise').hidden = true;
  $('recipes').hidden = false;
  state.current = null;
  if (location.hash) history.replaceState(null, '', location.pathname);
  await loadList();
  window.scrollTo(0, 0);
}

// ---- 設定 ----

async function openSettings() {
  const d = await api('GET', '/api/settings');
  $('record').checked = d.settings.record;
  $('data-dir').textContent = d.dir;
  $('reset-done').hidden = true;
  $('settings').showModal();
}

async function resetAll() {
  if (!confirm('合否と書きかけのコードをすべて消します．よろしいですか？')) return;
  await api('POST', '/api/progress/reset');
  $('reset-done').hidden = false;
  state.current = null;
  $('exercise').hidden = true;
  $('welcome').hidden = false;
  location.hash = '';
  await loadList();
}

// ---- 起動 ----

function route() {
  const id = location.hash.slice(1);
  if (id) openExercise(id);
}

window.addEventListener('DOMContentLoaded', async () => {
  $('code-input').addEventListener('input', onInput);
  $('code-input').addEventListener('keydown', onKeyDown);
  $('code-input').addEventListener('scroll', () => { $('gutter').scrollTop = $('code-input').scrollTop; });
  $('check').addEventListener('click', checkCode);
  $('run').addEventListener('click', runK6);
  $('reveal').addEventListener('click', revealSolution);
  $('copy-solution').addEventListener('click', () => {
    if (!state.current || !state.current.solution) return;
    setCode(state.current.solution);
    scheduleSave();
  });
  $('open-recipes').addEventListener('click', openRecipes);
  $('reset-code').addEventListener('click', () => {
    if (!state.current) return;
    setCode(state.current.exercise.starter);
    scheduleSave();
  });
  $('quiz-check').addEventListener('click', checkQuiz);
  $('open-settings').addEventListener('click', openSettings);
  $('record').addEventListener('change', (ev) => api('PUT', '/api/settings', { record: ev.target.checked }));
  $('reset-all').addEventListener('click', resetAll);
  window.addEventListener('hashchange', route);

  try {
    await checkStatus();
  } catch (err) {
    alertBanner('起動時の確認に失敗しました：' + err.message);
  }
  await loadList();
  route();
});
