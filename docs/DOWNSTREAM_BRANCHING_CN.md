# sub2api-pro 分支管理规范

本文档定义 `sub2api-pro` 在持续跟踪官方 `Wei-Shaw/sub2api` 的同时进行二次开发时使用的分支、合并和发布规则。

## 1. 仓库与远端

| 名称 | 地址 | 用途 |
|------|------|------|
| `origin` | `https://github.com/killaragorn/sub2api-pro.git` | 二次开发仓库 |
| `upstream` | `https://github.com/Wei-Shaw/sub2api.git` | 官方上游仓库 |

如果从新的工作目录克隆，应补充官方远端：

```bash
git clone https://github.com/killaragorn/sub2api-pro.git
cd sub2api-pro
git remote add upstream https://github.com/Wei-Shaw/sub2api.git
git fetch --all --tags --prune
```

## 2. 分支模型

项目采用两个长期分支和若干短期分支：

```text
upstream/main
      ↓ 快进同步
main                    官方纯净镜像
      ↓ 合并官方 Tag
product/main            二开稳定主线
      ├─ feature/*
      ├─ fix/*
      ├─ hotfix/*
      └─ sync/v*
```

### 2.1 `main`

`main` 是官方代码的纯净镜像。

- 不提交二开功能。
- 不接受普通功能或修复 PR。
- 只允许从 `upstream/main` 快进同步。
- 不允许 Squash、Rebase、Force Push 或手工改写历史。
- 不从该分支构建 `sub2api-pro` 生产镜像。

同步命令：

```bash
git fetch upstream --tags --prune
git switch main
git merge --ff-only upstream/main
git push origin main
```

### 2.2 `product/main`

`product/main` 是二开稳定主线，也是功能 PR 的默认目标分支。

- 所有二开功能最终合入该分支。
- 生产镜像和正式版本 Tag 只能从该分支产生。
- 禁止直接 Push 和 Force Push。
- 已共享或已发布的历史不得 Rebase。
- 上游同步必须通过 `sync/v*` PR 合入。

首次创建：

```bash
git switch main
git pull --ff-only origin main
git switch -c product/main
git push -u origin product/main
```

在 GitHub 中应将 `product/main` 设置为 Fork 仓库的默认分支。

## 3. 短期分支

| 分支模式 | 来源 | 目标 | 用途 | 合并方式 |
|----------|------|------|------|----------|
| `feature/*` | `product/main` | `product/main` | 二开功能 | Squash Merge |
| `fix/*` | `product/main` | `product/main` | 普通缺陷修复 | Squash Merge |
| `hotfix/*` | 当前生产 Tag 或 `product/main` | `product/main` | 生产紧急修复 | Squash Merge |
| `sync/v*` | `product/main` | `product/main` | 合并官方版本 | Merge Commit |

分支名称使用小写英文和连字符，例如：

```text
feature/custom-branding
feature/organization-billing
fix/login-callback
hotfix/database-timeout
sync/v0.1.161
```

短期分支合并后应删除本地和远程分支。

## 4. 功能开发流程

从最新 `product/main` 创建功能分支：

```bash
git switch product/main
git pull --ff-only origin product/main
git switch -c feature/custom-branding
```

开发过程中可以让尚未共享的功能分支跟进产品主线：

```bash
git fetch origin
git rebase origin/product/main
```

推送并创建 PR：

```bash
git push -u origin feature/custom-branding
```

功能、普通修复和紧急修复 PR 使用 **Squash Merge**，使每项二开能力在 `product/main` 中尽量保持为一个独立提交。

## 5. 上游同步流程

### 5.1 更新纯净镜像

先将 Fork 的 `main` 快进到官方最新状态：

```bash
git fetch upstream --tags --prune
git switch main
git merge --ff-only upstream/main
git push origin main
```

### 5.2 合并官方稳定 Tag

生产版本优先同步官方发布 Tag，不直接自动追踪移动中的 `upstream/main`。

```bash
git fetch upstream --tags --prune
git switch product/main
git pull --ff-only origin product/main
git switch -c sync/v0.1.161
git merge --no-ff v0.1.161 \
  -m "chore(upstream): merge sub2api v0.1.161"
```

解决冲突、完成测试后推送并创建目标为 `product/main` 的 PR：

```bash
git push -u origin sync/v0.1.161
```

`sync/v*` PR 必须使用 **Create a merge commit**，不能使用 Squash Merge。保留上游提交祖先关系可以避免后续同步重复产生已经处理过的差异。

### 5.3 同步未发布提交

只有需要紧急安全修复、解决阻断生产的官方缺陷，或依赖已确认稳定但尚未发布的功能时，才允许同步上游 `main` 的特定 Commit。

此时必须记录准确的上游 Commit SHA，并仍然通过独立 `sync/*` PR 集成和测试。

## 6. 冲突处理

启用 Git 冲突记忆，减少重复解决同类上游冲突：

```bash
git config rerere.enabled true
git config rerere.autoupdate true
```

处理同步冲突时遵循以下原则：

1. 先理解上游修改目的，不直接保留二开版本覆盖上游。
2. 检查上游是否已经实现等价功能；如已实现，删除重复二开补丁。
3. 不在同步 PR 中顺带开发新功能。
4. 将必要的二开适配作为独立提交放在上游 Merge Commit 之后。
5. 同步完成后执行完整后端、前端和镜像构建检查。

## 7. 合并策略

| 场景 | 策略 |
|------|------|
| 功能和普通修复 PR | Squash Merge |
| 上游 `sync/v*` PR | Create a merge commit |
| `main` 同步官方 | `git merge --ff-only upstream/main` |
| 未共享的短期分支整理 | 允许 Rebase |
| `main`、`product/main` | 禁止 Rebase 和 Force Push |

不要对 `sync/v*` 使用 Squash Merge，否则会丢失与官方提交的祖先关系。

## 8. 发布与版本 Tag

正式 Tag 只能从最新的 `product/main` 创建，格式为：

```text
v<上游版本>-pro.<二开修订号>
```

示例：

```text
v0.1.161-pro.1
v0.1.161-pro.2
v0.1.162-pro.1
```

当上游版本发生变化时，二开修订号重新从 `pro.1` 开始。

`-pro.N` 表示二开正式生产修订号，不是 alpha、beta 或 RC。虽然 SemVer 会把连字符后的
`pro.N` 解析为预发布标识，但 GitHub Release 必须发布为正式版本并可成为 Latest。
因此 `.goreleaser.yaml` 和 `.goreleaser.simple.yaml` 必须设置
`release.prerelease: false`，不能使用 `auto`。

```bash
git switch product/main
git pull --ff-only origin product/main
git tag -a v0.1.161-pro.1 -m "sub2api-pro v0.1.161-pro.1"
git push origin v0.1.161-pro.1
```

发布前必须确认工作目录干净、CI 通过，并记录该版本基于的官方 Tag 或 Commit SHA。

## 9. GitHub 分支保护

### `main`

- 禁止删除和 Force Push。
- 限制直接 Push。
- 只允许用于上游快进同步。
- 不将普通 PR 的目标设置为该分支。

### `product/main`

- 设置为默认分支。
- 要求通过 PR 合并。
- 要求 CI 状态检查通过。
- 禁止删除和 Force Push。
- 建议至少一次审核后合并。
- 功能 PR 默认 Squash Merge；同步 PR 使用 Merge Commit。

## 10. 差异检查

```bash
# 查看所有二开提交
git log main..product/main --oneline

# 查看相对官方镜像的完整代码差异
git diff main...product/main

# 查看尚未合入产品主线的功能分支差异
git diff product/main...feature/custom-branding
```

## 11. 不采用长期 `develop` 分支

当前阶段不建立长期 `develop` 分支，避免上游更新和二开提交在多个长期分支间重复流转。

只有在多人长期并行开发、存在固定集成测试环境，并且多个功能必须组合验证时，才考虑增加 `develop`。增加后仍需保留 `main` 作为官方纯净镜像，并保持 `product/main` 为唯一发布来源。

