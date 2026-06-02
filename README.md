# sloth

PostgreSQL 慢 SQL 智能诊断 agent：周期采集 `pg_stat_statements`，对最慢的语句用 LLM
做根因诊断并给出索引/改写建议，再把告警推送到企业微信 / 飞书 / Lark，同时提供看板 API。

## 架构

```
                          ┌─────────────────────────────┐
  target PG  ──pg_stat──▶ │ collector  (周期快照 + delta) │──┐
  (只读)                   └─────────────────────────────┘  │ SlowSQL
                          ┌─────────────────────────────┐  ▼
                          │ inspect (EXPLAIN/表元数据)    │  store (sqlc + pgx)
                          └─────────────────────────────┘  │
                          ┌─────────────────────────────┐  │
                          │ analyzer (规则引擎 + LLM)     │◀─┘ 证据包
                          └──────────────┬──────────────┘
                                         │ Diagnosis
                    ┌────────────────────┼────────────────────┐
                    ▼                    ▼                     ▼
              store(诊断缓存)      notify(企微/飞书/Lark)    api(gin 看板)
```

## 模块

| 包 | 职责 |
|----|------|
| `internal/collector` | 采集 `pg_stat_statements`，两次快照求 delta，TopN，指纹归一化 |
| `internal/inspect`   | 动态 `EXPLAIN (FORMAT JSON)` + 表/索引内省（原生 pgx，只读） |
| `internal/analyzer`  | 静态规则引擎初筛 + LLM 深度诊断，输出结构化建议 |
| `internal/llm`       | Provider 抽象（mock / claude / openai），含 tool-calling 循环 |
| `internal/notify`    | 多渠道分发：企微 / 飞书 / Lark，限频 + 指纹冷却 + 分级路由 |
| `internal/store`     | sqlc 生成的类型安全存储层 + repository 封装 |
| `internal/api`       | gin REST API（慢 SQL 列表 / 诊断 / 触发） |
| `internal/app`       | 编排：采集 → 内省 → 诊断 → 落库 → 通知 |

## 多实例监控

`config.yaml` 的 `targets[]` 可配置多台 PostgreSQL，每台一个唯一 `name`。sloth 为每个
实例启动独立采集协程，指纹按 `(instance, database, query)` 归一，诊断时 EXPLAIN 自动连回
SQL 来源实例。状态库（`store`）仍是单个。看板用 `?instance=` 过滤。

## 数据采集策略

- **主路** `pg_stat_statements`：轻量、远程、自带 calls/mean_time 指标，做 TopN 排序。
- **辅路**（规划中）：慢日志 + `auto_explain`，为高价值慢 SQL 补充真实参数与真实计划。

## 快速开始

```bash
cp config.example.yaml config.yaml      # 填 DSN / webhook
export SLOTH_LLM_API_KEY=sk-...          # 用真实 LLM 时
make sqlc                                # 生成存储层代码（已提交，改 SQL 后重跑）
make migrate-up                          # 建表（需 golang-migrate）
make run
```

无 LLM key 时 `llm.provider: mock` 可端到端跑通占位诊断。

## API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET  | `/healthz` | 健康检查 |
| GET  | `/api/v1/slow-sql` | TopN 慢 SQL 列表（`?instance=<name>` 按实例过滤，省略则返回全部） |
| GET  | `/api/v1/slow-sql/:fingerprint/diagnosis` | 最近一次诊断结果 |
| POST | `/api/v1/slow-sql/:fingerprint/diagnose` | 触发即时诊断（内省 + LLM + 通知） |

## 安全原则

- **只读优先**：agent 绝不在目标库执行任何变更；索引/改写一律“建议 + 人工确认”。
- **权限最小化**：目标库仅需 `pg_read_all_stats` + `pg_stat_statements`。
- **EXPLAIN 守卫**：仅对 `SELECT`/只读 `WITH` 跑 EXPLAIN，拒绝写语句。
- **成本可控**：诊断结果按指纹缓存，通知按指纹冷却 + 渠道限频。

## 开发

```bash
make test    # 单元测试
make vet
make fmt
```
