-- 001 初始化：班级、用户、作业与快照、学生作业状态、提交版本、批阅、补交、积分、催交、事件
-- 约定：时间统一存 UTC RFC3339 文本；状态用大写枚举字符串；所有唯一性靠数据库约束兜底。

CREATE TABLE classes (
    id   TEXT PRIMARY KEY,
    name TEXT NOT NULL
);

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    class_id      TEXT NOT NULL REFERENCES classes (id),
    name          TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('teacher', 'assistant', 'student')),
    password_hash TEXT NOT NULL,
    created_at    TEXT NOT NULL
);

CREATE INDEX idx_users_class ON users (class_id, role);

-- 作业：草稿可改；发布时把内容写入 snapshot_* 列，此后只读（业务层拒绝修改）。
CREATE TABLE assignments (
    id                   TEXT PRIMARY KEY,
    class_id             TEXT NOT NULL REFERENCES classes (id),
    teacher_id           TEXT NOT NULL REFERENCES users (id),
    title                TEXT NOT NULL,
    requirement          TEXT NOT NULL,
    deadline_at          TEXT NOT NULL,
    state                TEXT NOT NULL CHECK (state IN ('DRAFT', 'PUBLISHED')),
    published_at         TEXT,
    snapshot_title       TEXT,
    snapshot_requirement TEXT,
    snapshot_deadline_at TEXT,
    snapshot_by          TEXT,
    snapshot_at          TEXT,
    created_at           TEXT NOT NULL,
    CHECK (state <> 'PUBLISHED' OR (snapshot_at IS NOT NULL AND snapshot_deadline_at IS NOT NULL))
);

CREATE INDEX idx_assignments_class_state ON assignments (class_id, state);

-- 每个学生在每份作业上的状态行；state_version 是并发控制锚点（乐观锁）。
CREATE TABLE enrollments (
    assignment_id      TEXT NOT NULL REFERENCES assignments (id),
    student_id         TEXT NOT NULL REFERENCES users (id),
    state              TEXT NOT NULL CHECK (state IN ('NOT_SUBMITTED', 'SUBMITTED', 'RETURNED', 'PASSED')),
    state_version      INTEGER NOT NULL DEFAULT 0,
    current_version_no INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (assignment_id, student_id)
);

-- 提交版本：只追加，不更新不删除。request_id 唯一，保证重复请求不产生额外版本。
CREATE TABLE submission_versions (
    id            TEXT PRIMARY KEY,
    assignment_id TEXT NOT NULL,
    student_id    TEXT NOT NULL,
    version_no    INTEGER NOT NULL,
    content       TEXT NOT NULL,
    content_hash  TEXT NOT NULL,
    request_id    TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    FOREIGN KEY (assignment_id, student_id) REFERENCES enrollments (assignment_id, student_id),
    UNIQUE (assignment_id, student_id, version_no),
    UNIQUE (assignment_id, student_id, request_id)
);

CREATE INDEX idx_versions_student ON submission_versions (assignment_id, student_id, version_no DESC);

-- 批阅：一个版本只能有一个结论（UNIQUE version_id）。
CREATE TABLE reviews (
    id          TEXT PRIMARY KEY,
    version_id  TEXT NOT NULL UNIQUE REFERENCES submission_versions (id),
    decision    TEXT NOT NULL CHECK (decision IN ('pass', 'reject')),
    feedback    TEXT NOT NULL DEFAULT '',
    teacher_id  TEXT NOT NULL REFERENCES users (id),
    request_id  TEXT,
    created_at  TEXT NOT NULL
);

-- 补交：每个学生每份作业最多一次（主键保证）。
CREATE TABLE extensions (
    assignment_id TEXT NOT NULL REFERENCES assignments (id),
    student_id    TEXT NOT NULL REFERENCES users (id),
    deadline_at   TEXT NOT NULL,
    reason        TEXT NOT NULL,
    granted_by    TEXT NOT NULL REFERENCES users (id),
    granted_at    TEXT NOT NULL,
    PRIMARY KEY (assignment_id, student_id)
);

-- 积分流水：首次通过发 10 分，唯一约束防止重复发奖；本题不实现撤销。
CREATE TABLE points_ledger (
    id            TEXT PRIMARY KEY,
    student_id    TEXT NOT NULL REFERENCES users (id),
    assignment_id TEXT NOT NULL REFERENCES assignments (id),
    reason        TEXT NOT NULL,
    points        INTEGER NOT NULL,
    created_at    TEXT NOT NULL,
    UNIQUE (assignment_id, student_id, reason)
);

CREATE INDEX idx_ledger_student ON points_ledger (student_id, created_at);

-- 催交记录：只追加，可多条；助教无批阅权。
CREATE TABLE reminders (
    id            TEXT PRIMARY KEY,
    assignment_id TEXT NOT NULL REFERENCES assignments (id),
    student_id    TEXT NOT NULL REFERENCES users (id),
    assistant_id  TEXT NOT NULL REFERENCES users (id),
    channel       TEXT NOT NULL,
    note          TEXT NOT NULL,
    created_at    TEXT NOT NULL
);

CREATE INDEX idx_reminders_student ON reminders (assignment_id, student_id, created_at);

-- 审计事件：汇总页时间线的唯一来源。
CREATE TABLE events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    assignment_id TEXT,
    student_id    TEXT,
    actor_id      TEXT NOT NULL,
    actor_role    TEXT NOT NULL,
    type          TEXT NOT NULL,
    detail        TEXT NOT NULL,
    from_state    TEXT,
    to_state      TEXT,
    version_no    INTEGER,
    created_at    TEXT NOT NULL
);

CREATE INDEX idx_events_assignment ON events (assignment_id, created_at);
CREATE INDEX idx_events_student ON events (assignment_id, student_id, created_at);
