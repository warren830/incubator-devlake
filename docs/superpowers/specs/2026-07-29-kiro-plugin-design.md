# Kiro Plugin 设计文档

- 日期：2026-07-29
- 状态：待评审
- 范围：新建独立的 `kiro` DevLake 插件，采集 Kiro 企业版的用户活动报表与交互日志

---

## 1. 背景与目标

### 1.1 为什么要新插件

老版本 Amazon Q Developer 下线，公司改用 Kiro。现有 `q_dev` 插件按老版 Q Developer 的数据格式实现，与 Kiro 企业版实际产出的数据存在多处不一致（详见 §3、§7）。

决定：**新建独立的 `kiro` 插件；`q_dev` 保留、冻结、本项目不修改它**。

不共享代码。依据两点：

1. 仓库先例——`bitbucket_server` 未 import `bitbucket` 任何代码，插件孪生走完全独立路线。
2. `q_dev` 是要冻结的。让一个持续演进的插件依赖冻结插件的公共包，等于把自己焊在死代码上。

两者唯一共享的是 domain layer（`accounts` / `user_accounts` / `users`），那本来就是公共层。

### 1.2 本文档的范围

**本文档只覆盖数据采集**，即把 Kiro 的数据准确、完整、可增量地落到 `_tool_kiro_*` 表。

不覆盖"如何用这些数据判断 AI 提效"——那是独立的后续工作，方向记录在 §10。

---

## 2. 范围

### 2.1 v1 采集

| 数据源 | S3 路径模板 | 落表 |
|---|---|---|
| 用户活动报表 | `{Bucket}/{ReportPrefix}/AWSLogs/{acct}/KiroLogs/user_report/{region}/{y}/{m}/{d}/00/` | `_tool_kiro_user_report`、`_tool_kiro_user_model_messages` |
| Chat 交互日志 | `{PromptLogBucket}/{PromptLogPrefix}/AWSLogs/{acct}/KiroLogs/GenerateAssistantResponse/{region}/{y}/{m}/{d}/{h}/` | `_tool_kiro_chat_log` |
| Inline 补全日志 | `{PromptLogBucket}/{PromptLogPrefix}/AWSLogs/{acct}/KiroLogs/GenerateCompletions/{region}/{y}/{m}/{d}/{h}/` | `_tool_kiro_completion_log` |

### 2.2 明确不采：`by_user_analytic`（legacy 报表）

**决定：v1 及后续均不采集。**

理由（基于实测样本 `034362076319_by_user_analytic_202607270000_report.csv`）：

- 共 46 列 = 2 个标识列（`UserId`、`Date`）+ 44 个指标列。**44 个指标列中 42 列为 0**，仅 `Chat_AICodeLines` 与 `Chat_MessagesSent` 非零。
- 那 42 列全是 Q Developer 时代 slash command 的指标：`Dev_*`（/dev）、`TestGeneration_*`（/test）、`DocGeneration_*`（/doc）、`Transformation_*`（/transform）、`CodeFix_*` / `CodeReview_*`、`Inline*`。Kiro 的 agentic 模式不走 slash command，CLI 无 inline 补全 —— 这些列在当前使用方式下**永久为 0**，非"当日无数据"。
- 唯一非零且唯一的列是 `Chat_AICodeLines = 8854`。但该指标属于代码产出量，按既定口径只做描述性展示、不进主结论；而"用量剂量"轴由 `Credits_Used` 与 `Total_Messages` 更好地承担（均在 `user_report` 中）。
- 该报表自身埋点残缺：`Chat_MessagesInteracted = 0` 却 `Chat_AICodeLines = 8854`，两者按官方定义矛盾。

**可逆性**：AWS 持续向 S3 写入该报表，S3 不删除历史。scope 是"账号+年+月"时间切片，将来若要采集，重跑历史月份即可全量回补。此决定不造成数据丢失。

### 2.3 v2 候选：dashboard metrics

Kiro 控制台 dashboard 无公开 API，但 IAM 权限列表含 `q:ListDashboardMetrics`，暗示存在可调接口。它唯一提供而 `user_report` 无法计算的是 **total / pending subscriptions per tier**（买了但从未激活的席位）。

接口形状未文档化，需实测。列为 v2 候选，不阻塞 v1。

### 2.4 明确不在范围内

| 项 | 说明 |
|---|---|
| **修改 `q_dev`** | `q_dev` 保留、冻结、不做任何改动，包括 §7 中列出的那些缺陷。本项目只保证 `kiro` 正确。 |
| `by_user_analytic` 采集 | 见 §2.2 |
| domain layer 建模 | `kiro` 只写 `_tool_kiro_*`，不写 domain layer。跨工具 AI 语义统一是独立课题 |
| AI 提效归因与看板 | 见 §10，独立立项 |
| Kiro Hooks 采集 | 客户端可关闭、可篡改，且与本插件的 S3 采集是不同的信任模型。见 §10.2 |

---

## 3. 实测数据源 schema

> **重要**：以下全部来自真实样本，非官方文档。官方文档在 chat log 上已被证明与实际不符（文档列出 6 个字段实际不存在）。
>
> 采样环境：AWS 账号 `034362076319`，bucket `s3://ychchen-q-dev/`。

### 3.1 `user_report` CSV

**文件命名**：`{clientType}_{accountId}_user_report_{timestamp}.csv`

**一天产出多个文件，每个 client type 一个。** 实测 2026-07-27 有两个：

```
KIRO_CLI_034362076319_user_report_202607270000.csv   (398 B)
KIRO_IDE_034362076319_user_report_202607270000.csv   (394 B)
```

**两个文件表头完全相同**：

```csv
Date,UserId,Client_Type,Chat_Conversations,Credits_Used,Overage_Cap,Overage_Credits_Used,Overage_Enabled,ProfileId,Subscription_Tier,Total_Messages,New_User,User_Email,claude_opus_5_messages
```

**实测数据行**：

```csv
2026-07-27,"6478a4a8-60a1-70d9-37bc-6aae85f6746a",KIRO_CLI,39,308.97581546497514,10000.0,0.0,true,"arn:aws:codewhisperer:us-east-1:034362076319:profile/AK9NEHM4WYCR",POWER,618,false,"ychchen@amazon.com",618
2026-07-27,"6478a4a8-60a1-70d9-37bc-6aae85f6746a",KIRO_IDE,2,19.59644039641791,10000.0,0.0,true,"arn:aws:codewhisperer:us-east-1:034362076319:profile/AK9NEHM4WYCR",POWER,30,false,"ychchen@amazon.com",30
```

**关键事实**：

| 事实 | 说明 |
|---|---|
| 同一用户同一天出现在两个文件中 | 主键**必须**包含 `client_type`，否则相互覆盖 |
| `User_Email` 是原生列 | `ychchen@amazon.com` —— 这是接通 git 作者身份的关键字段 |
| `New_User` 是原生列 | `false`。标记用户是否在报表日激活订阅，可直接作为采纳时点，无需从"首次出现用量"反推 |
| `Subscription_Tier` = `POWER` | **全大写**。官方文档写作 `Power`。任何 tier→价格映射必须大小写无关，且需未知值兜底 |
| `ProfileId` 是完整 ARN | `arn:aws:codewhisperer:us-east-1:034362076319:profile/AK9NEHM4WYCR`，非短 id |
| `Date` 格式 | ISO `2026-07-27` |
| `Credits_Used` 高精度浮点 | `308.97581546497514`，用 float64 |
| `UserId` 是裸 UUID | `6478a4a8-60a1-70d9-37bc-6aae85f6746a`（36 字符），**与日志中的格式不同**，见 §3.2 |
| 动态列 `claude_opus_5_messages` | 列名随实际使用的模型而变，固定 struct 无法容纳 |

**动态列解析规则**：遍历表头，凡匹配 `(?i)^(.+)_messages$` 者视为 per-model 计数，`model_name` 取捕获组。

> **必须显式排除 `Total_Messages`**。该列名同样匹配上述正则，会被误解析为一个名叫 `Total` 的模型，且其值恰好等于所有模型之和 —— 下游做占比分析时会得到看似合理的错误结果（如恒定 50%），极难发现。

### 3.2 `GenerateAssistantResponse`（chat 日志）

**采样规模**：105 个文件 / 118 条记录（2026-07-27 23 时全量）。全天：**627 个对象 / 292,141 字节**。

**记录数分布**：92 个文件含 1 条，13 个文件含 2 条（均值 1.12）。

**字段并集 —— 只有 7 个，且 118/118 全部存在**：

```
generateAssistantResponseEventRequest.prompt
generateAssistantResponseEventRequest.chatTriggerType
generateAssistantResponseEventRequest.userId
generateAssistantResponseEventRequest.timeStamp
generateAssistantResponseEventRequest.modelId
generateAssistantResponseEventResponse.assistantResponse
generateAssistantResponseEventResponse.requestId
```

**官方文档列出但 0/118 出现的字段**：

```
messageMetadata.conversationId      ← 无 session 分组能力
messageMetadata.utteranceId
customizationArn
followupPrompts
codeReferenceEvents
supplementaryWebLinksEvent
```

**实测值分布**：

| 字段 | 分布 |
|---|---|
| `prompt` 长度 | **102/118 = 0（空）**；16/118 有内容，长度 1236 / 1282 / 1363 / 1369 / 1920 / 1928 / 2007 / 2015 |
| `chatTriggerType` | 118/118 = `MANUAL` |
| `modelId` | 118/118 = `claude-opus-5` |
| `userId` | `d-9067deb161.6478a4a8-60a1-70d9-37bc-6aae85f6746a`（49 字符）|
| `timeStamp` | `2026-07-27T23:03:29.027400929Z`（**纳秒精度**）|
| `requestId` | UUID（36 字符）|

**`prompt` 空值的含义**：空 prompt 对应 agent 自主续轮（工具调用返回后继续推理），非空 prompt 对应用户真正开口。该区分产生一个派生字段 `has_prompt`，其比值（本样本约 102:16 ≈ 6.4）反映 agent 自主程度。

**`userId` 格式差异**：日志中带 `d-{identityStoreId}.` 前缀，CSV 中是裸 UUID。

```
日志: d-9067deb161.6478a4a8-60a1-70d9-37bc-6aae85f6746a
CSV:               6478a4a8-60a1-70d9-37bc-6aae85f6746a
```

**落库前必须剥离前缀**，否则日志与报表永远 join 不上，且不会报错，只得到空结果。附带收益：`d-9067deb161` 即 Identity Store ID，可从日志反推，无需配置。

### 3.3 `GenerateCompletions`（inline 补全日志）

**采样规模**：全量 430 个对象 / 1,601,826 字节（均值 3.7 KB/文件，为 chat log 的 8 倍）。

**最后一条数据为 2026-03-19。** 当前（7 月）的 agentic 使用方式不产生此类日志。

**字段结构**：

```
generateCompletionsEventRequest:  customizationArn, fileName, leftContext, rightContext, timeStamp, userId
generateCompletionsEventResponse: completions[], requestId
```

**实测值**（2026-03-19，11 条记录）：

| 字段 | 分布 |
|---|---|
| `completions` 数组长度 | **7/11 = 0（空数组）**；4/11 = 1 |
| 非空 completion 内容长度 | 347 / 262 / 315 / 34 字符 |
| 非空 completion 行数 | 15 / 10 / 11 / 2 |
| `leftContext` 长度 | 5324 / 4345 / 4347 / 2317 |
| `rightContext` 长度 | 12 |
| `fileName` | **`mcp.json`（11/11）—— 纯文件名，不含路径** |
| `customizationArn` | 存在（chat log 中不存在）|

**两点语义修正**：

1. **日志在请求时产生，不在接受时产生。** 空 `completions` 数组大量存在，说明记录并非"用户接受了建议"的证据。因此派生字段应命名为 `returned_line_count` 而非 `accepted_line_count`。
2. **`fileName` 只有文件名，没有目录路径。** `mcp.json` 在一个仓库中可能存在多处，无法唯一映射到 `commit_files.file_path`。文件级归因能力因此显著弱于预期。

---

## 4. 架构与落地位置

### 4.1 文件结构

```
backend/plugins/kiro/
  kiro.go
  impl/impl.go
  api/
    init.go
    connection.go
    test_connection.go
    s3_slice_api.go
    blueprint_v200.go
  models/
    connection.go              → _tool_kiro_connections
    s3_slice.go                → _tool_kiro_s3_slices          (scope)
    s3_file_meta.go            → _tool_kiro_s3_file_meta        (增量游标)
    user_report.go             → _tool_kiro_user_report
    user_model_messages.go     → _tool_kiro_user_model_messages
    chat_log.go                → _tool_kiro_chat_log
    completion_log.go          → _tool_kiro_completion_log
    migrationscripts/
  tasks/
    s3_client.go
    identity_client.go
    s3_file_collector.go
    user_report_extractor.go
    chat_log_extractor.go
    completion_log_extractor.go
    task_data.go
```

实现的插件接口与 `q_dev` 一致：

```
plugin.PluginMeta, PluginInit, PluginTask, PluginApi, PluginModel,
PluginSource, PluginMigration, DataSourcePluginBlueprintV200, CloseablePluginTask
```

### 4.2 相对 `q_dev` 删除的部分

| 删除项 | 理由 |
|---|---|
| `models/user_data.go`（46 字段） | 不采 `by_user_analytic`，见 §2.2 |
| `impl.go` 的 legacy scope 分支（`op.S3Prefix` 直传） | `q_dev` 为兼容早期配置保留的后门，新插件从一开始使用结构化 scope |
| `identity_client.go` 的 email 解析职责 | `User_Email` 已是 CSV 原生列，无需调用 Identity Store 获取邮箱 |

`identity_client.go` **保留但降级为可选**：仅负责将 `UserId` 解析为 `DisplayName`。`User_Email` 是身份关联的主键，`DisplayName` 仅用于展示。Identity Store 未配置不影响核心链路。

### 4.3 Connection：支持双 bucket

Kiro 官方建议 prompt log 与用户活动报表使用**不同的 bucket**。`q_dev` 的实现是单 bucket 硬编码双前缀（`impl/impl.go` 的 `PrepareTaskData` 中 `logBase` 把 `logging/` 写死，且与 `csvBase` 共用 `connection.Bucket`），若按官方建议配置成两个 bucket 则读不到 prompt log。

```go
type KiroConn struct {
    AccessKeyId     string
    SecretAccessKey string
    Region          string

    Bucket       string  // 用户活动报表 bucket
    ReportPrefix string  // 空 → 默认 "user-report"

    PromptLogBucket string  // 空 → 回落到 Bucket
    PromptLogPrefix string  // 空 → 默认 "logging"

    IdentityStoreId     string  // 可选
    IdentityStoreRegion string  // 可选

    RateLimitPerHour int
}
```

空值回落让单 bucket 与双 bucket 两种合法配置走同一条代码路径，无需分支。

**实测环境为单 bucket**：`Bucket=ychchen-q-dev`、`ReportPrefix=user-report`、`PromptLogPrefix=logging`。

**实现改动**：`s3_client.go:45` 目前把 `Bucket` 烧进 client 结构体。需改为**建两个 client 实例**，各绑一个 bucket。选择独立实例而非将 bucket 提升为方法参数，因为两个 bucket 可能有不同的 KMS 密钥与 IAM 条件，独立实例的权限边界更清晰、更易排查。

### 4.4 Scope

沿用 `q_dev` 的时间切片模型：`KiroS3Slice = {basePath, accountId, year, month}`。

保留理由：

- **增量粒度合适**：一个 scope = 一个账号一个月，重跑某月不影响其他月。
- **对齐 S3 分区**：Kiro 路径本身是 `{region}/{year}/{month}/{day}/{hour}/`，scope 直接映射到前缀，list 操作无需扫全桶。
- **多账号天然支持**：Kiro 要求用户订阅所在的每个 AWS 账号各建 bucket、不支持跨账号，scope 带 `accountId` 正好对应。

`ScopeConfig()` 返回 `nil`。Kiro 的 CSV 语义由 AWS 定义、全组织统一，无可配置的转换空间。

---

## 5. 数据模型

### 5.1 `_tool_kiro_s3_file_meta`（增量游标）

```
PK: (connection_id, s3_path)

connection_id   uint64
s3_path         varchar(512)          -- 完整 S3 key
file_name       varchar(255)          -- basename
scope_id        varchar(255)  index
file_type       varchar(32)   index   -- report / chat_log / completion_log
processed       bool          default false
processed_time  datetime      nullable
record_count    int           default 0
error_message   text          nullable
attempt_count   int           default 0
```

主键取 `s3_path` 而非 `file_name`，原因见 §7.1。

`record_count` 与 `error_message` 用于可观测性：无此二列时，"某月数据偏少"只能靠猜；有此二列时一条 SQL 即可定位是哪个文件、什么原因。

`file_type` 让 extractor 无需重新解析路径即可筛选待处理文件。

### 5.2 `_tool_kiro_user_report`

```
PK: (connection_id, scope_id, user_id, date, client_type)

user_id              varchar(64)     -- 裸 UUID
date                 date
client_type          varchar(20)     -- KIRO_IDE / KIRO_CLI / PLUGIN
user_email           varchar(255)  index    -- 身份关联主键
display_name         varchar(255)  nullable  -- 来自 Identity Store，仅展示
profile_id           varchar(512)             -- 完整 ARN
subscription_tier    varchar(50)              -- POWER 等，全大写
is_new_user          bool
chat_conversations   int
total_messages       int
credits_used         float64
overage_cap          float64
overage_credits_used float64
overage_enabled      bool
```

### 5.3 `_tool_kiro_user_model_messages`

```
PK: (connection_id, scope_id, user_id, date, client_type, model_name)

model_name     varchar(100)   -- 如 claude_opus_5
message_count  int
```

解析规则与 `Total_Messages` 排除见 §3.1。

### 5.4 `_tool_kiro_chat_log`

```
PK: (connection_id, scope_id, request_id)

request_id         varchar(64)
user_id            varchar(64)   index    -- 剥掉 d-xxx. 前缀的裸 UUID
identity_store_id  varchar(32)            -- 从前缀提取，如 d-9067deb161
timestamp          datetime(6)   index    -- 纳秒截断到微秒
chat_trigger_type  varchar(20)            -- 实测均为 MANUAL
model_id           varchar(100)           -- 实测均为 claude-opus-5
has_prompt         bool          index    -- prompt 非空
prompt_length      int
prompt_sha256      char(64)      nullable index  -- prompt 为空时存 NULL
response_length    int
has_steering       bool          nullable -- 仅 has_prompt=true 时有意义
is_spec_mode       bool          nullable -- 同上
conversation_id    varchar(64)   nullable -- 实测 0/118 存在，建列备将来
utterance_id       varchar(64)   nullable -- 同上
```

**不落库**：`prompt` 原文、`assistantResponse` 原文。派生字段在 extract 阶段用内存中的原文计算，算完丢弃。

`has_steering` / `is_spec_mode` 为**启发式**字段（从 prompt 文本模式匹配），仅在 `has_prompt=true` 时可信，否则为 NULL 而非 false。

`prompt_sha256` 用于识别同一 prompt 被同一用户短时间内反复提交（返工信号）。**prompt 为空时必须存 NULL**，不可 hash 空串 —— 否则 86% 的记录会共享同一 hash 值 `e3b0c442...`，使该字段失效。

### 5.5 `_tool_kiro_completion_log`

```
PK: (connection_id, scope_id, request_id)

request_id           varchar(64)
user_id              varchar(64)   index
identity_store_id    varchar(32)
timestamp            datetime(6)   index
file_name            varchar(255)  index   -- 仅文件名，无路径
file_extension       varchar(50)
has_customization    bool
completions_count    int                   -- len(completions)，0 常见
returned_char_count  int
returned_line_count  int
left_context_length  int
right_context_length int
```

字段命名为 `returned_*` 而非 `accepted_*`，理由见 §3.3。

### 5.6 通用实现纪律

**所有从 JSON 解析的字段使用指针类型**，以区分"字段缺失"与"字段为零值"。

原因：AWS 的 S3 导出在字段为空时直接从 JSON 中省略该键（实测 6 个文档字段 0/118 出现）。若用值类型，缺失会静默变成 `0` / `""` 落库，下游无法区分"AI 返回了 0 行"与"这个字段根本不存在"。

---

## 6. 采集流程

### 6.1 Subtask 编排

| # | Subtask | 职责 |
|---|---|---|
| 1 | `collectKiroS3Files` | 列 3 条前缀，批量写 `s3_file_meta`，打 `file_type` |
| 2 | `extractKiroUserReport` | `file_type=report` → `user_report` + `user_model_messages` |
| 3 | `extractKiroChatLog` | `file_type=chat_log` → `chat_log` |
| 4 | `extractKiroCompletionLog` | `file_type=completion_log` → `completion_log` |

拆成 4 个（`q_dev` 为 3 个，混管两种 CSV 与两种日志）。好处：进度分别上报（2 万文件的任务混在一起无法定位卡点）；单条流出问题可独立重跑。

### 6.2 文件发现（subtask 1）

按 S3 listing 分页批处理，而非逐文件查询：

```
ListObjectsV2 每页 1000 个 key
  ↓
后缀过滤（.csv / .json.gz）
  ↓
一次 SELECT ... WHERE connection_id=? AND s3_path IN (最多 1000 个)  → 已存在集合
  ↓
一次批量 INSERT 剩余项
```

将 `q_dev` 的每文件 2 次数据库往返（2 万文件 = 4 万次）降到每页 2 次（40 次）。

### 6.3 并发（subtask 3 / 4）

日志文件数量大、单个体积小，瓶颈是 GET 往返次数而非带宽。

使用仓库现有的 `api.NewWorkerScheduler(ctx, numOfWorkers, tickInterval, logger)`（基于 `ants` 池，自带限速 tick）：

```
numOfWorkers = 20                            可配
tickInterval ← RateLimitPerHour 推导，默认不限速
```

S3 单前缀支持 5500 GET/s，20 并发远低于上限。

**Worker 边界**：worker 只做 **GET + 解析**，结果推入 channel，由主协程批量落库。

这不是性能优化而是正确性要求：20 个 worker 并发 INSERT 同一张表，在 MySQL 上会因间隙锁产生死锁，而 DevLake 的重试机制会把它变成随机失败 —— 一个只在高并发下偶发、难以复现的 bug。

### 6.4 增量与失败恢复

```
选待处理: WHERE scope_id=? AND file_type=? AND processed=false AND attempt_count < 3
  ↓
GET + 解析 + 落库（单文件一个事务）
  ↓
成功 → processed=true, record_count=N, processed_time=now
失败 → attempt_count += 1, error_message=原因, processed 保持 false
```

`attempt_count < 3` 是必需条件：缺少它时，一个永久损坏的 gz 会在每次运行中重试，刷满日志且该 scope 永远无法"完成"。

### 6.5 规模预估

基于实测：单用户单日 **627 个文件 / 292 KB**，1.12 记录/文件。

按 100 人、人均 200 消息/天估算：

| 指标 | 量级 |
|---|---|
| 文件数 | 约 2 万/天，730 万/年 |
| 数据量 | 数十 MB/天（可忽略）|
| 串行 GET 耗时 | 约 13 分钟/天；回补一年约 80 小时 |
| 20 并发回补一年 | 约 4 小时 |

结论：**存储与成本不是约束，对象数导致的墙钟时间是约束**，必须并发。

### 6.6 边界情况

| 情况 | 处理 |
|---|---|
| `KiroLogs/` 根目录下 8 个 103 字节 UUID 文件（AWS 权限校验文件） | 现有后缀过滤已排除，无需改动 |
| 单个 gz 含 1~2 条记录（实测 92 个 1 条、13 个 2 条） | 按数组遍历，不假设单条 |
| `ListObjectsV2` 的 `IsTruncated` 是指针 | `!*result.IsTruncated` 会 nil panic，需防御 |
| 同一天多个 report CSV（CLI + IDE） | 均按 `file_type=report` 处理，靠 `client_type` 列区分，主键已含该列 |
| `prompt` 为空 | `has_prompt=false`，`prompt_sha256` = NULL，`has_steering` / `is_spec_mode` = NULL |
| `completions` 为空数组 | `completions_count=0`，行数字段为 0，**不跳过该记录** |
| `userId` 带 `d-xxx.` 前缀 | 拆为 `identity_store_id` + `user_id`，落库前剥离 |
| 两种日期格式 | `user_report` 为 ISO `2026-07-27`。按 `file_type` 分别指定格式，不用宽松解析器 |
| `Subscription_Tier` 大小写 | 实测 `POWER`，文档写 `Power`。比较需大小写无关，并对未知值兜底 |

---

## 7. 关键实现约束

以下是 `kiro` 必须满足的硬约束。每条附上判断依据 —— 依据多来自 `q_dev` 的现有实现，因为那是同类数据源在本仓库唯一的实战样本，它踩过的坑就是本插件的约束来源。

> **`q_dev` 不在本文档范围内，不做任何修改。** 引用它仅为说明约束的成因。

### 7.1 增量游标必须按主键查询（严重）

**约束**：`s3_file_meta` 的存在性检查必须走主键，主键即 `(connection_id, s3_path)`。

**依据** —— `q_dev/tasks/s3_file_collector.go:70`：

```go
db.First(existingFile, dal.Where("connection_id = ? AND s3_path = ?", ...))
```

但 `models/s3_file_meta.go` 中：

```go
ConnectionId uint64 `gorm:"primaryKey"`
FileName     string `gorm:"primaryKey;type:varchar(255)"`   // PK 第二列
S3Path       string `gorm:"type:varchar(512)"`              // 无索引
```

主键是 `(connection_id, file_name)`，查询按 `s3_path`，而该列无任何索引。在 730 万行规模下，每个文件的存在性检查都要扫该 connection 下的全部行 —— 2 万文件/天 × 700 万行扫描，任务无法完成。

此类缺陷的形态值得记住：**模型的索引声明与查询条件不一致**。它在小数据量下完全不可见，到千万行才暴露，且表现为"任务跑不完"而非报错。

**实现要求**：主键 `(connection_id, s3_path)`，查询条件与主键逐列对齐。Code review 时对照模型的索引声明检查每一处 DAL 查询。

### 7.2 完整 S3 key 不得存入 varchar(255)

**约束**：`file_name` 存 basename（`varchar(255)`），`s3_path` 存完整 key（`varchar(512)`）。

**依据**：`q_dev` 的 collector 把完整 S3 key 赋给了 `FileName`（`varchar(255)`）。当前 key 长约 155 字符尚可，前缀再长即会截断或插入失败。

### 7.3 不建没有数据源的字段

**约束**：字段必须有实测可见的数据源，不得依据官方文档建列。

**依据**：`q_dev` 的 `QDevChatLog` 含以下字段，但实测 payload 中不存在对应数据源：

| 字段 | 实测 |
|---|---|
| `open_file_count` | payload 中无此字段 |
| `active_file_name` | payload 中无此字段。`q_dev` 从 prompt 文本正则提取，而 86% 的 prompt 为空 |
| `has_steering` | 靠 `strings.Contains(prompt, ".kiro/steering")`，prompt 为空时恒 false |
| `is_spec_mode` | 同上 |

**实现要求**：`open_file_count` 不建；`active_file_name` 不建（无路径来源，且 §3.3 显示 completion log 的 `fileName` 亦无路径）；`has_steering` / `is_spec_mode` 建为 nullable，仅 `has_prompt=true` 时赋值，否则 NULL。

### 7.4 文件发现必须批处理

**约束**：按 S3 listing 分页批量查询与插入，见 §6.2。

**依据**：`q_dev` 采用每文件一次 `SELECT` + 一次 `INSERT`，在 2 万文件/天的规模下是 4 万次数据库往返。

---

## 8. 测试策略

遵循 DevLake 既有约定。

### 8.1 单元测试

| 目标 | 用例 |
|---|---|
| 动态 model 列解析 | 含 `Total_Messages` 的表头必须不产生名为 `Total` 的模型；多个 `{model}_messages` 列全部解析 |
| `userId` 前缀剥离 | `d-9067deb161.{uuid}` → `identity_store_id=d-9067deb161` + `user_id={uuid}`；无前缀输入原样返回 |
| 空 prompt 处理 | `prompt=""` → `has_prompt=false`、`prompt_sha256=nil`、`has_steering=nil` |
| 空 completions | `completions=[]` → `completions_count=0`，记录仍落库 |
| 纳秒时间戳解析 | `2026-07-27T23:03:29.027400929Z` 正确解析并截断到微秒 |
| tier 大小写 | `POWER` / `Power` / `power` 归一化一致；未知值不 panic |
| 缺失字段 | JSON 缺 `modelId` 时落 NULL，不落空串 |
| 日期格式 | `user_report` 的 ISO 格式；非法格式返回错误而非静默错值 |

### 8.2 E2E 快照测试

走 DevLake 的 e2e 快照模式（`e2e/` 目录 + CSV 快照）。

Fixture 需从附录 A 的 S3 路径重新提取，**且必须脱敏后才能入库**：`assistantResponse` 与非空 `prompt` 含真实工作内容（实测样本中包含仓库路径与 commit SHA），不可直接提交到代码库。脱敏方式：保留结构与长度、替换文本内容。

脱敏后的 fixture 需覆盖以下分支（实测样本均已包含）：空 prompt / 非空 prompt、空 `completions` / 非空 `completions`、`KIRO_CLI` / `KIRO_IDE` 两种 client type、单记录文件 / 双记录文件。

---

## 9. 待验证项与风险

### 9.1 待验证

| 项 | 现状 | 验证方式 |
|---|---|---|
| `chatTriggerType` 是否出现 `INLINE_CHAT` | 实测 118/118 为 `MANUAL` | 采集更长时间窗后统计；解析不做枚举白名单，未知值原样落库 |
| 多模型场景 | 实测仅 `claude_opus_5_messages` 一列 | 需一个使用多模型的账号样本，验证多动态列 |
| `PLUGIN` client type | 实测仅 `KIRO_CLI` / `KIRO_IDE` | 同上 |
| `conversation_id` 是否会出现 | 实测 0/118 | 建列备用，nullable |
| `q:ListDashboardMetrics` 接口形状 | 未文档化 | v2 前实测 |
| 多用户 / 多账号 | 实测样本仅 1 个用户、1 个账号 | 需真实团队环境样本，验证 CSV 多行解析与 scope 多账号 |

### 9.2 风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| AWS 单方面变更 CSV 列或 JSON 字段 | 解析失败或静默丢字段 | 按列名映射（非位置）；未知列忽略而非报错；指针类型区分缺失；`error_message` 暴露解析失败 |
| 上游 schema 与官方文档不一致 | 已发生（6 个文档字段实际不存在）| 以真实样本为唯一 schema 依据；建表前必须实测 |
| 对象数增长超预估 | 采集墙钟时间变长 | `numOfWorkers` 可配；scope 按月切片可分批回补 |
| 仅单用户样本 | 多用户场景未验证 | §9.1 列为待验证；上线首个真实团队后复核 |
| `fileName` 无路径 | 文件级归因不可靠 | 本文档不依赖该能力；后续归因工作需另寻路径来源 |

---

## 10. 后续工作（不在本文档范围）

采集层建成后，"如何判断 AI 是否带来提效"是独立课题。已明确的方向记录如下，供后续单独立项：

### 10.1 身份关联

`user_report.user_email` → `accounts`（`organization='kiro'`）→ 复用 `org` 插件现有的 `ConnectUserAccountsExact`（按 email → full_name → user_name 三级精确匹配）→ `user_accounts` → `commits.author_id`。

匹配失败的走 `org` 插件已有的 `PUT /user_account_mapping` 人工补录。

### 10.2 论证强度阶梯

| 级别 | 形式 | 需要的数据能力 | 本插件是否已提供 |
|---|---|---|---|
| L1 相关性 | 高用量组 vs 低用量组的产出差异 | 身份关联 | 是（§10.1）|
| L2 准因果 | 交错采纳 DiD + event study | 采纳时点 + 人×周面板 | 是（`is_new_user` 提供采纳时点）|
| L3 组内对照 | 同一人同一周内，AI 参与的 PR vs 未参与的 PR | commit 级 AI 归因 | 否 |

L3 需要 session → commit 的确定性绑定。已知 Kiro **没有 git lifecycle hook**（社区 feature request `kirodotdev/Kiro#6436` 未实现），且 S3 日志无 `conversationId`（§3.2），因此 S3 数据无法支撑 L3。可行路径是 Kiro Hooks（其 hook event 含 `session_id`）配合 git trailer，但那是独立项目。

### 10.3 已确定的口径约束

- **不产出个人评分或标签。** 人是分析单位，群体是汇报单位。原因：个人评价会改变使用行为，从而污染用来做 L2 / L3 的行为数据 —— 两个目标共用一套指标时，评价目标会吃掉度量目标。
- **代码产出量只做描述性展示，不进主结论。** AI 本身在推高代码行数，用行数论证 AI 有效构成循环论证。
- **速度类结论必须配质量护栏**（change failure rate、新增 sonar 问题）。只报速度提升，无法回应"是否牺牲质量换速度"的质疑。

### 10.4 本插件提供的两个非常规指标

采集实测中发现两个别处得不到、且对提效判断有直接价值的派生指标：

1. **自主率** = `has_prompt=false` 的记录数 / `has_prompt=true` 的记录数。实测 102:16 ≈ **6.4**，即用户每开口 1 次，agent 自主推进约 6.4 轮。它比 credits 更贴近"AI 替我做了多少事"。

   注意该比值来自**单个小时窗口**（2026-07-27 23 时，105 个文件 / 118 条记录）的单用户样本。若外推到当日全量（CLI 618 消息 / 309 credits），意味着仅约 1/7 的消息由用户发起，其余为 agent 循环 —— 但这是**外推而非实测**，需按 §9.1 用更长时间窗与多用户样本验证。

   若该比值稳定，它修正了对"用量"的理解：所谓"用得多"主要是 agent 循环长，而非人问得多。这对区分"AI 干得多"与"人用得勤"有直接价值。

2. **per-model 成本构成**（`user_model_messages`）。不同模型的 credit 成本差异显著，该表可回答"credits 花在哪个模型上"，进而支撑一个可直接行动的决策：用 Auto 路由 vs 手动指定贵模型的成本差异。

---

## 附录 A：样本清单

| 文件 | 来源 | 用途 |
|---|---|---|
| `KIRO_CLI_034362076319_user_report_202607270000.csv` | `s3://ychchen-q-dev/user-report/AWSLogs/034362076319/KiroLogs/user_report/us-east-1/2026/07/27/00/` | report schema、CLI 数据 |
| `KIRO_IDE_034362076319_user_report_202607270000.csv` | 同上 | 验证 IDE 表头一致、同用户双 client type |
| `GenerateAssistantResponse/us-east-1/2026/07/27/23/*.json.gz`（105 个）| `s3://ychchen-q-dev/logging/AWSLogs/034362076319/KiroLogs/` | chat log 字段并集、prompt 空值分布 |
| `GenerateCompletions/us-east-1/2026/03/19/**/*.json.gz`（11 个）| 同上 | completion log schema、completions 分布 |
| `034362076319_by_user_analytic_202607270000_report.csv` | `s3://ychchen-q-dev/user-report/.../by_user_analytic/` | 论证 §2.2 不采集的依据 |
