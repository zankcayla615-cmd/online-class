# 课下作业平台（题目 03）

网校课下作业的布置、提交与改版、批阅、补交、积分激励与班级汇总。
业务规则见 `deliverables/01-需求分析/`，过程证据见 `deliverables/`。

## 运行

```bash
# 依赖（SQLite 驱动，纯 Go 无 CGO）
go mod download

# 启动（默认 8080，自动建库、迁移、写入演示数据）
go run ./cmd/server

# 健康检查
curl http://localhost:8080/healthz
```

环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PORT` | `8080` | 监听端口 |
| `DB_PATH` | `data/app.db` | SQLite 文件路径 |
| `TEACHING_CLOCK_OFFSET` | 空（0） | 教学时钟偏移，如 `2h`、`-30m`，用于演示逾期与补交 |
| `SEED` | `1` | 是否写入预置班级与账号 |

## 预置账号

1 个班级（P01）+ 1 名教师、1 名助教、5 名学生，另有 1 名外班学生（P02）。

| 账号 | 姓名 | 角色 | 班级 |
| --- | --- | --- | --- |
| T01 | 周老师 | 教师 | P01 |
| A01 | 李助教 | 助教 | P01 |
| S01~S05 | 林小雨 / 王浩然 / 陈子墨 / 赵嘉宁 / 吴一诺 | 学生 | P01 |
| X01 | 外班同学 | 学生 | P02（不可见本班作业） |

演示口令统一为 `demo1234`（仅教学演示，视频与截图中须脱敏）。口令以 PBKDF2-HMAC-SHA256 加盐哈希存储。

## 数据库选择说明

选用 **SQLite（modernc.org/sqlite，纯 Go 实现）**：

- 单实例教学环境足够，重启后业务数据仍可查询（对应 AC-19）；
- 无需额外中间件或 CGO，部署简单；
- WAL + `busy_timeout=5000` 支撑并发演示，写事务使用 `BEGIN IMMEDIATE` 减少锁升级失败；
- 业务冲突判定不依赖数据库锁语义：提交/批阅靠状态行 `state_version` 乐观锁，发奖与补交靠唯一约束兜底；
- 仓储层隔离在 `internal/store`，后续如需 PostgreSQL 只替换实现。

## 目录

```
cmd/server        服务入口
internal/config   配置读取
internal/clock    可注入业务时钟（禁止业务代码直接 time.Now）
internal/auth     口令哈希与校验
internal/store   连接、事务、迁移、种子
  migrations/     SQL 迁移（可重复执行）
deliverables/     需求、原型、任务、约束、Skill、测试与部署证据
```
