# sub2api-pro 二次开发变更说明

> 本文记录 `killaragorn/sub2api-pro` 相对官方 `Wei-Shaw/sub2api` 的长期二次开发差异，供部署、升级、代码审查和后续上游同步使用。

## 1. 快照范围

| 项目 | 值 |
|---|---|
| 官方基线 | `Wei-Shaw/sub2api` `v0.1.165` |
| 下游仓库 | `killaragorn/sub2api-pro` |
| 产品分支 | `product/main` |
| 已发布快照 | `v0.1.165-pro.4` |
| 快照提交 | `65c41d3cb15be3a33a8fe685c98f90564cd1ce50` |
| Fork 独有非合并提交 | 17 个 |
| 相对基线差异 | 157 个文件，`+11247 / -1047` |

本清单只描述 Fork 独有差异，不重复列出从官方基线继承的能力。统计范围是本文创建前的 `v0.1.165-pro.4`；本文档提交本身不计入上述功能差异。

本文不记录服务器地址、管理员密码、数据库密码、OAuth 凭据或部署平台密钥。服务器实例的临时操作记录不能代替这里的产品变更记录。

## 2. 变更摘要

sub2api-pro 当前增加或改变了以下能力：

1. OpenAI 账号按 `Priority` 顺序逐个饱和使用，并在所有旧调度策略之前生效；
2. 为历史亲和会话提供全局百分比并发预留，默认预留 `20%`；
3. 统一 HTTP、WebSocket 和其他 OpenAI 请求入口的会话 owner 与并发槽位语义；
4. Cyber Policy 命中时保存完整原始请求体，供管理员复核；
5. Prompt Audit 增加 Groq GPT-OSS Safeguard 节点，并强化结构化审计结果；
6. 修复 OpenAI WebSocket 控制关闭帧在取消竞态中丢失的问题；
7. 将内置自更新源、安装脚本和镜像来源切换到本 Fork；
8. 建立 `product/main` 产品分支、上游同步规则和 Fork 专属 Release/GHCR 发布流程。

## 3. OpenAI 优先级饱和调度

详细的行为契约、并发场景和验收矩阵见 [OpenAI 顺序饱和调度需求](OPENAI_SCHEDULING_REQUIREMENTS_CN.md)。本节记录对运维和升级最重要的最终语义。

### 3.1 账号排序与饱和规则

账号只按以下稳定顺序排列：

```text
Priority ASC, AccountID ASC
```

- `Priority` 数字越小越先使用；
- 相同 `Priority` 按账号 ID 升序稳定排序；
- 实时负载、等待数、错误率、TTFT、计费倍率和请求到达顺序不会改变基础顺序；
- 前一账号仍有当前请求类别可用槽位时，不会把普通新请求分散到后一账号；
- 前一账号普通容量满载、失效、冷却、额度不足或不适配当前请求时，才继续尝试后一账号；
- 前面的账号释放槽位后，新的无亲和请求立即回到最前面的可用账号。

主要实现：

- `backend/internal/service/openai_priority_saturation_scheduler.go`
- `backend/internal/service/openai_account_scheduler.go`
- `backend/internal/service/openai_gateway_scheduling.go`

### 3.2 与旧调度器的优先关系

新开关：

```text
openai_priority_saturation_enabled
```

运行时优先级固定为：

| Priority Saturation | Advanced Scheduler | 实际策略 |
|---|---|---|
| 开启 | 关闭 | Priority Saturation |
| 开启 | 开启 | Priority Saturation |
| 关闭 | 开启 | Weighted TopK |
| 关闭 | 关闭 | Legacy selector |

两个开关不互斥，保存一个开关时不会自动关闭另一个。只要 Priority Saturation 开启，它就具有最高优先级；关闭后才恢复已经保存的 Advanced 或 Legacy 配置。

管理端在“系统设置”页面提供独立的“OpenAI 优先级饱和调度”卡片，不再要求管理员到每个账号中重复设置该策略。

### 3.3 默认启用与迁移范围

- 全新安装默认开启 Priority Saturation；
- 从官方 `sub2api` 首次迁入 sub2api-pro 时默认开启；
- 管理端读取到缺少新字段的旧官方设置响应时，按开启处理；
- 当前只保证“全新安装”和“从官方版本迁入”这两条路径；
- 不为已经执行过旧 Pro 专属迁移的数据库追加兼容状态转换。需要验证的 Pro 实例应使用全新数据部署，而不是恢复旧 Pro 备份。

相关迁移：

- `backend/migrations/191_enable_priority_saturation_scheduler.sql`
- `backend/migrations/192_add_openai_priority_saturation_affinity_reserve_percent.sql`

## 4. 全局亲和会话预留

全局设置：

```text
openai_priority_saturation_affinity_reserve_percent
```

- 默认值：`20`；
- 合法范围：`0..99` 的整数；
- 在系统设置页统一配置；
- 对所有有限并发 OpenAI 账号生效；
- 已删除账号创建、编辑和批量编辑页面中的逐账号预留配置；
- 历史 `Account.Extra.affinity_concurrency_reserve` 只为旧数据兼容保留，调度器不再读取它。

对账号硬并发上限 `C` 和全局百分比 `P`：

```text
R = floor(C * P / 100)
G = C - R
```

- `R` 是为该账号已有亲和会话保留的容量；
- `G` 是普通新会话和从其他账号临时溢出请求可使用的容量；
- 原账号亲和请求可使用完整硬上限 `C`，不是最多只能使用 `R`；
- 普通请求不能借用空闲的 `R`；
- `C <= 0` 沿用无限并发语义，此时不计算有限预留；
- `session_hash` 和 `previous_response_id` 共享同一份亲和容量，不重复划分预留区。

该预留逻辑与账号选择策略正交。Priority Saturation、Weighted TopK、Legacy selector，以及 HTTP/WebSocket 各入口都必须使用相同的 affinity/general 分类和同一份百分比快照。

主要实现：

- `backend/internal/service/account_affinity_capacity.go`
- `backend/internal/service/openai_priority_saturation_settings.go`
- `frontend/src/views/admin/SettingsView.vue`

## 5. 会话亲和与并发原子性

为了避免多个网关实例或不同调度策略覆盖同一会话 owner，Fork 对共享 Redis 会话绑定语义做了统一加固：

- 新会话先按 general 上限成功取得账号槽位，再以原子 claim 建立 owner；
- 已有 `session_hash` 或 `previous_response_id` owner 的请求优先使用原账号；
- owner 写入使用原子 claim/CAS，不能用普通 `SET` 覆盖另一个实例已经建立的绑定；
- 原账号暂时满载时，可在满足迁移条件后临时使用其他账号；
- 临时溢出不会覆盖原 owner，下一次请求仍优先尝试原账号；
- 永久迁移只有在显式 CAS 成功后才改变 owner；
- 每次选择仍会重新校验账号状态、分组、模型、额度、隐私资格和传输能力，不能因为旧亲和记录绕过当前请求资格；
- HTTP、WebSocket、Images、Embeddings、Count Tokens 等 OpenAI 入口使用一致的选账号与绑定规则。

这些规则不是 Priority Saturation 私有行为。即使关闭新调度器，owner-aware 写入和两级槽位语义仍然保留。

## 6. 风控审计差异

### 6.1 Cyber Policy 请求证据

Cyber Policy 命中时，Fork 会把命中事件与请求证据关联保存，并在管理端风险控制详情中提供查询和展示：

- 保存请求协议、原始字节数、实际存储字节数和历史截断标记；
- 新记录保存网关收到的完整原始请求体；
- 新记录不再执行原有 64 KiB 截断；
- 新记录不对请求体做字段脱敏；
- 管理端通过专用接口读取命中记录的请求体；
- 历史版本已经截断的记录仍通过 `truncated` 字段兼容展示。

主要实现：

- `backend/internal/service/content_moderation_cyber_audit.go`
- `backend/internal/repository/content_moderation_repo.go`
- `backend/migrations/185_cyber_policy_request_audit.sql`
- `frontend/src/views/admin/RiskControlView.vue`

> **安全与隐私警告：**完整请求体可能包含用户提示词、工具调用参数、工具结果、媒体引用，以及被用户放入请求 JSON 的令牌或其他 credential。数据库、备份、管理员接口和日志访问必须按敏感数据处理，并配置最小权限、加密、保留期限和删除流程。这是 sub2api-pro 相对官方版本最重要的敏感行为差异之一。

### 6.2 OAuth 安全审查结论

在本快照的 17 个 Fork 独有提交中，没有新增专门遍历、导出或扫描已授权 OAuth 账号凭据的后台任务。OAuth 相关定制集中在账号调度、资格复核、并发兼容和测试。

这个结论只适用于上述快照，不能替代未来版本的代码审查、镜像来源验证和运行时流量审计。

## 7. Prompt Audit 扩展

Prompt Audit 在现有 Qwen3Guard OpenAI-compatible 节点之外，增加了 Groq GPT-OSS Safeguard 节点支持：

```text
默认 Base URL: https://api.groq.com/openai
默认 Model:    openai/gpt-oss-safeguard-20b
```

主要差异：

- 增加 `groq_safeguard` endpoint protocol；
- 生成适用于 GPT-OSS Safeguard 的 system policy；
- 使用结构化 JSON Schema 约束模型输出；
- 解析风险分类、动作、理由和各 scanner 结果；
- 审计记录包含 scanner backend、版本、endpoint、policy/config version 和证据元数据；
- 管理端可选择 Qwen3Guard 或 Groq Safeguard，并显示相应默认值与说明；
- 对外请求继续经过 Prompt Audit 的出站 URL 与安全校验。

主要实现：

- `backend/internal/securityaudit/prompt_gpt_oss_safeguard.go`
- `backend/internal/securityaudit/prompt_qwen3guard.go`
- `backend/internal/securityaudit/prompt_config.go`
- `frontend/src/features/prompt-audit/`

## 8. OpenAI WebSocket 关闭帧修复

`backend/internal/service/openai_ws_v2_passthrough_adapter.go` 修复了取消上下文与控制关闭帧写入之间的竞态：

- 写入开始前仍检查原上下文是否已经取消；
- 控制写入开始后保留原 deadline，但不让随后到达的 cancel 直接关闭底层 transport；
- ingress capacity lease 丢失时，客户端能收到 `1013 Try Again Later` 和重连原因，而不是只看到直接 EOF；
- 上游繁忙、连接超时等其他 1013 场景同样保留标准 WebSocket close frame。

## 9. 自更新与容器运行方式

### 9.1 Fork 自更新源

内置 Release 查询和二进制下载源已从官方仓库改为：

```text
killaragorn/sub2api-pro
```

同时做了以下配套修改：

- Redis 更新缓存键改为 `update:latest:killaragorn/sub2api-pro`，避免与官方版本缓存混用；
- 安装、卸载、Docker 部署和源码克隆文档指向本 Fork 的 `product/main`；
- OCI 镜像 source/maintainer 元数据指向本 Fork；
- 版本比较正确识别 `vX.Y.Z-pro.N`；
- 对相同官方基线，Pro 修订高于未修改的官方 `vX.Y.Z`；
- Release 容器中的 `/app/sub2api` 和相关目录归 UID/GID 1000 的 `sub2api` 用户所有，允许应用采用校验后原子替换方式完成容器内二进制更新。

更新仍只允许从 GitHub/objects.githubusercontent.com 下载，并继续执行 Release 资产和 checksum 校验。

### 9.2 容器更新边界

- `docker restart <container>` 只重启同一个容器，容器可写层中的已更新二进制会保留；
- `docker compose up -d --force-recreate`、删除容器或重新拉起容器，会恢复到 Compose 中固定的镜像版本；
- 因此验证环境应先部署一个明确的 Release 镜像 Tag，后续可通过容器内二进制自更新验证新 Release；
- 不应依赖旧 Pro 容器或旧备份作为全新验证环境的基础。

## 10. Release、镜像与仓库维护

### 10.1 发布产物

- 正式发布源为 `product/main`；
- Tag 格式为 `v<官方版本>-pro.<修订号>`；
- `-pro.N` 是稳定正式版本，不作为 alpha/beta/RC；
- GitHub Release 允许成为 Latest；
- Release workflow 构建跨平台归档、`checksums.txt` 和 amd64/arm64 容器 manifest；
- 容器镜像只发布到 `ghcr.io/killaragorn/sub2api-pro`；
- 已移除 Docker Hub 发布；
- 保留可选 simple release 路径；
- workflow 增加 Fork/仓库归属保护，避免在错误的上游仓库上下文发布下游产物；
- Release 文档链接固定到对应 Tag，避免历史版本说明随分支移动。

### 10.2 分支与上游同步

完整规则见 [sub2api-pro 分支管理规范](DOWNSTREAM_BRANCHING_CN.md)。核心约束：

- `main` 是官方纯净镜像，只允许快进同步 `upstream/main`；
- `product/main` 是唯一产品主线和发布来源；
- 功能与普通修复通过短期分支和 Squash Merge 进入 `product/main`；
- 官方稳定版本通过 `sync/v*` 分支和 Merge Commit 进入 `product/main`，保留上游祖先关系；
- 禁止对 `main`、`product/main` Force Push；
- 发布前必须确认工作区干净、CI 通过，并记录官方基线。

## 11. 测试与维护性修正

- 增加 Priority Saturation、全局预留、迁移默认值、会话 owner 原子性、混合调度器和前端设置表单测试；
- 为 Prompt Audit、Cyber 请求证据和管理端详情增加后端与前端测试；
- 系统回滚测试显式使用 15 分钟请求超时，避免长时间回滚被客户端默认超时提前终止；
- 修复调度器初版在 CI 中暴露的接口、stub 和契约检查问题。

## 12. Fork 独有提交台账

以下为 `v0.1.165..v0.1.165-pro.4` 的非合并提交，按新到旧排列：

| Commit | 变更 |
|---|---|
| `fc74d0939` | 全局亲和预留和 Priority Saturation 最终设置/UI/运行语义 |
| `e2de636b6` | 调度兼容、默认迁移、资格复核和混合实例安全加固 |
| `ffc8fcabd` | 自更新、安装文档、容器所有权和版本比较切换到 Fork |
| `39fc34981` | 修复 Priority Saturation 的 CI 契约问题 |
| `ab7dc4c47` | 初始 Priority Saturation 调度器实现 |
| `02170d1a3` | 移除 Docker Hub 发布，只保留 GHCR |
| `2f9b2eaea` | 保留 WebSocket 控制关闭帧 |
| `11f2597d2` | 将 `-pro.N` 发布为稳定正式 Release |
| `c9bae7361` | Cyber 命中保存完整请求体，不再截断或脱敏 |
| `32feb4246` | 增加 Groq GPT-OSS Safeguard Prompt Audit 节点 |
| `3cbbfc524` | 增加 Cyber Policy 请求证据存储和管理端查看能力 |
| `e2355e191` | 回滚系统测试显式使用 15 分钟请求超时 |
| `6b4726c6f` | Release 文档链接固定到发布 Tag |
| `81657bd50` | 迁移并适配官方 Release workflow |
| `956452fe1` | 增加下游 Release workflow 仓库保护 |
| `ddda3fb6a` | 发布下游 GHCR 镜像 |
| `813245125` | 建立下游分支和上游同步规范 |

## 13. 复核命令

每次同步官方版本或发布新 Pro 修订后，应更新本文的基线、快照和提交台账。可以用以下命令复核：

```bash
git fetch origin upstream --tags --prune
git log --oneline --no-merges v0.1.165..product/main
git diff --shortstat v0.1.165...product/main
git diff --name-status v0.1.165...product/main
```

还应重点复查：

1. 调度器开关优先级和默认迁移是否仍符合第 3 节；
2. 所有 OpenAI 入口是否仍使用 owner-aware 绑定与相同的亲和预留；
3. Cyber 完整请求体的权限、保留和删除策略；
4. 自更新 Release 仓库、checksum、镜像源和容器内文件所有权；
5. 新增 Fork 代码是否引入 OAuth 凭据枚举、导出或非预期外联。
