# Kiro Plugin 实现计划

- 日期：2026-07-29
- Spec：[2026-07-29-kiro-plugin-design.md](../specs/2026-07-29-kiro-plugin-design.md)
- 分支：`docs/kiro-plugin-design`（实现另起 `feat/kiro-plugin`）

---

## 贯穿全程的架构决定：IO 与解析分离

`q_dev` 没有任何 e2e 测试，原因是结构性的：它的 extractor 在 subtask 内部直接调 `data.S3Client` 读文件、就地解析、就地写库。三件事耦合在一个函数里，既无法回放也无法单测。

`kiro` 每条数据流都拆成两层：

```
纯解析函数                         subtask 薄壳
parseUserReport([]byte)      ←──   GET → 调解析 → 批量落库
  → ([]*UserReport,
     []*UserModelMessage, error)
```

**解析函数不碰 S3、不碰数据库、不碰 taskCtx**，输入是字节、输出是 model 结构体。这带来三个好处：

1. 单测可以直接喂真实样本字节，覆盖 19 种表头变体和所有边界情况
2. 解析逻辑的改动不需要跑起 DevLake
3. subtask 薄壳只剩编排，逻辑少到不容易错

这是本计划里唯一一处对 spec 的结构性补充，其余任务严格按 spec 落地。

---

## 任务序列

任务按依赖排序。T1–T5 是纯函数与模型，可完全独立开发和验证；T6 起需要 DevLake 上下文。

### T1 — 归一化工具包（纯函数）

**为什么排第一**：spec 里最容易出错的几处（前缀剥离、`Total_Messages` 排除、布尔大小写）全在这里，且全是纯函数。先把它们钉死，后面所有解析器都建立在可信的基础上。

**文件**
```
backend/plugins/kiro/tasks/normalize.go
backend/plugins/kiro/tasks/normalize_test.go
```

**函数**

| 函数 | 行为 | Spec 依据 |
|---|---|---|
| `SplitUserId(raw) (userId, identityStoreId string)` | `d-9067deb161.6478a4a8-...` → `("6478a4a8-...", "d-9067deb161")`；无前缀时 → `(raw, "")` | §3.1.3、§3.2 |
| `ParseKiroBool(s) (bool, errors.Error)` | 接受 `TRUE`/`true`/`True`/`FALSE`/`false`/`False`；其余报错 | §3.1.3 |
| `NormalizeTier(s) string` | 转 UPPER_SNAKE：`Power`→`POWER`、`ProPlus`→`PRO_PLUS`；未知值原样大写返回 | §3.1.2 |
| `ParseModelColumn(header) (modelName string, ok bool)` | `claude_opus_4.6_messages`→`("claude_opus_4.6", true)`；**`Total_Messages`→`("", false)`** | §3.1 |
| `CanonicalModelName(s) string` | 分隔符归一供查询层比较：`claude-opus-4.6` 与 `claude_opus_4.6` 归一到同一形式 | §3.2、§5.3 |
| `ParseKiroTime(s) (time.Time, errors.Error)` | `2026-07-27T23:03:29.027400929Z` → 截断到微秒 | §6.6 |
| `ParseReportDate(s) (time.Time, errors.Error)` | 严格 ISO `2026-07-27`；不接受其他格式 | §6.6 |

**测试用例（必须全覆盖）**

```
SplitUserId:      带前缀 / 不带前缀 / 空串 / 只有前缀无 UUID / 多个点号
ParseKiroBool:    TRUE true True FALSE false False "" "yes" "1"
NormalizeTier:    POWER PRO_PLUS Power ProPlus ProMax power ""  未知值
ParseModelColumn: 8 个真实模型列 + Total_Messages + total_messages(小写)
                  + Chat_Conversations(不匹配) + "_messages"(空捕获组)
CanonicalModelName: claude-opus-4.6 vs claude_opus_4.6 归一后相等
ParseKiroTime:    纳秒 / 微秒 / 无小数位 / 非法格式
ParseReportDate:  2026-07-27 / 07-27-2026(必须报错) / 非法
```

`ParseModelColumn` 对 `total_messages`（小写）也必须返回 `false` —— 正则是大小写无关的，排除逻辑也必须是。

**验证**
```bash
cd backend && go test ./plugins/kiro/tasks/ -run 'TestSplitUserId|TestParseKiroBool|TestNormalizeTier|TestParseModelColumn|TestCanonicalModelName|TestParseKiroTime|TestParseReportDate' -v
```

---

### T2 — 数据模型与 migration

**依赖**：无（可与 T1 并行）

**文件**
```
backend/plugins/kiro/models/connection.go
backend/plugins/kiro/models/s3_slice.go
backend/plugins/kiro/models/s3_file_meta.go
backend/plugins/kiro/models/user_report.go
backend/plugins/kiro/models/user_model_messages.go
backend/plugins/kiro/models/chat_log.go
backend/plugins/kiro/models/completion_log.go
backend/plugins/kiro/models/migrationscripts/20260729_init.go
backend/plugins/kiro/models/migrationscripts/register.go
```

按 spec §5 逐字段落地。**三处最容易写错、需要 review 时逐个核对**：

| 检查点 | 要求 | Spec |
|---|---|---|
| `s3_file_meta` 主键 | `(connection_id, s3_path)`，**不是** `file_name` | §5.1、§7.1 |
| `user_report.user_email` / `is_new_user` | **nullable** | §3.1.1、§5.2 |
| `chat_log.model_id` | **nullable**（仅 45% 覆盖）| §3.2、§5.4 |

所有从 JSON 解析的可选字段用指针类型（§5.6）。

**验证**
```bash
cd backend && go build ./plugins/kiro/... && go vet ./plugins/kiro/...
```
外加人工核对：`register.go` 中 migration 已注册，`GetTablesInfo()` 返回全部 7 张表。

---

### T3 — user_report 解析器（纯函数）

**依赖**：T1、T2

**文件**
```
backend/plugins/kiro/tasks/user_report_parser.go
backend/plugins/kiro/tasks/user_report_parser_test.go
backend/plugins/kiro/tasks/testdata/user_report/*.csv
```

**签名**
```go
func ParseUserReport(data []byte, connectionId uint64, scopeId string) (
    []*models.KiroUserReport,
    []*models.KiroUserModelMessage,
    errors.Error,
)
```

**实现要点**

1. **按列名映射，不做任何位置假设**（§3.1.1：19 种表头、列顺序会变）
2. 未知列**忽略**，不报错（§3.1.1）
3. 已知列缺失 → 落 NULL，不落零值（§5.6）
4. `UserId` 过 `SplitUserId`（§3.1.3：CSV 也可能带前缀）
5. 动态列过 `ParseModelColumn`，产出 `KiroUserModelMessage` 行
6. 多数据行（§3.1.2：实测有 2 行的文件）

**测试 fixture** —— 从真实的 19 种表头变体中取，覆盖：

| fixture | 覆盖点 |
|---|---|
| 最早变体（无 `New_User`、无 `User_Email`、无模型列）| 两列均落 NULL |
| `...,Total_Messages,New_User,User_Email,auto_messages` | 标准顺序 |
| `...,Total_Messages,auto_messages,claude_opus_4.6_messages,New_User` | **`New_User` 在模型列之后** |
| 3 个模型列的变体 | 多动态列 |
| 带 `d-xxx.` 前缀的 UserId（2026-03-10）| 前缀剥离 |
| `Overage_Enabled=TRUE`（大写）| 布尔大小写 |
| 2 数据行（2026-03-24）| 多用户 |
| `KIRO_WEB` + 非零 `Overage_Credits_Used` | 第四种 client type |
| `PRO_PLUS` tier | tier 取值 |

**fixture 脱敏**：CSV 中只有 `User_Email` 与 `UserId` 属敏感信息，替换为 `dev1@example.com` / 固定假 UUID，其余数值原样保留。

**断言**：除逐字段比对外，加一条不变式 —— 同一行的模型列之和 == `Total_Messages`（§3.1.3）。

**验证**
```bash
cd backend && go test ./plugins/kiro/tasks/ -run TestParseUserReport -v
```

---

### T4 — 日志解析器（纯函数）

**依赖**：T1、T2

**文件**
```
backend/plugins/kiro/tasks/log_parser.go
backend/plugins/kiro/tasks/log_parser_test.go
backend/plugins/kiro/tasks/testdata/logs/*.json.gz
```

**签名**
```go
func ParseChatLog(gzData []byte, connectionId uint64, scopeId string) ([]*models.KiroChatLog, errors.Error)
func ParseCompletionLog(gzData []byte, connectionId uint64, scopeId string) ([]*models.KiroCompletionLog, errors.Error)
```

**chat log 实现要点**

| 要点 | Spec |
|---|---|
| 遍历 `records` 数组（1~2 条，不假设单条）| §6.6 |
| `prompt == ""` → `has_prompt=false`、`prompt_sha256=NULL`、`has_steering=NULL`、`is_spec_mode=NULL` | §5.4 |
| `prompt != ""` → 计算 SHA256 与启发式字段，**算完丢弃原文** | §5.4 |
| `modelId` 缺失 → NULL（55% 会缺失）| §3.2 |
| `followupPrompts` 存在 → `has_followup_prompts=true` | §3.2 |
| `userId` 过 `SplitUserId` | §3.2 |
| `conversationId` / `utteranceId` 缺失 → NULL | §3.2 |

**completion log 实现要点**

| 要点 | Spec |
|---|---|
| `completions` 空数组 → `completions_count=0`，**记录仍落库** | §6.6 |
| `returned_line_count` = Σ 各 completion 的行数（**不叫 `accepted_`**）| §3.3、§5.5 |
| `file_name` 只有文件名，`file_extension` 从中推导 | §3.3 |
| `customizationArn` 存在（与 chat log 不同）| §3.3 |

**测试 fixture 与脱敏**

日志含真实工作内容（`assistantResponse` 与非空 `prompt` 中有仓库路径与 commit SHA），**必须脱敏**：保留 JSON 结构与字符串长度，内容替换为占位文本。长度保留是必要的，因为 `prompt_length` / `response_length` 是被断言的字段。

fixture 覆盖：空 prompt / 非空 prompt、有 `modelId` / 无 `modelId`、有 `followupPrompts` / 无、单记录 / 双记录文件、空 `completions` / 非空 `completions`。

**验证**
```bash
cd backend && go test ./plugins/kiro/tasks/ -run 'TestParseChatLog|TestParseCompletionLog' -v
```

---

### T5 — S3 client（双 bucket）

**依赖**：T2

**文件**
```
backend/plugins/kiro/tasks/s3_client.go
backend/plugins/kiro/tasks/s3_client_test.go
backend/plugins/kiro/tasks/identity_client.go
```

**要点**

- 建**两个 client 实例**，各绑一个 bucket（§4.3）。不把 bucket 提升为方法参数 —— 两个 bucket 可能有不同 KMS 密钥与 IAM 条件，独立实例的权限边界更清晰
- `PromptLogBucket` 为空 → 回落到 `Bucket`；`PromptLogPrefix` 为空 → 默认 `logging`；`ReportPrefix` 为空 → 默认 `user-report`（§4.3）
- `identity_client.go` 只做 `UserId → DisplayName`，**不再负责 email**（§4.2）。返回 nil client 时整条链路仍可用

**测试**：用接口 mock（照 `q_dev/tasks/identity_client.go` 的 `IdentityStoreAPI` 模式）。验证回落逻辑的四种组合：单桶、双桶、前缀为空、前缀非空。

**验证**
```bash
cd backend && go test ./plugins/kiro/tasks/ -run 'TestS3Client|TestBucketFallback' -v
```

---

### T6 — 文件发现 subtask

**依赖**：T2、T5

**文件**
```
backend/plugins/kiro/tasks/s3_file_collector.go
```

**要点**

- 按 listing 分页批处理：每页 1000 个 key → 一次 `IN` 查询取已存在集合 → 一次批量 INSERT（§6.2、§7.4）
- 后缀过滤 `.csv` / `.json.gz`（顺带排除 `KiroLogs/` 根下的 8 个无扩展名权限校验文件，§6.6）
- 按前缀打 `file_type`：`report` / `chat_log` / `completion_log`（§5.1）
- `IsTruncated` 是指针，需防御 nil（§6.6）
- 查询条件必须与主键 `(connection_id, s3_path)` 逐列对齐（§7.1）

**验证**

单测覆盖分页与 `file_type` 判定（mock S3 ListObjectsV2 返回多页）。另加一条**必须人工确认**的检查：

```bash
cd backend && grep -n "dal.Where" plugins/kiro/tasks/s3_file_collector.go
```
逐条核对 where 子句的列与 `s3_file_meta` 主键一致 —— 这正是 §7.1 那个缺陷的成因，值得用一次显式检查挡住。

---

### T7 — extractor subtasks（IO 薄壳 + 并发）

**依赖**：T3、T4、T6

**文件**
```
backend/plugins/kiro/tasks/user_report_extractor.go
backend/plugins/kiro/tasks/chat_log_extractor.go
backend/plugins/kiro/tasks/completion_log_extractor.go
```

**三个 extractor 共用的骨架**

```
选待处理: WHERE scope_id=? AND file_type=? AND processed=false AND attempt_count<3
  ↓
[并发] WorkerScheduler(20 workers) → 每个 worker: S3 GET + 调 T3/T4 的解析函数
  ↓  结果推 channel
[主协程] 批量落库（单文件一个事务）
  ↓
成功 → processed=true, record_count=N, processed_time=now
失败 → attempt_count+=1, error_message=原因, processed 保持 false
```

**两条硬约束**

1. **worker 内不写库**（§6.3）—— 20 个 worker 并发 INSERT 会因 MySQL 间隙锁产生死锁，DevLake 的重试机制会把它变成随机失败，是只在高并发下偶发的 bug
2. `attempt_count < 3`（§6.4）—— 缺少它时一个永久损坏的 gz 会每轮重试，该 scope 永远无法完成

`user_report_extractor` 是 CSV、文件数少（107 个/年/用户），**不需要并发**，串行即可。只有两个日志 extractor 需要。

**验证**
```bash
cd backend && go test ./plugins/kiro/tasks/ -v
cd backend && make lint
```

---

### T8 — impl.go 接线

**依赖**：T1–T7

**文件**
```
backend/plugins/kiro/kiro.go
backend/plugins/kiro/impl/impl.go
```

**要点**

- 接口集与 `q_dev` 一致（§4.1）
- `SubTaskMetas()` 返回 4 个 subtask，顺序：collect → report → chat → completion（§6.1）
- `PrepareTaskData()` 构造 3 条前缀（§2.1），**不保留** `q_dev` 的 legacy scope 分支（§4.2）
- `Scope()` 返回 `&models.KiroS3Slice{}`；`ScopeConfig()` 返回 `nil`（§4.4）

**前缀模板**（§附录 A 实测确认）
```
{Bucket}/{ReportPrefix}/AWSLogs/{acct}/KiroLogs/user_report/{region}/{y}/{m}
{PromptLogBucket}/{PromptLogPrefix}/AWSLogs/{acct}/KiroLogs/GenerateAssistantResponse/{region}/{y}/{m}
{PromptLogBucket}/{PromptLogPrefix}/AWSLogs/{acct}/KiroLogs/GenerateCompletions/{region}/{y}/{m}
```

**验证**
```bash
cd backend && make build-plugin 2>&1 | tail -20
```

---

### T9 — API 层

**依赖**：T8

**文件**
```
backend/plugins/kiro/api/{init.go,connection.go,test_connection.go,s3_slice_api.go,blueprint_v200.go}
```

照 `q_dev/api/` 同构实现。`test_connection.go` 需要验证**两个 bucket 都可访问**（若配置了双桶），而非只验一个。

**验证**
```bash
cd backend && make build-plugin && go test ./plugins/kiro/api/... -v
```

---

### T10 — 真实数据烟测

**依赖**：T1–T9

用 `s3://ychchen-q-dev/` 跑一次端到端，验证的是**真实数据而非 fixture**。

**步骤**

1. 建 connection：`Bucket=ychchen-q-dev`、`ReportPrefix=user-report`、`PromptLogPrefix=logging`、`Region=us-east-1`
2. 建 scope：`accountId=034362076319`、`year=2026`、`month=7`
3. 跑 blueprint
4. 核对：

| 断言 | 期望值（实测已知）|
|---|---|
| `_tool_kiro_user_report` 中 2026-07-27 的行数 | 2（KIRO_CLI + KIRO_IDE）|
| `KIRO_CLI` 的 `total_messages` / `credits_used` | 618 / 308.97581546497514 |
| `KIRO_IDE` 的 `total_messages` / `credits_used` | 30 / 19.59644039641791 |
| `user_email` | `ychchen@amazon.com`（两行都有）|
| `_tool_kiro_user_model_messages` 中该日行数 | 2（各 client type 一行 `claude_opus_5`）|
| `_tool_kiro_chat_log` 中 2026-07-27 的行数 | 118 |
| 该日 `has_prompt=true` 的行数 | 16 |
| `_tool_kiro_s3_file_meta` 中 `processed=false` 的行数 | 0 |
| `error_message` 非空的行数 | 0 |

5. 再跑一次 blueprint，确认**增量生效**：`s3_file_meta` 行数不变、无重复插入、无重复 GET

**再扩一个月验证跨月与表头漂移**：scope 改 `month=3`，该月含带 `d-` 前缀的 UserId 与 `TRUE` 布尔值，是格式漂移的实战检验。核对 2026-03-10 那行的 `user_id` 是裸 UUID、`identity_store_id` = `d-9067deb161`。

---

## 任务依赖图

```
T1 归一化 ──┬─→ T3 report 解析 ──┐
            └─→ T4 log 解析 ─────┤
T2 models ──┬────────────────────┼─→ T7 extractors ─→ T8 impl ─→ T9 api ─→ T10 烟测
            └─→ T5 S3 client ──→ T6 collector ──┘
```

T1 与 T2 可并行起步。T3、T4、T5 在 T1/T2 完成后可三路并行。

---

## 全局验证

每个任务完成后跑：
```bash
cd backend && go build ./plugins/kiro/... && go vet ./plugins/kiro/... && go test ./plugins/kiro/... 
```

全部完成后：
```bash
cd backend && make lint && make unit-test-go && make build-plugin
```

---

## 明确不做的事

按 spec §2.4，以下内容**不在本计划范围**，实现时若发现相关问题只记录不修：

- 修改 `q_dev`（包括 spec §7 中列为佐证的四个缺陷）
- 采集 `by_user_analytic`
- 写 domain layer（`accounts` / `user_accounts`）—— kiro 只写 `_tool_kiro_*`
- Grafana 看板
- dashboard metrics API
- AI 提效归因逻辑

---

## 已知风险与应对

| 风险 | 何时会暴露 | 应对 |
|---|---|---|
| 上游 schema 在实现期间又变 | T10 烟测 | 未知列忽略机制已覆盖；若关键列消失，`error_message` 会暴露 |
| e2e 快照框架不适配 S3 型数据源 | T10 | 主要覆盖靠 T3/T4 的纯函数单测（喂真实字节），e2e 只覆盖落库层；这是 IO/解析分离的直接收益 |
| 整体 `make unit-test` 需要 mock 与 python 工具链 | T1 | 单任务验证只跑 `go test ./plugins/kiro/...`（`unit-test-go` 本身不依赖 mock）；`make unit-test` 留到 T10 之后跑一次完整的 |
| 双 bucket 无环境可测 | T5 | 单测覆盖回落逻辑的四种组合；真实双桶场景列入 spec §9.2 待验证 |
