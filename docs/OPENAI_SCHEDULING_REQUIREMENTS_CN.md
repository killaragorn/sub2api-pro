# OpenAI 顺序饱和调度需求

## 1. 需求结论

本需求不是 TopK，也不需要固定 K 个首选账号、三分之二阈值、迟滞状态或首选池状态。

目标行为是“按稳定顺序逐个打满”：

1. 从当前请求可用账号中的第一个开始抢并发槽位；
2. 只要第一个账号仍有当前请求类别可用的槽位，新请求就继续使用它；
3. 第一个账号的普通区达到上限后，立即尝试第二个，并为历史亲和请求保留保护槽位；
4. 第二个普通区满后尝试第三个，以此类推；
5. 前面的账号释放槽位后，下一批无粘性请求立即回到最前面的可用账号；
6. 账号额度耗尽、进入冷却、故障或不适配当前请求时，跳过该账号并继续后面的账号。
7. 已有会话优先抢原绑定账号；原账号并发满且请求可迁移时临时使用其他账号，但不覆盖原绑定。

该策略的目的：

- 用稳定、确定性的账号选择提高上游缓存命中率；
- 不把流量均匀摊到全部账号；
- 尽快消耗排序靠前账号的额度；
- 前面账号并发满时仍能使用后续账号的容量；
- 不因 TopK 截断在后续账号空闲时提前排队。

## 2. 账号顺序

第一版直接复用现有账号字段，不增加显式账号 ID 列表：

```text
Priority ASC, AccountID ASC
```

- `Priority` 数字越小，顺序越靠前；
- 相同 `Priority` 使用账号 ID 升序作为稳定 tie-break；
- 人工调整 `Priority` 后，新顺序随账号快照更新生效；
- 实时负载、等待数、错误率、TTFT、计费倍率和请求到达顺序不得改变账号的基础顺序。

如果管理员希望指定账号 101、102、103 依次使用，只需给它们配置递增且优先于其他账号的 `Priority`。

## 3. 并发和亲和预留槽位

### 3.1 两级并发上限

每个有限并发账号增加一个存放在 `Account.Extra` 中的整数配置：

```text
affinity_concurrency_reserve
```

该名称与现有按美元计量的 `window_cost_sticky_reserve` 无关。默认值为 `0`；管理员可以逐账号配置，也可以通过批量编辑给所有账号设置相同值。

对每个账号定义：

```text
C = Account.Concurrency
if C > 0:
    R = clamp(Account.AffinityConcurrencyReserve, 0, C - 1)
    G = C - R
else:
    R = 0
    G = C  // 沿用现有 <= 0 表示无限并发

C: hard_limit，账号真实硬并发上限
R: affinity_reserve，只供该账号已有亲和会话使用
G: general_limit，新会话和临时溢出请求的上限
```

`R` 是硬预留，不允许普通请求借用。否则普通请求可能在历史会话到达前一刻占满全部槽位，无法提供“专门预留”的保证。没有亲和请求时，预留槽位保持空闲，这是确定性保障的容量成本。

这里的 `R` 是“为未来亲和请求保留的并发余量”，不是“亲和请求最多只能有 R 个”。亲和请求也可能占用 `total_active < G` 的槽位，并且最多可使用整个 `C`；普通请求的准入只看当前总数是否低于 `G`。因此即使当前已有亲和请求，也仍保留 R 个额外 headroom 给随后到达的亲和请求，这是保护历史会话而不是做两类请求配额切分。

请求类别和抢槽上限固定为：

| 请求 | 对目标账号的身份 | 原子抢槽上限 |
|---|---|---|
| Redis session owner 命中原账号 | affinity | `C` |
| `previous_response_id` 命中其绑定账号 | affinity | `C` |
| 无绑定的新会话或无会话请求 | general | `G` |
| 从其他 owner 临时溢出到该账号 | general | `G` |
| CAS 永久迁移后的下一轮请求 | affinity | `C` |

新会话的首次请求必须先在 general 区成功，之后才能 claim 成为 owner；不能先创建绑定再用预留槽位绕过 `G`。临时溢出请求不是目标账号的亲和请求，也不能消耗目标账号为自身历史会话保留的 `R`。

### 3.2 原子实现

继续复用同一个账号 Redis 槽位有序集合和现有原子接口，只向脚本传入不同上限：

```go
// 已绑定到该账号的历史会话
AcquireAccountSlot(ctx, account.ID, C)

// 新会话、无会话请求、临时溢出请求
AcquireAccountSlot(ctx, account.ID, G)
```

现有脚本在一个 Lua 调用中清理过期成员、读取 `ZCARD` 并比较调用方传入的上限，因此不需要第二套计数器或跨 key 事务。所有实例使用同一个槽位集合时必须满足：

```text
total_active <= C
general 请求在 total_active >= G 时不能再进入
affinity 请求在 G <= total_active < C 时仍可进入
```

不同上限的并发调用由 Redis 串行执行。例如 `total_active=29, G=30, C=35` 时，并发到达的普通请求最多一个能把总数推进到 30，亲和请求仍可继续推进到 35，任何请求都不能超过 35。

### 3.3 示例和配置边界

账号 A、B、C 都配置：

```text
C = 35
R = 5
G = 30
```

没有历史亲和流量时：

```text
新请求 1..30  -> A
新请求 31..60 -> B
新请求 61..90 -> C
```

A 的普通区已有 30 个请求时，绑定 A 的历史会话仍可再获取 5 个槽位；第 6 个亲和请求在 A 达到 35 后，按请求可迁移性临时溢出或等待。新会话不能使用 A 的第 31..35 个槽位。

如果 `R=0`，则 `G=C`，行为退化为不预留槽位的原始顺序饱和。配置必须满足 `R >= 0`；管理 API 在 `C > 0` 时拒绝 `R >= C`，并在修改 `Concurrency` 时联合校验。运行时仍需防御性 clamp 并记录告警，避免旧数据或并发配置更新产生无普通槽位的账号。

`Concurrency <= 0` 表示不受有限槽位约束，此时预留没有意义并按 `R=0` 处理。排序第一的无限并发账号仍会持续承接流量；启用顺序饱和策略时应在管理界面和日志中给出配置告警。

不得使用 `EffectiveLoadFactor()`、`LoadRate`、等待队列长度、TopK、评分归一化或动态百分比决定 `C/R/G`。亲和预留是管理员显式配置的硬类别上限，不是随实时负载变化的软阈值。

至少记录以下观测值：

- `general_limit`、`hard_limit` 和配置的 `affinity_reserve`；
- 普通请求因达到 `G` 而跳过账号的次数；
- 亲和请求在 `total_active >= G` 时成功使用保护槽位的次数；
- 保护槽位也满后发生的临时溢出、等待和拒绝次数。

## 4. 请求资格过滤

顺序饱和只替换“候选排序与选择”，不得复制或弱化现有公共资格过滤。

候选账号仍必须满足：

- 属于目标分组和平台；
- active、schedulable；
- 未在当前请求 `ExcludedIDs` 中；
- 支持请求模型；
- 满足 endpoint、图片、compact 和 transport 能力；
- 未被 runtime block；
- 未处于代理流隔离；
- 未触发配额自动暂停；
- 影子账号母账号健康；
- 满足分组 privacy 和 channel upstream 限制。

额度耗尽或进入自动暂停的前置账号会从本次合格候选中排除，后续账号自然前移。账号恢复后按原 Priority 顺序重新参与选择。

## 5. 选择状态机

### 5.1 普通无粘性请求

```text
candidates = collectEligibleCandidates(request)
ordered = stableSort(candidates, Priority ASC, AccountID ASC)

for account in ordered:
    acquired = atomicAcquire(account.ID, generalLimit(account))
    if !acquired:
        continue

    fresh = hydrateAndRecheck(account, request)
    if fresh invalid:
        release()
        continue

    if fresh.Concurrency or fresh.AffinityConcurrencyReserve changed:
        release()
        reacquire with fresh generalLimit
        if !acquired:
            continue

    bind session when applicable
    return selected account + release function

return full-pool fallback
```

调度器不得根据缓存负载快照直接跳过顺序靠前账号后就选中后续账号。负载快照最多用于观测；是否有槽位以本次原子抢槽结果为准。这样前置账号一释放，下一请求即可立即回填，不受当前约 200ms 负载缓存影响。

### 5.2 原子失败的含义

对账号 N 的原子抢槽失败，只表示该账号本次已达到当前请求类别的上限，不能把缓存负载快照当作最终结论：

- 普通请求达到 `G`：立即继续账号 N+1，不能在 N 返回等待计划、`429` 或截断后续候选；
- owner 亲和请求达到 `C` 且可迁移：按 6.3 节扫描其他账号的 general 区；
- owner 亲和请求达到 `C` 且不可迁移：允许按 6.3 节等待 N 或返回可重试错误。

只有普通请求完整扫描所有 general 候选均失败，或可迁移亲和请求除 owner 外的完整 general 候选均失败后，才允许进入相应等待或繁忙响应。

### 5.3 fresh 复检

抢槽成功后继续执行现有缓存水合和数据库最新状态复检。复检失败必须释放槽位并继续后面的账号，不得返回已经失效的账号。

无绑定请求发生可重试上游故障时，handler 把失败账号加入 `ExcludedIDs`，重新从剩余合格账号中按稳定顺序选择。已有 session 的当前请求若允许临时跨账号 failover，同样可以排除失败账号，但必须设置 `PreserveStickyBinding=true`，不能用本次临时重试账号覆盖原绑定。

## 6. 会话亲和和缓存命中

顺序饱和只决定“没有有效绑定的新会话”落到哪个账号。已有会话先走绑定账号，与该账号当前的 `Priority` 排名无关。这样既能让新会话按账号顺序扩散，也能让后续轮次继续命中第一次请求使用的上游缓存。

### 6.1 会话身份契约

能够保证亲和的前提是每一轮都能得到同一个稳定会话标识。沿用现有识别顺序：

1. `session_id`、`conversation_id` 和现有客户端专用 session headers；
2. body 中的 `prompt_cache_key`；
3. 完整对话请求的内容派生 fallback（模型、tools、system/developer、instructions、首条 user message）。

客户端应优先传显式会话 ID 或 `prompt_cache_key`。如果客户端既不传稳定标识，也不在每轮携带可稳定派生的完整对话，服务端无法可靠判断两个无状态 HTTP 请求属于同一个会话，不得承诺账号亲和。

Redis 继续使用现有分组隔离键：

```text
sticky_session:{group_id}:openai:{session_hash} -> account_id
```

绑定采用滑动 TTL：成功命中原账号后刷新 TTL；配置值表示“最后一次成功使用后的空闲过期时间”，而不是会话创建后的绝对寿命。TTL 内修改账号 `Priority` 不迁移已有会话；TTL 过期后，该请求按新会话重新选择。

### 6.2 亲和优先级

路由优先级固定为：

```text
不可移动 previous_response_id
  > 可移动 previous_response_id 对应账号
  > session_hash 绑定账号
  > Priority ASC, AccountID ASC 的新会话扫描
```

`previous_response_id` 是上游续链约束，优先级高于普通 session 绑定：

- `PreviousResponseCanMove=false`：绑定账号有槽位时继续使用；满载时只等待该账号；账号不可用时返回明确错误，不得静默迁移；
- `PreviousResponseCanMove=true`：仍先尝试 response 对应账号；并发满载时允许临时溢出到其他账号；
- response 绑定账号与 session 绑定不一致时，以 response 绑定为准；新响应在溢出账号建立不可迁移续链状态后，用原子 CAS 把 session 绑定收敛到新的实际 owner。

### 6.3 sticky-first 状态机

```text
binding = get(session_hash)

if binding exists:
    account = validate(binding, request)
    if account is still eligible:
        if atomic_acquire(account, hard_limit(account)):
            refresh_ttl_if_owner(session_hash, account.id)
            return account

        if not request.can_temporarily_overflow:
            return bound_account_wait(account)

        overflow = scan_ordered_accounts_excluding(account.id, using=general_limit)
        if overflow acquired:
            overflow.preserve_sticky_binding = true
            return overflow

        return bound_account_wait(account)

    invalidate_or_migrate_with_cas(binding)

for account in eligible_accounts ordered by (Priority ASC, AccountID ASC):
    if not atomic_acquire(account, general_limit(account)):
        continue

    owner = claim_binding_if_absent(session_hash, account.id)
    if owner == account.id:
        return account

    release(account)
    return acquire_or_wait(owner, hard_limit(owner)) // 并发请求已经选出了唯一归属账号

return full-pool fallback
```

普通 `session_hash` 是软亲和：先原子抢原账号；原账号满载时，当前请求从稳定顺序中扫描其他账号，并设置 `PreserveStickyBinding=true`。临时溢出账号只处理本次请求，不得覆盖 Redis 中的原 owner；下一轮仍先尝试原账号。原账号释放槽位后，会话自然恢复缓存命中。

只有请求携带不可移动的上游状态时使用硬亲和。HTTP 返回原账号 `WaitPlan`，沿用现有 `StickySessionWaitTimeout` 和 `StickySessionMaxWaiting`；WebSocket 按不排队约束返回 TryAgainLater。所有可迁移候选也满载时，同样优先等待原 owner。

`can_temporarily_overflow` 必须由协议和请求语义显式给出，不能只根据是否存在 `session_hash` 猜测：

- 普通携带完整上下文的 Chat Completions、Messages，以及无上游续链状态的请求允许临时溢出；
- `PreviousResponseCanMove=true` 时允许临时溢出；
- `PreviousResponseCanMove=false`、不可迁移的 WS 连接状态或其他账号无法重建的上游状态禁止溢出；
- 无法确认是否可迁移时默认禁止。

因此：

- 新会话：A 满后选 B，B 满后选 C；
- 已绑定 B 且 B 有槽位：即使 A 空闲也继续选择 B；
- 已绑定 B、B 满载且请求可迁移：临时选择稳定顺序中第一个有槽位的其他账号，Redis owner 仍为 B；
- 已绑定 B、B 满载且请求不可迁移：等待 B 或返回可重试错误；
- B 暂时满载不是永久迁移条件。

这同时保留“优先命中原账号”和“原账号满时仍利用全池容量”。临时溢出的这一轮可能失去上游缓存命中，这是容量可用性与缓存亲和无法同时满足时的明确取舍。

### 6.4 原子绑定和并发首轮

普通 `GET` 后 `SET` 会在同一会话的并发首轮跨过账号并发边界时产生漂移：一个请求可能抢到 A，另一个抢到 B，最后写入者随机决定后续归属。必须给现有 `GatewayCache` 增加单键原子操作：

这是共享 OpenAI session key 的全局一致性契约，不是 `priority_saturation` 私有实现。`weighted_topk`、关闭高级调度器时的旧选择器、HTTP/WS handler 的等待后绑定，以及所有 `OpenAIGatewayService.BindStickySession` 调用都必须走同一 owner-aware 路径。当前 `setStickySessionAccountID` 对权威 key 执行普通 `SET` 的行为必须删除；任何策略都不得直接覆盖已经存在的 owner。

```go
ClaimSessionAccount(ctx, groupID, sessionHash, accountID, ttl) (ownerID int64, claimed bool, err error)
CompareAndSwapSessionAccount(ctx, groupID, sessionHash, oldID, newID, ttl) (bool, error)
RefreshSessionTTLIfOwner(ctx, groupID, sessionHash, accountID, ttl) (bool, error)
DeleteSessionAccountIfOwner(ctx, groupID, sessionHash, accountID) (bool, error)
```

`ownerID` 和 CAS 结果是路由决策的一部分，调用方不得忽略。账号已经抢槽但 claim 返回另一个 owner 时，调用方必须释放错误账号槽位，并在转发上游之前重新获取或等待 owner；不能只把普通 `SET` 替换为 `SET NX` 后仍继续使用自己选中的账号。scheduler 已完成 claim 后，handler 的重复绑定应删除或降为 `RefreshSessionTTLIfOwner`。

Redis 使用单键 Lua 或等价的 `SET NX`/compare-and-swap 实现，多实例共享同一结果，不增加分布式锁。账号槽位键与 session 键不要求跨键事务：

1. 先原子抢候选账号槽位；
2. 再 claim session；
3. claim 失败且 owner 是另一个账号时，立即释放刚抢到的候选槽位；
4. 转而获取或等待 owner 账号。

这样不会超卖槽位，也不会让同一会话在并发首轮永久分裂。当前格式 session key 是唯一权威值；旧 SHA-256 key 兼容读取命中时，应先原子 claim 到当前格式 key，再参与选择。

已有 owner 的临时溢出是唯一不执行 claim 的选择路径；它必须携带 `PreserveStickyBinding=true`，并只以 `RefreshSessionTTLIfOwner` 维持原 owner。若溢出响应建立了后续不可迁移的 response 状态，必须在响应映射持久化后以 CAS 把 session owner 转移到该响应账号。

### 6.5 迁移和故障

只有绑定账号确实不能继续服务当前会话时才迁移，例如账号被删除、禁用、移出分组、凭证失效、额度耗尽，或不再满足当前模型、capability、transport 和协议要求。迁移必须：

1. 选中新账号并成功获取槽位；
2. 以旧账号 ID 为 expected value 执行 CAS；
3. CAS 成功后使用新账号；
4. CAS 失败时释放新账号槽位，并服从 Redis 中新的 owner。

并发满载、等待人数已满、单次请求超时和一次可重试上游错误均不得直接永久改写绑定。请求允许迁移时可以临时跨账号处理，但必须设置 `PreserveStickyBinding=true`；下一轮仍先尝试原账号。`priority_saturation` 不使用基于 TTFT 或错误率的旧 `sticky escape`，只使用由原子并发满载和 `can_temporarily_overflow` 共同触发的确定性亲和溢出。

### 6.6 分配示例

假设 A、B、C 的并发均为 35：

```text
并发到达的新会话 S01..S35 -> A，并建立各自到 A 的绑定
S36..S70                -> B
A 后续释放一个槽位：
  新会话 S71            -> A
  已绑定 B 且 B 有槽位的 S40 -> B
B 满载时 S40 再次请求：
  可迁移请求             -> 临时使用 A 或 C，Redis owner 仍为 B
  不可迁移请求           -> 等待 B 或返回可重试错误
B 释放槽位后 S40 再次请求 -> B
```

这里“账号 1 打满后才使用账号 2”约束新会话的首次分配，也约束可迁移历史会话的临时溢出扫描。真实流量中，已绑定到后续账号且原账号有槽位的会话继续使用后续账号，这是维持上游缓存命中的必要例外。

`Account.Concurrency` 只限制正在执行的请求，不是活跃会话配额。如果 100 个新会话完全串行创建，每次请求结束后 A 都重新有槽位，那么 100 个会话都可能绑定 A；之后这些会话同时回来时，可迁移请求会临时溢出到 B/C，不可迁移请求仍会等待 A。若还要求“每个账号最多绑定 35 个活跃会话”，必须另建带空闲 TTL 的 session reservation 计数，不能复用请求并发计数。

新策略不使用 `sticky weighted` 分数。粘性是选择前置规则，不参与动态 TopK 打分。

## 7. 全池满载和等待

第一版不引入首选池状态、扩散状态或迟滞；但必须为已有 session 绑定补充单键 CAS，以保证多实例和并发首轮只有一个归属账号。

已有有效绑定的请求先抢原账号；可迁移请求在原账号满载后扫描其余账号，不可迁移请求才只等待原账号。

`WaitPlan.MaxConcurrency` 必须与请求身份一致：等待原 owner 使用 `C`；无绑定请求和临时溢出目标使用 `G`。不得在 handler 等待阶段统一改回 `Account.Concurrency`，否则会让普通请求偷用保护槽位。

为无绑定请求创建等待计划前，必须至少完成一次完整稳定顺序的 general 原子抢槽扫描；存在亲和 owner 的可迁移请求还必须扫描 owner 之外的 general 候选。所有相应类别的候选上限均已达到时：

- 普通 HTTP 请求可以沿用现有 fallback 超时和等待上限；
- 等待目标优先选择顺序最靠前的有效账号，以便槽位释放后优先回填前置账号；
- WebSocket 保持现有不排队语义，快速复检失败后关闭为 TryAgainLater；
- Count Tokens 必须真正获取槽位后才能转发，不能忽略 `WaitPlan`。

当前单账号 `WaitPlan` 在等待期间不会切换到另一个先释放的账号。这不影响“选择时前满后溢”的核心行为，但可能降低全池满载后的恢复效率。池级等待属于可独立后续优化，不作为第一版顺序饱和策略的前置条件。

## 8. 与当前实验调度器及管理 UI 的关系

### 8.1 独立开关模型

不得复用或扩展现有 `openai_advanced_scheduler_enabled`。该开关继续只控制当前 `weighted_topk` 高级调度，已有语义、默认值和配置项保持不变。

顺序饱和增加全新的独立布尔开关：

```text
openai_priority_saturation_enabled
```

配置状态固定为：

| 现有 advanced switch | 新 priority saturation switch | 实际行为 |
|---|---|---|
| `false` | `false` | 现有 legacy selector |
| `true` | `false` | 当前 `weighted_topk` |
| `false` | `true` | `priority_saturation` |
| `true` | `true` | 非法配置 |

后端设置更新必须基于更新后的完整有效值校验两个开关互斥，不能只校验本次 payload 中出现的字段。新字段缺失时默认为 `false`，因此升级不会改变现有 advanced scheduler 行为。

正常配置不得同时开启两个开关。为防手工改数据库、旧管理端或滚动升级产生脏状态，运行时检测到二者同时为 `true` 时必须记录高优先级错误并确定性选择 `priority_saturation`，不能在请求之间随机分派；管理 API 和新 UI 仍阻止保存该状态。

### 8.2 独立的 `SettingsView` 配置卡

当前 `SettingsView.vue` 的“OpenAI 实验调度策略”卡片保持原样，继续包含现有 advanced scheduler 开关、TopK、权值、sticky weighted、subscription priority 和 OAuth 调度倍率。

在其旁边增加新的独立卡片：

```text
OpenAI 顺序饱和调度
  [开关] openai_priority_saturation_enabled
```

新卡片不得把现有 advanced toggle 改名、替换成 Select 或复用其 `v-model`。新开关关闭时只显示功能说明；开启时显示：

```text
账号顺序：Priority ASC, Account ID ASC
新会话/临时溢出：使用 general_limit (G)
原账号亲和请求：使用 hard_limit (C)
保护槽位：逐账号配置 affinity_concurrency_reserve (R)
```

互斥交互必须明确：

- 开启新开关时若现有 advanced switch 已开启，弹出确认说明“将同时关闭 TopK 加权调度”；确认后在同一表单状态中设为 `advanced=false, priority_saturation=true`；
- 开启现有 advanced switch 时若新开关已开启，执行对称确认并设为 `priority_saturation=false, advanced=true`；
- 取消确认时恢复触发前的开关状态；
- 两个变更必须在一次 Settings 保存请求中提交，不能出现先关闭一个、网络失败后未开启另一个的中间自动保存；
- 开关变化不得绕过现有“保存设置”和 step-up 流程。

关闭或切换开关不得清空 TopK、权值或账号预留配置；重新启用原策略时原值继续存在。

顺序是分组相关的，新卡片不能展示一个误导性的全局账号列表。应提供“前往账号管理”入口；账号管理页在当前分组过滤下按 `Priority, ID` 展示实际顺序。

### 8.3 账号级保护槽位 UI

`affinity_concurrency_reserve` 是账号容量属性，不是全局调度权值。必须在 OpenAI-compatible 账号的以下位置增加配置：

- `CreateAccountModal.vue`：放在“并发数”旁边；
- `EditAccountModal.vue`：放在“并发数”旁边；
- `BulkEditAccountModal.vue`：使用现有“启用本字段批量修改”的 checkbox 模式；
- 账号列表容量展示：显示当前值以及 `G/C`，例如 `28 / 30 / 35`，tooltip 解释 30 是普通上限、35 是硬上限。

表单只编辑 `C` 和 `R`，`G=C-R` 必须只读计算，不能再增加第三个可独立修改的字段。示例预览：

```text
总并发上限 C        35
亲和预留 R           5
普通请求上限 G      30
```

交互要求：

- 默认 `R=0`；
- 输入必须为整数且 `0 <= R < C`；
- C 或 R 改变时实时更新 G；
- `R>=C` 时阻止提交并显示字段级错误，不能静默 clamp；
- `C<=0` 时禁用 R 并显示“无限并发账号不使用保护槽位”；
- 明确提示“硬预留可能在没有亲和流量时保持空闲”；
- 明确区分并发槽位字段与现有美元额度字段 `window_cost_sticky_reserve`；
- 平台切换为不适用类型时不把隐藏字段意外写成 0 或删除已有值。

亲和预留与选择策略正交。只要账号 `R>0`，`weighted_topk`、`priority_saturation`、旧选择器及全部 HTTP/WS 入口都必须遵守相同的 affinity/general 分类；UI 不得暗示切回 `weighted_topk` 会自动关闭预留。管理员若要恢复全部普通容量，应显式把 R 改为 0。

### 8.4 UI 状态和提示

- Settings API 类型增加独立布尔字段 `openai_priority_saturation_enabled`，表单默认值为 `false`；
- Account 类型通过现有 `extra` 写入 `affinity_concurrency_reserve`，列表 DTO 同时暴露归一化后的只读值；
- 新开关开启时显示非阻断告警：`R=0` 的账号没有亲和保护，`Concurrency<=0` 的前置账号不会自然扩散；
- 两个调度开关、C、R、G 的名称和帮助文本必须覆盖项目现有语言，并在移动端不横向溢出；
- UI 只负责互斥交互、提前校验和解释；后端必须重复执行开关互斥及 `0 <= R < C` 校验。

### 8.5 策略语义

`openai_priority_saturation_enabled=true` 时：

- 不使用 `LBTopK` 截断；
- 不计算动态评分决定账号顺序；
- 不做 TopK 内加权随机；
- `sticky weighted` 不生效；
- 基于 TTFT 或错误率的旧 `sticky escape` 不生效；仅保留“原账号原子抢槽失败且请求可迁移”触发的确定性亲和溢出；
- `subscription priority` 不改变全局 Priority 顺序；
- 评分权值保留在数据库中，切回 `weighted_topk` 后继续生效。

实现不新增业务表、Ent schema 或池级 Redis 状态；账号级预留值存入现有 `Account.Extra`，并复用现有账号并发集合。owner-aware 写入和两级槽位上限必须对所有 OpenAI 调度策略同时生效，不能放在策略分支内部。

## 9. 实现边界

主要新代码放入独立文件，降低以后同步上游时的冲突：

```text
backend/internal/service/openai_priority_saturation_scheduler.go
backend/internal/service/openai_priority_saturation_scheduler_test.go
```

现有文件只保留必要接入点：

| 文件 | 修改 |
|---|---|
| `openai_account_scheduler.go` | 读取独立开关并在候选过滤后分派一次；脏状态下 priority saturation 优先 |
| settings service/DTO | 增加独立布尔开关、默认值、完整状态互斥校验和脏状态告警 |
| `frontend/src/api/admin/settings.ts` | 增加 `openai_priority_saturation_enabled` 读写字段 |
| `SettingsView.vue` | 保留现有 advanced 卡片；增加独立顺序饱和卡片、开关及双向互斥确认 |
| Settings UI tests | 验证两个独立开关、取消确认、单次保存、旧配置兼容和字段保值 |
| `frontend/src/types/index.ts` | 暴露账号预留值和 create/update extra 类型约束 |
| Create/Edit/Bulk account modals | 在并发数旁配置 R，实时预览 G，并联合校验 C/R |
| `AccountCapacityCell.vue` | 展示 current/general/hard 和保护槽位 tooltip |
| account management list/view | 在分组过滤下按 `Priority, ID` 预览实际顺序，并提供 R/G/C 容量信息 |
| 共享 affinity reserve utility | 统一计算 C/R/G 和前端校验，避免三个 modal 各自实现不同规则 |
| i18n 和前端组件测试 | 增加策略、保护槽位、容量成本、错误提示和切换保值用例 |
| `openai_gateway_count_tokens.go` | 未获取账号槽位时禁止直接转发 |
| `gateway_service.go` / `repository/gateway_cache.go` | 为现有 session 绑定增加原子 claim、CAS、按 owner 刷新和删除 |
| `openai_sticky_compat.go` | 取消权威 key 的普通 `SET`，兼容旧 hash 时先 claim 当前格式 key |
| OpenAI scheduler/handler 的全部绑定调用点 | 统一消费 owner 结果；claim 失败时释放错误槽位并收敛到 owner，删除 post-select 覆盖写 |
| `account.go` / account DTO | 读写并校验 `affinity_concurrency_reserve`，并与美元额度预留明确区分 |
| `repository/scheduler_cache.go` | 把 `affinity_concurrency_reserve` 纳入账号调度快照 |
| OpenAI 的全部账号槽位获取和 WaitPlan 调用点 | 按 owner 身份统一选择 `C` 或 `G`，禁止普通请求直接使用 `Account.Concurrency` |

新文件应复用现有：

- `listSchedulableAccounts`；
- `isAccountRequestCompatibleReason`；
- transport/capability 检查；
- `resolveFreshSchedulableOpenAIAccount`；
- `recheckSelectedOpenAIAccountFromDB`；
- `tryAcquireAccountSlot`；
- sticky cache 和 previous-response state store。

不得复制一份模型、配额、代理、privacy 或 capability 过滤逻辑。

## 10. 验收测试

### 10.1 顺序和并发

构造账号 A、B、C，Priority 分别为 10、20、30，并发上限均为 2，预留均为 0：

1. 连续请求且不释放，选择结果必须为 `A, A, B, B, C, C`；
2. A 释放一个槽位后，下一无粘性请求必须立即选择 A；
3. A 满、B 有槽位时不得返回 A 的 `WaitPlan`；
4. A/B 满、C 有槽位时必须选择 C；
5. A/B/C 全满后才允许返回等待计划。

### 10.2 原子竞态

构造 3 个账号、每个并发 35、预留为 0，同时启动 100 个 goroutine：

- A 最多获取 35 个槽位；
- B 只在 A 满后获取，最多 35 个；
- C 获取剩余 30 个；
- 不得超卖任何账号；
- 不得因负载快照同时看到 A=34 而让多个请求越过上限。

### 10.3 亲和保护槽位

构造 A、B、C，均为 `C=35, R=5, G=30`：

- 100 个无绑定并发请求只能在 A/B/C 各获取 30 个，剩余 10 个进入 general 等待或繁忙路径；
- A 的 `total_active=30` 时，新会话必须跳过 A，但 owner=A 的历史会话还能连续获取 5 个槽位；
- A 达到 35 后，第 6 个可迁移亲和请求临时溢出，第 6 个不可迁移亲和请求等待 A；
- 从 A 临时溢出到 B 的请求按 B 的 `G=30` 获取，不能消耗 B 的保护槽位；
- owner 的 `WaitPlan.MaxConcurrency=35`，无绑定和临时溢出的 `WaitPlan.MaxConcurrency=30`；
- `total_active=29` 时并发执行普通和亲和抢槽，不得让普通请求把总数推进到 30 以上，也不得让总数超过 35；
- `R=0` 时保持 10.1 和 10.2 的原行为；
- `R<0`、`R>=C`、降低 C 后 R 失效以及 `C<=0` 的配置校验和运行时防御行为均有测试。

### 10.4 稳定顺序

- 修改负载率、等待数、错误率 EWMA、TTFT 和计费倍率，不改变基础顺序；
- 相同 Priority 按 ID 升序；
- 修改 Priority 后顺序按新值稳定变化；
- `Concurrency <= 0` 的前置账号产生明确告警。

### 10.5 资格和故障

- A 不支持模型：直接从 B 开始；
- A 额度自动暂停：直接从 B 开始；
- A 代理隔离或 runtime block：直接从 B 开始；
- A 抢槽后数据库复检失效：释放 A 槽位并选择 B；
- A 上游失败加入 `ExcludedIDs`：当前请求 failover 到 B；
- A 恢复后，下一无粘性请求重新优先选择 A。

### 10.6 粘性

- session 已绑定 B 且 B 有槽位：即使 A 空闲也必须继续选择 B；
- session 已绑定 B、B 满载且请求可迁移：选择第一个有槽位的其他账号，并保持 Redis owner 为 B；
- 临时溢出完成后 B 释放槽位：下一轮必须再次选择 B；
- session 已绑定 B、B 满载且请求不可迁移：只返回 B 的 `WaitPlan`，不得扫描 A 或 C；
- 不可迁移请求等待超时或等待队列已满：返回可重试错误，Redis owner 仍为 B；
- B 被禁用或移出分组：CAS 迁移到顺序中第一个可用账号，后续请求稳定命中新账号；
- 同一新 session 在 A 的最后一个槽位附近并发到达：所有请求最终服从同一个原子 owner，claim 失败方释放错误账号槽位；
- 临时溢出路径不得 claim 溢出账号，也不得被 handler 的 post-select 绑定覆盖；
- 溢出响应建立不可迁移 response 状态：先持久化 response 映射，再 CAS 转移 session owner；
- Priority 调整不迁移 TTL 内的已有绑定；
- 每次成功命中或临时溢出只在 Redis value 仍等于原账号时刷新滑动 TTL；
- 当前请求临时 failover 时保留原绑定，下一轮仍优先原账号；
- 不可移动 previous response 绑定 B：不得切换到 A；
- response 绑定与 session 绑定冲突：response 绑定胜出，session 通过 CAS 收敛；
- 不提供稳定 session 信号且无法稳定派生内容的请求，不承诺跨轮账号亲和。

### 10.7 混合策略一致性

使用同一个 Redis、同一个 group 和同一个 session，同时运行一个 `weighted_topk` 实例和一个 `priority_saturation` 实例：

- 在 A 最后一个槽位附近并发选择，让两个策略分别产生 A/B 候选，最终只能有一个 session owner；
- claim 失败实例必须释放错误账号槽位，并在转发前改为获取或等待 owner；
- `weighted_topk` 在 `priority_saturation` 已 claim 后不得用普通 `SET` 覆盖 owner，反向顺序也相同；
- handler 等待完成后的重复绑定不得覆盖并发迁移出的新 owner；
- owner 迁移与旧请求刷新 TTL 并发时，旧请求不得延长或恢复旧 owner；
- 测试必须覆盖两个服务实例而不是只在单 scheduler 对象内并发。

- 配置 `C=35, R=5` 时，两种策略的普通请求都不得在账号总并发达到 30 后继续获取；任何旧路径以 35 获取普通请求都必须使测试失败。

### 10.8 管理 UI

- 旧设置缺少新字段时，`openai_priority_saturation_enabled=false`，现有 advanced scheduler 行为不变；
- 两个开关都关闭时使用 legacy selector；
- 仅现有 advanced 开关开启时使用 `weighted_topk`，其 TopK/权值 UI 保持原样；
- 仅新开关开启时使用 `priority_saturation`，独立卡片显示顺序、C/R/G、溢出语义和账号管理入口；
- UI 开启任一开关时对另一个执行带确认的关闭；取消确认恢复原状态；
- 一次保存同时提交两个开关，后端拒绝二者同时为 true；运行时脏状态确定性选择 priority saturation 并记录错误；
- 反复启停两个开关不会清空或重置 TopK、权值和 R；
- Create/Edit 表单实时显示 G，并拒绝小数、负数及 `R>=C`；
- Bulk Edit 只有启用预留字段时才更新 R，未勾选时不触碰已有账号值；
- `C<=0`、平台切换、后端返回旧数据和并发修改 C/R 均不会产生无普通槽位的静默配置；
- 账号容量展示正确区分 `current / G / C`，移动端无横向溢出；
- Settings 和 account payload round-trip 后值不丢失，后端拒绝非法枚举和非法 C/R。

### 10.9 回归和入口

- `affinity_concurrency_reserve=0` 时，`weighted_topk` 下现有 scheduler tests 保持不变；
- Responses、Chat Completions、Messages、Images、Embeddings、Alpha Search 和 Count Tokens 均验证“前满后溢”；
- Count Tokens 未获取槽位时不得调用上游；
- WebSocket 验证快速抢槽和 TryAgainLater 行为；
- 各入口验证 owner 请求使用 `C`，无绑定和临时溢出请求使用 `G`；
- 后端运行 `go test -p 2 ./...`；
- 前端 lint 和 production build 串行运行。

## 11. 发布和回滚

共享 sticky key 和账号并发集合时，禁止直接把旧实例与 owner/reserve-aware 新实例混合 canary。安全发布必须分两步：

1. 先发布兼容版本：实现原子 owner 接口和两级槽位 helper，让 `weighted_topk`、旧选择器及所有 OpenAI handler 都按请求身份使用 `C/G`；新的 `openai_priority_saturation_enabled` 保持 `false`，所有账号的 `affinity_concurrency_reserve` 暂时保持 `0`；
2. 把兼容版本部署到所有实例，确认集群中不存在普通 `SET` owner 或始终以 `Account.Concurrency` 获取普通请求的旧实例；
3. 通过 10.7 节混合策略并发测试，并观察 claim 冲突、保护槽位命中、general 拒绝、错误槽位释放和 CAS 失败指标；
4. 为目标账号配置小规模 `affinity_concurrency_reserve`，验证历史会话可以使用保护区且普通容量下降符合预期；
5. 确认现有 `openai_advanced_scheduler_enabled=false` 后，才在 canary 实例或低峰窗口开启独立的 `openai_priority_saturation_enabled`；
6. 观察各账号普通区、保护区、临时溢出、sticky 等待、上游错误和额度下降速度；
7. 确认顺序及会话亲和均符合预期后扩大范围。

如果无法先升级所有 writer，只能让 canary 使用独立 Redis key namespace，并按稳定 session hash 把整个会话固定到同一实例池；不能让两种写入语义共享 key。

策略回滚只关闭 `openai_priority_saturation_enabled`；需要回到 TopK 时在同一次设置更新中开启 `openai_advanced_scheduler_enabled`，否则回到 legacy selector。owner-aware 写入和两级槽位 helper 必须保留。需要恢复全部普通容量时可把 `affinity_concurrency_reserve` 调回 `0`；不得把运行中实例回滚到会普通 `SET` owner 或让普通请求直接使用 `C` 的旧二进制。
