'use strict';

/* =========================================================
 * 课下作业平台 · 交互原型（题目 03）
 * 纯前端内存状态机：用于需求沟通与验收演示，不是生产实现
 * ========================================================= */

const USERS = [
  { id: 'T01', name: '周老师', role: 'teacher', cls: 'P01' },
  { id: 'A01', name: '李助教', role: 'assistant', cls: 'P01' },
  { id: 'S01', name: '林小雨', role: 'student', cls: 'P01' },
  { id: 'S02', name: '王浩然', role: 'student', cls: 'P01' },
  { id: 'S03', name: '陈子墨', role: 'student', cls: 'P01' },
  { id: 'S04', name: '赵嘉宁', role: 'student', cls: 'P01' },
  { id: 'S05', name: '吴一诺', role: 'student', cls: 'P01' },
  { id: 'X01', name: '外班同学', role: 'student', cls: 'P02' }
];
const STUDENTS = USERS.filter(u => u.role === 'student' && u.cls === 'P01').map(u => u.id);

let SESSION = null;
let VIEW = 'assign';
let DB = null;

/* ---------------- 工具 ---------------- */
const el = id => document.getElementById(id);

function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, c => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ));
}
function user(id) { return USERS.find(u => u.id === id); }
function now() { return Date.now() + DB.clock; }
function fmt(ts) {
  if (!ts) return '—';
  const d = new Date(ts), p = n => String(n).padStart(2, '0');
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}
function normalize(t) {
  return String(t || '').replace(/\r\n/g, '\n').split('\n').map(l => l.replace(/[ \t]+$/, '')).join('\n').trim();
}
function hash(t) {
  let h = 5381;
  for (let i = 0; i < t.length; i++) h = ((h << 5) + h) ^ t.charCodeAt(i);
  return (h >>> 0).toString(16);
}
function uid() { return 'rq-' + Math.random().toString(36).slice(2, 10); }

/* ---------------- 状态 ---------------- */
function freshDB() {
  const db = {
    clock: 0,
    seq: 1,
    assignment: {
      id: 'HW03',
      title: '第 3 课 · 函数与递归 课后作业',
      requirement: '完成教材 P42 第 3、5 题：写出递归函数，说明边界条件与终止条件。只收纯文本，步骤要完整。',
      deadlineMin: 30,
      state: 'DRAFT',
      deadlineTs: null,
      snapshot: null
    },
    enroll: {},
    events: [],
    ledger: [],
    reminders: []
  };
  STUDENTS.forEach(id => {
    db.enroll[id] = { state: 'NOT_SUBMITTED', ver: 0, versions: [], ext: null, points: 0, last: null };
  });
  return db;
}

function resetAll() {
  DB = freshDB();
  SESSION = null;
  VIEW = 'assign';
  render();
}

/* ---------------- 业务规则 ---------------- */
function effDeadline(stu) {
  const e = DB.enroll[stu];
  if (e && e.ext) return e.ext.ts;
  return DB.assignment.deadlineTs || Infinity;
}

function stateOf(stu) {
  const e = DB.enroll[stu];
  const dl = effDeadline(stu);
  if (e.state === 'PASSED') return { code: 'PASSED', text: '已通过', cls: 'ok' };
  if (e.state === 'SUBMITTED') return { code: 'SUBMITTED', text: '待批阅', cls: 'warn' };
  if (e.state === 'RETURNED') {
    return now() > dl
      ? { code: 'RETURNED', text: '退回·已逾期', cls: 'dark' }
      : { code: 'RETURNED', text: '已退回', cls: 'danger' };
  }
  return (DB.assignment.deadlineTs && now() > dl)
    ? { code: 'OVERDUE', text: '逾期未交', cls: 'dark' }
    : { code: 'NOT_SUBMITTED', text: '未提交', cls: 'muted' };
}

function curVer(stu) {
  const e = DB.enroll[stu];
  return e.versions.length ? e.versions[e.versions.length - 1] : null;
}

function logEvent(type, text, stu) {
  const u = SESSION ? user(SESSION) : { id: 'sys', role: '-' };
  DB.events.push({ id: DB.seq++, t: now(), type, text, stu: stu || null, by: u.id, role: u.role });
}

const ok = msg => ({ ok: true, msg });
const fail = (code, msg) => ({ ok: false, code, msg: `409 ${code}：${msg}` });

function doPublish(title, requirement, minutes) {
  const a = DB.assignment;
  if (a.state === 'PUBLISHED') return fail('assignment_locked', '已发布内容不可静默变更');
  const min = Number(minutes);
  if (!String(title).trim() || !String(requirement).trim()) return fail('invalid_input', '标题与提交要求必填');
  if (!min || min < 1) return fail('invalid_input', '截止时间必须大于 0 分钟');
  a.title = String(title).trim();
  a.requirement = String(requirement).trim();
  a.deadlineMin = min;
  a.state = 'PUBLISHED';
  a.deadlineTs = now() + min * 60 * 1000;
  a.snapshot = { title: a.title, requirement: a.requirement, deadlineTs: a.deadlineTs, by: SESSION, at: now() };
  logEvent('publish', `发布作业《${a.title}》，截止 ${fmt(a.deadlineTs)}`);
  return ok('发布成功：已生成发布快照，P01 五名学生可见，外班不可见');
}

function doSubmit(stu, content, mode) {
  const e = DB.enroll[stu];
  if (!DB.assignment.deadlineTs) return fail('not_published', '作业尚未发布');
  const raw = String(content || '').trim();
  const norm = normalize(raw);
  const h = hash(norm);

  if (mode === 'replay' || mode === 'conflict') {
    if (!e.last) return fail('no_request', '没有可重放的请求编号，请先正常提交一次');
    if (mode === 'replay') {
      if (e.last.hash !== h) return fail('idempotency_conflict', `请求编号 ${e.last.reqId} 的内容与首次不一致，未写入`);
      return ok(`幂等重放：请求编号 ${e.last.reqId} 命中首次结果，返回 v${e.last.ver}，未新增版本`);
    }
    if (e.last.hash === h) return fail('idempotency_conflict', '内容与首次完全一致，属于纯重放（不会冲突），请改内容后再试');
    return fail('idempotency_conflict', '相同请求编号不得携带不同内容，未写入');
  }

  if (e.state === 'PASSED') return fail('already_passed', '作业已通过，不再允许修改');
  if (!norm.length) return fail('invalid_input', '内容不能为空');
  if (norm.length > 5000) return fail('too_long', '内容超过 5000 字符上限');
  if (now() > effDeadline(stu)) {
    return e.ext
      ? fail('deadline_exceeded', `已超过补交截止时间 ${fmt(e.ext.ts)}`)
      : fail('deadline_exceeded', '已超过截止时间，且未获补交批准');
  }
  if (mode === 'stale' && e.ver < 1) return fail('no_base', '尚无历史版本，无法模拟旧请求');

  const base = mode === 'stale' ? Math.max(0, e.ver - 1) : e.ver;
  if (base !== e.ver) {
    return fail('version_conflict', `请求基于 v${base}，但当前已是 v${e.ver}，旧请求不得覆盖新版本`);
  }

  const ver = e.ver + 1;
  const reqId = uid();
  e.versions.push({ no: ver, content: raw, hash: h, reqId, at: now(), review: null });
  e.ver = ver;
  e.state = 'SUBMITTED';
  e.last = { ver, reqId, hash: h };
  logEvent('submit', `提交 v${ver}（请求 ${reqId}）`, stu);
  return ok(`提交成功：v${ver} 进入待批阅（请求编号 ${reqId}）`);
}

function doReview(stu, versionNo, decision, feedback) {
  const e = DB.enroll[stu];
  const v = e.versions.find(x => x.no === Number(versionNo));
  if (!v) return fail('not_found', '指定版本不存在');

  if (e.state === 'PASSED') {
    if (v.review && v.review.decision === 'pass') return ok('该版本已通过，幂等返回，未重复发放积分');
    return fail('review_conflict', '作业已通过，不能再改结论（本题不实现撤销）');
  }
  if (v.no !== e.ver) return fail('stale_version', `v${v.no} 已被 v${e.ver} 替代，不能作为当前作业批阅`);
  if (v.review) {
    if (v.review.decision === decision) return ok('同一版本同一结论，幂等返回，未产生新记录');
    return fail('review_conflict', '该版本已有相反结论，不能重复批阅');
  }
  if (decision === 'reject' && !String(feedback || '').trim()) {
    return fail('feedback_required', '退回必须填写文字反馈');
  }

  v.review = { decision, feedback: String(feedback || '').trim(), at: now(), by: SESSION };

  if (decision === 'pass') {
    e.state = 'PASSED';
    const exists = DB.ledger.some(l => l.stu === stu && l.assignment === DB.assignment.id);
    if (!exists) {
      DB.ledger.push({ stu, assignment: DB.assignment.id, points: 10, reason: 'first_pass', at: now() });
      e.points += 10;
    }
    logEvent('review', `通过 v${v.no}${exists ? '（已发过奖，未重复发放）' : '，发放 10 积分'}`, stu);
    return ok(`已通过 v${v.no}${exists ? '（未重复发奖）' : '，发放 10 积分'}`);
  }

  e.state = 'RETURNED';
  logEvent('review', `退回 v${v.no}：${v.review.feedback}`, stu);
  return ok(`已退回 v${v.no}，学生可在有效截止时间内重交`);
}

function doGrant(stu, reason) {
  const e = DB.enroll[stu];
  if (!DB.assignment.deadlineTs) return fail('not_published', '作业尚未发布');
  if (e.state === 'PASSED') return fail('already_passed', '该生已通过，无需补交');
  if (e.ext) return fail('extension_exists', '该生已获批一次补交机会，不能重复设置');
  const r = String(reason || '').trim();
  if (r.length < 5) return fail('reason_required', '补交原因必填，且不少于 5 字');
  const base = Math.max(DB.assignment.deadlineTs, now());
  e.ext = { ts: base + 3600e3, reason: r, by: SESSION, at: now() };
  logEvent('extension', `批准补交至 ${fmt(e.ext.ts)}，原因：${r}`, stu);
  return ok(`已为${user(stu).name}设置补交截止 ${fmt(e.ext.ts)}（仅该生生效，一次性）`);
}

function doRemind(stu, method, note) {
  const n = String(note || '').trim();
  if (n.length < 2) return fail('note_required', '请填写沟通结果摘要（至少 2 字）');
  DB.reminders.push({ stu, by: SESSION, method, note: n, at: now() });
  logEvent('remind', `催交（${method}）：${n}`, stu);
  return ok('催交记录已保存（仅记录，不改变作业状态）');
}

function simulateRace(stu) {
  const e = DB.enroll[stu];
  if (e.state !== 'SUBMITTED') return fail('invalid_state', '该生当前不是待批阅状态，无法模拟抢先重交');
  const v = curVer(stu);
  const ver = e.ver + 1;
  const content = (v ? v.content : '') + '\n\n（补充：边界条件应为 n<=1，函数在 n=0 时返回 1。）';
  const reqId = uid();
  e.versions.push({ no: ver, content, hash: 'race' + ver, reqId, at: now(), review: null });
  e.ver = ver;
  e.state = 'SUBMITTED';
  e.last = { ver, reqId, hash: 'race' + ver };
  logEvent('submit', `学生抢先提交 v${ver}（教师正在批 v${ver - 1}）`, stu);
  return ok(`已模拟：学生在你批阅前提交了 v${ver}，此时按 v${ver - 1} 批阅会被拒绝`);
}

/* ---------------- 渲染 ---------------- */
function render() {
  if (!SESSION) return renderLogin();
  const u = user(SESSION);
  el('login').classList.add('hidden');
  el('shell').classList.remove('hidden');
  el('whoName').innerHTML = `${esc(u.name)} <small>${u.id} · ${roleName(u)}</small>`;
  el('userSwitch').innerHTML = USERS
    .map(x => `<option value="${x.id}" ${x.id === u.id ? 'selected' : ''}>${esc(x.name)}（${x.id}）</option>`)
    .join('');
  renderNav(u);
  el('main').innerHTML = renderMain(u);
  updateClock();
}

function roleName(u) { return u.role === 'teacher' ? '教师' : u.role === 'assistant' ? '助教' : '学生'; }

function renderLogin() {
  el('shell').classList.add('hidden');
  const login = el('login');
  login.classList.remove('hidden');
  login.innerHTML = `
    <div class="login-card">
      <div class="login-head">
        <span class="logo">03</span>
        <div>
          <h1>课下作业平台</h1>
          <p>交互原型 · 布置 / 提交 / 批阅 / 补交 / 积分 / 汇总</p>
        </div>
      </div>
      <p class="hint" style="margin-top:18px">预置账号（教学演示，口令省略）。选择身份进入：</p>
      <div class="acct-grid">
        ${USERS.map(u => `
          <button class="acct" data-act="login" data-user="${u.id}">
            <span class="acct-role ${u.role}">${roleName(u)}</span>
            <strong>${esc(u.name)}</strong>
            <small>${u.id} · ${u.cls === 'P02' ? '外班 P02（不可见本班作业）' : 'P01'}</small>
          </button>`).join('')}
      </div>
      <p class="hint small">数据仅保存在当前页面内存，刷新即重置。教学时钟可在右上角快进，用于演示逾期与补交。</p>
    </div>`;
}

const TABS = {
  teacher: [['assign', '作业设置'], ['review', '批阅台'], ['summary', '班级汇总']],
  assistant: [['board', '班级看板']],
  student: [['home', '我的作业']]
};

function renderNav(u) {
  const tabs = TABS[u.role] || [];
  if (!tabs.some(t => t[0] === VIEW)) VIEW = tabs.length ? tabs[0][0] : 'home';
  const title = u.role === 'teacher' ? '教师工作台' : u.role === 'assistant' ? '助教工作台' : '学生空间';
  el('nav').innerHTML = `<div class="nav-title">${title}</div>`
    + tabs.map(([k, label]) => `<button class="${VIEW === k ? 'active' : ''}" data-act="view" data-view="${k}">${label}</button>`).join('');
}

function renderMain(u) {
  if (u.cls === 'P02') return viewNoAccess(u);
  if (u.role === 'teacher') {
    if (VIEW === 'review') return viewReview();
    if (VIEW === 'summary') return viewSummary();
    return viewAssign();
  }
  if (u.role === 'assistant') return viewBoard();
  return viewStudent(u);
}

function updateClock() {
  const off = DB ? DB.clock : 0;
  const txt = fmt(now()) + (off ? `（偏移 +${Math.round(off / 3600e3)}h）` : '');
  const node = el('clockNow');
  if (node) node.textContent = txt;
}

/* ---------- 教师：作业设置 ---------- */
function viewAssign() {
  const a = DB.assignment;
  const published = a.state === 'PUBLISHED';
  return `
  <div class="page-head">
    <div>
      <h2>作业设置</h2>
      <p class="sub">发布即生成不可变快照；发布后任何内容修改请求都会被拒绝。</p>
    </div>
    <span class="badge ${published ? 'ok' : 'muted'}">${published ? '已发布' : '草稿'}</span>
  </div>
  <div class="grid two">
    <section class="card">
      <h3>作业内容</h3>
      <label>标题<input id="f-title" value="${esc(a.title)}" ${published ? 'disabled' : ''}></label>
      <label>提交要求<textarea id="f-req" rows="5" ${published ? 'disabled' : ''}>${esc(a.requirement)}</textarea></label>
      <label>截止时间（发布后 N 分钟，便于演示）<input id="f-min" type="number" min="1" max="1440" value="${a.deadlineMin}" ${published ? 'disabled' : ''}></label>
      <div class="row">
        <button class="btn primary" data-act="publish" ${published ? 'disabled' : ''}>发布作业</button>
        <button class="btn ghost" data-act="editLocked">尝试修改已发布要求</button>
        <button class="btn ghost" data-act="clock1h">教学时钟 +1 小时</button>
      </div>
      <p class="hint small">演示逾期：发布时把截止设为 1 分钟，点「+1 小时」即进入逾期。</p>
    </section>
    <section class="card">
      <h3>发布快照（学生看到的就是它）</h3>
      ${published ? `
        <dl class="kv">
          <dt>标题</dt><dd>${esc(a.snapshot.title)}</dd>
          <dt>要求</dt><dd class="pre">${esc(a.snapshot.requirement)}</dd>
          <dt>截止时间</dt><dd>${fmt(a.snapshot.deadlineTs)}</dd>
          <dt>发布人</dt><dd>${a.snapshot.by} · ${fmt(a.snapshot.at)}</dd>
          <dt>可见范围</dt><dd>P01（5 名学生）；外班 P02 返回 404</dd>
        </dl>`
        : `<p class="empty">尚未发布。发布后此处展示不可变快照。</p>`}
      <div class="note">教师可在批阅台为未通过学生设置<b>一次</b>补交；补交只影响该生。</div>
    </section>
  </div>`;
}

/* ---------- 教师：批阅台 ---------- */
function viewReview() {
  if (!DB.assignment.deadlineTs) {
    return `<div class="page-head"><div><h2>批阅台</h2><p class="sub">作业尚未发布。</p></div></div>
      <div class="card"><p class="empty">请先在「作业设置」发布作业。</p></div>`;
  }
  const cards = STUDENTS.map(id => {
    const u = user(id), e = DB.enroll[id], meta = stateOf(id), v = curVer(id);
    const opts = e.versions.map(x => `<option value="${x.no}">v${x.no}${x.no === e.ver ? '（当前）' : '（历史）'}</option>`).join('');
    const fb = v && v.review ? (v.review.feedback || '') : '';
    return `
    <section class="card">
      <div class="card-head">
        <div><strong>${u.name}</strong> <small>${id}</small></div>
        <span class="badge ${meta.cls}">${meta.text}</span>
      </div>
      <p class="meta">当前版本 ${e.ver ? 'v' + e.ver : '—'} · 有效截止 ${fmt(effDeadline(id))}${e.ext ? ' · <span class="tag-ext">补交已批</span>' : ''}</p>
      ${v ? `<pre class="content">${esc(v.content)}</pre>
      <label class="inline">批阅版本<select id="ver-${id}">${opts}</select></label>
      <textarea id="fb-${id}" rows="2" placeholder="反馈（退回必填，≤500 字）">${esc(fb)}</textarea>
      <div class="row">
        <button class="btn ok" data-act="pass" data-stu="${id}">按该版本通过</button>
        <button class="btn danger" data-act="reject" data-stu="${id}">退回</button>
        <button class="btn ghost" data-act="race" data-stu="${id}">模拟学生抢先重交</button>
        <button class="btn ghost" data-act="double" data-stu="${id}">重复通过两次</button>
      </div>`
        : `<p class="empty">尚无提交，无法批阅。</p>`}
      <div class="row ext-row">
        <input id="ext-${id}" placeholder="补交原因（≥5 字，每生仅一次）">
        <button class="btn warn" data-act="grant" data-stu="${id}">批准补交 1 小时</button>
      </div>
    </section>`;
  }).join('');
  return `
  <div class="page-head">
    <div><h2>批阅台</h2><p class="sub">批阅必须绑定具体版本；被替代的版本不能通过。</p></div>
    <div class="note small">演示：先「模拟学生抢先重交」，再按旧版本通过 → 409 stale_version。</div>
  </div>
  <div class="grid three">${cards}</div>`;
}

/* ---------- 教师：班级汇总 ---------- */
function viewSummary() {
  if (!DB.assignment.deadlineTs) {
    return `<div class="page-head"><div><h2>班级汇总</h2><p class="sub">作业尚未发布。</p></div></div>
      <div class="card"><p class="empty">发布并产生数据后再查看。</p></div>`;
  }
  let passed = 0, submitted = 0, returned = 0, none = 0;
  STUDENTS.forEach(id => {
    const c = stateOf(id).code;
    if (c === 'PASSED') passed++;
    else if (c === 'SUBMITTED') submitted++;
    else if (c === 'RETURNED') returned++;
    else none++;
  });

  const rows = STUDENTS.map(id => {
    const e = DB.enroll[id], meta = stateOf(id), first = e.versions[0];
    const mine = DB.ledger.filter(l => l.stu === id);
    return `<tr>
      <td>${user(id).name}<br><small>${id}</small></td>
      <td>${fmt(effDeadline(id))}</td>
      <td>${e.ver ? 'v' + e.ver : '—'}</td>
      <td><span class="badge ${meta.cls}">${meta.text}</span></td>
      <td>${first ? fmt(first.at) : '—'}</td>
      <td>${e.ext ? `<span class="tag-ext">补交至 ${fmt(e.ext.ts)}</span><br><small>${esc(e.ext.reason)}</small>` : '—'}</td>
      <td>${mine.length ? mine.map(l => '+' + l.points).join(' ') : '—'}</td>
    </tr>`;
  }).join('');

  const ledgerRows = DB.ledger.length
    ? DB.ledger.map(l => `<tr><td>${user(l.stu).name}</td><td>${l.reason}</td><td>+${l.points}</td><td>${fmt(l.at)}</td></tr>`).join('')
    : '<tr><td colspan="4" class="empty">暂无奖励流水</td></tr>';

  const events = DB.events.slice().reverse().slice(0, 40);
  const timeline = events.length
    ? events.map(ev => `<li><time>${fmt(ev.t)}</time><div><span class="who-tag">${ev.by}</span> ${esc(ev.text)}</div></li>`).join('')
    : '<li><time>—</time><div class="empty">暂无事件</div></li>';

  return `
  <div class="page-head">
    <div><h2>班级汇总</h2><p class="sub">数据时间 ${fmt(now())}；只读查询，可追溯到首次提交。</p></div>
    <span class="badge ${passed === STUDENTS.length ? 'ok' : 'warn'}">达标 ${passed}/${STUDENTS.length}</span>
  </div>
  <div class="grid four">
    <div class="card stat"><b>${none}</b><span>未交 / 逾期</span></div>
    <div class="card stat"><b>${submitted}</b><span>待批阅</span></div>
    <div class="card stat"><b>${returned}</b><span>已退回</span></div>
    <div class="card stat"><b>${passed}</b><span>已通过（达标）</span></div>
  </div>
  <section class="card">
    <h3>各学生最终状态</h3>
    <table>
      <thead><tr><th>学生</th><th>有效截止</th><th>版本</th><th>最终状态</th><th>首次提交</th><th>补交</th><th>积分</th></tr></thead>
      <tbody>${rows}</tbody>
    </table>
  </section>
  <div class="grid two">
    <section class="card">
      <h3>奖励流水（首次通过 +10）</h3>
      <table><thead><tr><th>学生</th><th>原因</th><th>分值</th><th>时间</th></tr></thead><tbody>${ledgerRows}</tbody></table>
      <p class="hint small">重复批阅 / 重试不会新增流水（唯一约束 student+assignment+reason）。</p>
    </section>
    <section class="card">
      <h3>过程时间线</h3>
      <ul class="timeline">${timeline}</ul>
    </section>
  </div>`;
}

/* ---------- 助教：班级看板 ---------- */
function viewBoard() {
  if (!DB.assignment.deadlineTs) {
    return `<div class="page-head"><div><h2>班级看板</h2><p class="sub">作业尚未发布。</p></div></div>
      <div class="card"><p class="empty">教师发布后才可查看完成情况。</p></div>`;
  }
  const buckets = [
    { key: ['NOT_SUBMITTED', 'OVERDUE'], label: '未交', cls: 'muted' },
    { key: ['SUBMITTED'], label: '待批阅', cls: 'warn' },
    { key: ['RETURNED'], label: '已退回', cls: 'danger' },
    { key: ['PASSED'], label: '已通过', cls: 'ok' }
  ];
  const cols = buckets.map(b => {
    const list = STUDENTS.filter(id => b.key.indexOf(stateOf(id).code) >= 0);
    return `<section class="card">
      <div class="card-head"><h3>${b.label}</h3><span class="badge ${b.cls}">${list.length}</span></div>
      ${list.length
        ? list.map(id => `<span class="chip">${user(id).name}${stateOf(id).code === 'OVERDUE' ? '（逾期）' : ''}</span>`).join('')
        : '<p class="empty">无</p>'}
    </section>`;
  }).join('');

  const rows = STUDENTS.map(id => {
    const mine = DB.reminders.filter(r => r.stu === id);
    const last = mine[mine.length - 1];
    return `<tr>
      <td>${user(id).name}</td>
      <td><span class="badge ${stateOf(id).cls}">${stateOf(id).text}</span></td>
      <td>${last ? `${fmt(last.at)}<br><small>${esc(last.method)}：${esc(last.note)}</small>` : '—'}</td>
      <td>
        <div class="row">
          <select id="m-${id}" style="width:88px"><option>站内</option><option>电话</option><option>群</option></select>
          <input id="n-${id}" placeholder="沟通结果摘要" style="min-width:120px">
          <button class="btn sm" data-act="remind" data-stu="${id}">记录催交</button>
        </div>
      </td>
    </tr>`;
  }).join('');

  return `
  <div class="page-head">
    <div><h2>班级看板</h2><p class="sub">助教只能查看状态并记录催交，不能批阅、不能设补交。</p></div>
    <button class="btn danger" data-act="assistantTryReview">尝试批阅（越权演示）</button>
  </div>
  <div class="grid four">${cols}</div>
  <section class="card">
    <h3>催交记录</h3>
    <table><thead><tr><th>学生</th><th>状态</th><th>最近催交</th><th>新增记录</th></tr></thead><tbody>${rows}</tbody></table>
  </section>`;
}

/* ---------- 学生：我的作业 ---------- */
function viewStudent(u) {
  if (!DB.assignment.deadlineTs) {
    return `<div class="page-head"><div><h2>我的作业</h2><p class="sub">教师尚未发布作业。</p></div></div>
      <div class="card"><p class="empty">暂无可查看的作业。</p></div>`;
  }
  const e = DB.enroll[u.id], meta = stateOf(u.id), v = curVer(u.id);
  const ledger = DB.ledger.filter(l => l.stu === u.id);
  const history = e.versions.slice().reverse().map(x => `
    <div class="ver-item ${x.no === e.ver ? 'current' : ''}">
      <div class="ver-head">
        <b>v${x.no}</b>
        <small>${fmt(x.at)} · ${x.reqId}</small>
        ${x.no === e.ver ? '<span class="badge muted">当前</span>' : ''}
        ${x.review
          ? `<span class="badge ${x.review.decision === 'pass' ? 'ok' : 'danger'}">${x.review.decision === 'pass' ? '通过' : '退回'}</span>`
          : '<span class="badge warn">待批阅</span>'}
      </div>
      <pre class="content">${esc(x.content)}</pre>
      ${x.review ? `<p class="meta">教师反馈：${esc(x.review.feedback || '（无）')} · ${fmt(x.review.at)}</p>` : ''}
    </div>`).join('');

  const locked = meta.code === 'PASSED';
  return `
  <div class="page-head">
    <div><h2>我的作业</h2><p class="sub">只有你和本班教师能看到你的内容；助教不可见正文。</p></div>
    <span class="badge ${meta.cls}">${meta.text}</span>
  </div>
  <div class="grid two">
    <section class="card">
      <h3>${esc(DB.assignment.snapshot.title)}</h3>
      <p class="pre" style="margin:0">${esc(DB.assignment.snapshot.requirement)}</p>
      <p class="meta">有效截止：<b>${fmt(effDeadline(u.id))}</b>${e.ext ? ` · 已批补交（原因：${esc(e.ext.reason)}）` : ''}</p>
      ${locked
        ? '<div class="note ok">已通过，作业锁定，不能再修改。本题不实现撤销。</div>'
        : now() > effDeadline(u.id)
          ? '<div class="note danger">已超过有效截止时间，无法再提交。如需补交请联系教师批准。</div>'
          : '<div class="note">提交纯文本，长度 1~5000 字符；每次修改都会保留版本。</div>'}
      <label>作业内容<textarea id="content" rows="7" ${locked ? 'disabled' : ''} placeholder="在这里写下你的解答...">${v ? esc(v.content) : ''}</textarea></label>
      <div class="row">
        <button class="btn primary" data-act="submit" data-mode="normal" ${locked ? 'disabled' : ''}>${e.ver ? '提交新版本' : '提交作业'}</button>
        <button class="btn ghost" data-act="submit" data-mode="replay" ${locked ? 'disabled' : ''}>重放上一次请求</button>
        <button class="btn ghost" data-act="submit" data-mode="conflict" ${locked ? 'disabled' : ''}>同请求号改内容</button>
        <button class="btn ghost" data-act="submit" data-mode="stale" ${locked ? 'disabled' : ''}>模拟旧请求</button>
      </div>
      <p class="hint small">「重放」验证幂等；「同请求号改内容」验证 409；「模拟旧请求」验证 base_version 冲突。</p>
    </section>
    <section class="card">
      <h3>我的积分</h3>
      <p style="margin:0;font-size:26px;font-weight:700">${e.points} <small style="font-size:12px;color:#6b7280">分</small></p>
      ${ledger.length
        ? ledger.map(l => `<p class="meta">${fmt(l.at)} · 首次通过 +${l.points}（${l.reason}）</p>`).join('')
        : '<p class="empty">尚未获得积分（通过后发放 10 分）。</p>'}
      <p class="hint small">重复批阅或重试不会重复发奖。</p>
    </section>
  </div>
  <section class="card">
    <h3>版本记录（旧版本与意见仍可查）</h3>
    ${e.versions.length ? history : '<p class="empty">还没有提交记录。</p>'}
  </section>`;
}

/* ---------- 外班学生 ---------- */
function viewNoAccess(u) {
  return `
  <div class="card" style="max-width:520px">
    <span class="badge dark">404</span>
    <h2 style="margin:0">未找到该作业</h2>
    <p class="hint">${esc(u.name)}（${u.id}）属于 P02 班，作业只对本班学生可见。即使直接输入地址，也不会暴露作业是否存在。</p>
    <div class="row"><button class="btn" data-act="reset">回到登录页</button></div>
  </div>`;
}

/* ---------------- 交互 ---------------- */
function toast(msg, kind) {
  const box = el('toasts');
  if (!box) return;
  const t = document.createElement('div');
  t.className = 'toast ' + (kind || 'info');
  t.textContent = msg;
  box.appendChild(t);
  setTimeout(() => { t.remove(); }, 7000);
}

document.addEventListener('click', ev => {
  const btn = ev.target.closest('[data-act]');
  if (!btn) return;
  const act = btn.dataset.act;
  const stu = btn.dataset.stu;
  const mode = btn.dataset.mode;

  if (act === 'login') {
    SESSION = btn.dataset.user;
    VIEW = (TABS[user(SESSION).role] || [['home', '']])[0][0];
    render();
    return;
  }
  if (act === 'reset') { resetAll(); toast('演示已重置', 'info'); return; }
  if (act === 'view') { VIEW = btn.dataset.view; render(); return; }

  if (act === 'clock1h') { DB.clock += 3600e3; render(); toast('教学时钟 +1 小时（当前 ' + fmt(now()) + '）', 'info'); return; }
  if (act === 'clock1d') { DB.clock += 86400e3; render(); toast('教学时钟 +1 天（当前 ' + fmt(now()) + '）', 'info'); return; }
  if (act === 'clockReset') { DB.clock = 0; render(); toast('教学时钟已重置', 'info'); return; }

  if (act === 'publish') {
    const r = doPublish(el('f-title').value, el('f-req').value, el('f-min').value);
    toast(r.msg, r.ok ? 'ok' : 'err');
    render();
    return;
  }

  if (act === 'editLocked') {
    if (DB.assignment.state !== 'PUBLISHED') { toast('当前仍是草稿，可直接编辑。', 'info'); return; }
    toast('409 assignment_locked：已发布内容不可静默变更，快照已保留；如需变更请新建作业', 'err');
    return;
  }

  if (act === 'submit') {
    const r = doSubmit(SESSION, el('content').value, mode);
    toast(r.msg, r.ok ? 'ok' : 'err');
    render();
    return;
  }

  if (act === 'pass' || act === 'reject') {
    const r = doReview(stu, el('ver-' + stu).value, act === 'pass' ? 'pass' : 'reject', el('fb-' + stu).value);
    toast(r.msg, r.ok ? 'ok' : 'err');
    render();
    return;
  }

  if (act === 'double') {
    const ver = el('ver-' + stu).value, fb = el('fb-' + stu).value;
    const r1 = doReview(stu, ver, 'pass', fb);
    const r2 = doReview(stu, ver, 'pass', fb);
    toast('第 1 次：' + r1.msg + '\n第 2 次：' + r2.msg, r2.ok ? 'ok' : 'err');
    render();
    return;
  }

  if (act === 'race') { const r = simulateRace(stu); toast(r.msg, r.ok ? 'info' : 'err'); render(); return; }
  if (act === 'grant') { const r = doGrant(stu, el('ext-' + stu).value); toast(r.msg, r.ok ? 'ok' : 'err'); render(); return; }
  if (act === 'remind') { const r = doRemind(stu, el('m-' + stu).value, el('n-' + stu).value); toast(r.msg, r.ok ? 'ok' : 'err'); render(); return; }
  if (act === 'assistantTryReview') { toast('403 forbidden：助教无批阅权限（服务端按角色拦截，不是前端藏按钮）', 'err'); return; }
});

document.addEventListener('change', ev => {
  if (ev.target && ev.target.id === 'userSwitch') {
    SESSION = ev.target.value;
    VIEW = (TABS[user(SESSION).role] || [['home', '']])[0][0];
    render();
  }
});

/* ---------------- 启动 ---------------- */
DB = freshDB();
render();
