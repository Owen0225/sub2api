# Sub2API 维护分支

维护仓库：https://github.com/Owen0225/sub2api  
上游仓库：https://github.com/Wei-Shaw/sub2api

## 分支约定

- `main`：已验证的上游代码与本仓库补丁，供后续构建使用。
- `deployed/0.2.16-42b2aa11`：当前部署源码快照，提交 `42b2aa11d3cdc873e514f3da7c30503a45c747ba`，基于上游 v0.2.16 并含 Composite Messages dispatch 修复。2026-10-16 部署到 `192.168.31.254` Docker；健康检查及 `/v1/messages`、Chat Completions 冒烟测试通过。部署记录和备份位于服务器 `/mnt/data/appdata/sub2api/upgrade-0.2.16-42b2aa11/`。
- `deployed/0.2.16-9696082a`：上一部署源码快照，提交 `9696082a2c8c59626d8a742cd2f75a1ae22cf49c`，基于上游 v0.2.16 并保留 Composite 补丁。2026-10-15 部署；旧容器保留用于应用回滚，部署记录和备份位于服务器 `/mnt/data/appdata/sub2api/upgrade-0.2.16-9696082a2/`。
- `deployed/0.2.15-c85c184f`：上一部署源码快照，提交 `c85c184f389baa5aa586be32ad050da281e6ee9a`，基于上游 0.2.15；升级前通过 18 个隔离回退场景验证。
- `deployed/composite-20261008`：更早部署版本的源码快照，提交 `554b8ae72ddfa4c67babb750066695a2dcad5d6a`，基于上游 `0363b8cdba8cec3e2ba4b2dbd49c4481143fa55d`。保留所有历史部署快照。
- `maintenance/*`：同步上游或调整补丁的临时分支，通过验证后合并到 `main`。
- 本地 `origin` 指向维护仓库，`upstream` 指向原项目。保留上游历史与许可证，不强制覆盖已发布历史。

## 必须保留的 Composite 行为

原始补丁提交：`554b8ae72ddfa4c67babb750066695a2dcad5d6a`。

1. 对已识别的文本模型，优先原生平台；在同一分组内，只有明确声明相同公开模型映射的账号所属平台可以成为备用候选。
2. 原生 GLM 无可用账号或出现可重试上游错误时，依次尝试 OpenCode Go 和其他兼容候选。Ollama 可通过 Anthropic 兼容账号接入。
3. 支持 HTTP Messages、Chat Completions、Responses 及现有文本路径别名，覆盖流式和非流式请求。
4. 显式路由固定平台；未知的跨平台重名模型不猜测归属；用户额度、权限和策略错误不触发跨平台回退。
5. 客户端响应一旦提交或开始输出流式内容，不再切换。每次尝试恢复原请求并清理尝试状态，保留错误历史，最终账号负责计费。

核心回归测试位于 `backend/internal/server/routes/composite_failover_test.go`、`backend/internal/handler/composite_failover_signal_test.go` 和 `backend/internal/service/composite_route_resolver_test.go`。

## v0.2.19 同步说明

- 同步上游 `a3298b46e`，保留 Composite 原生优先、精确模型声明候选、显式路由固定及响应提交后不回退的行为。
- 前端继续使用 pnpm 9 锁定依赖安装。
- 缓存刷新回归测试改用不启动后台 worker 的夹具，避免后台刷新干扰精确调用次数断言；不修改生产逻辑。
- 包含迁移 246–248：开启存量 Gemini/Antigravity 分组生图权限、增加受限管理密钥表、增加默认关闭的分组图片按张计费开关。部署前必须备份数据库及应用配置。
- 应用回滚可回切旧镜像；迁移 246 修改了存量配置，回切镜像不会撤销此配置变更。数据库恢复需单独评估新写入数据，不自动覆盖生产数据。

## 跟随上游

Codex 当前任务中的每日维护自动化负责检查 `upstream/main`，在维护分支合并更新、解决常规冲突并保留上述行为。通过检查后将更新合并到本仓库 `main`。若上游提供等价修复，应先验证行为与回归测试，再移除重复实现。

验证至少包含后端常规与 unit 测试、Composite 竞态检查、golangci-lint，以及涉及前端时的依赖锁定安装、类型检查、关键测试与构建；同时运行上游 CI 要求的可用检查。无法运行的检查必须说明，失败时不合并。

没有上游更新或需要处理的问题时保持安静；同步完成、出现测试失败或需要用户决策时通知。自动化由 Codex 调度，需要对应本机运行环境可用。

**同步仓库不会自动部署。每次部署必须先向用户说明版本、变更、验证与回滚方案，并获得确认。** 服务器凭据、访问令牌、生产数据和私有部署信息不得写入仓库或 Actions 日志。
