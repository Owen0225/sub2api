# Sub2API 维护分支

维护仓库：https://github.com/Owen0225/sub2api  
上游仓库：https://github.com/Wei-Shaw/sub2api

## 分支约定

- `main`：已验证的上游代码与本仓库补丁，供后续构建使用。
- `deployed/0.2.23-06370606f`：当前部署源码快照，提交 `06370606f`（上游 v0.2.23 同步合并，见下方「v0.2.23 同步说明」；含 0.2.22-spark-cancel-20261117a 的断连取消默认开启语义）。2026-11-19 部署到 `192.168.31.254` Docker（compose `--no-deps` 仅重建 sub2api 服务），镜像 `sub2api:0.2.23-06370606f`（`Sub2API 0.2.23 commit: docker built: 2026-11-19T06:50:12Z`）；容器 healthy、`/health` 返回 ok、迁移 249–252 应用成功、流式/非流式 Chat Completions 冒烟 200，1 秒断开请求在 1000ms 完成响应（取消生效）。部署前数据库、环境文件与 Compose 备份位于服务器 `/mnt/data/appdata/sub2api/upgrade-0.2.23-06370606f/`；旧镜像 `0.2.22-spark-cancel-20261117a` 保留供回滚。构建注记：沿用 `--network=host` + `NPM_CONFIG_REGISTRY=https://registry.npmmirror.com`、`GOPROXY=https://mirrors.aliyun.com/goproxy/,direct`；前端构建 OOM（vite build JS heap 1.5GB 耗尽，exit 134），通过在 Dockerfile 前端构建步骤加 `NODE_OPTIONS=--max-old-space-size=6144` 解决（服务器上就地 sed 修改，本地仓库 Dockerfile 未改）。
- `deployed/0.2.22-spark-cancel-20261117a`：上一部署源码快照，提交 `e4bf73e82`（含上游 v0.2.22 同步合并 `48394c6b9`）。将客户端断连即取消改为**默认开启**：`cancelCCUpstreamOnClientDisconnect` 缺省 true，账号可用 `extra.cancel_upstream_on_client_disconnect=false` 退出以恢复"断开排水计费"语义；覆盖范围扩展至 `/v1/responses`（raw CC 回退路径，此前仅 Messages 与 raw Chat Completions）。取消请求按零 token 入账。2026-11-17 部署到 `192.168.31.254` Docker（compose `--no-deps` 仅重建 sub2api 服务），镜像 `sub2api:0.2.22-spark-cancel-20261117a`；健康检查通过。端到端验证：8000 token 流式请求 1 秒断开，sub2api 完成延迟由 95378ms 降至 144ms，引擎 Running ≤2s 回落、KV 归零。部署前数据库、环境文件与 Compose 备份位于服务器 `/mnt/data/appdata/sub2api/upgrade-0.2.22-spark-cancel-20261117a/`；旧镜像 `0.2.19-spark-cancel-20261103b` 保留供回滚。构建注记：docker 默认 bridge 网络内 goproxy.cn 拨号超时（宿主同地址正常，Go 代理回退不响应网络错误），改用 `--network=host` 构建并通过；前端 `NPM_CONFIG_REGISTRY=https://registry.npmmirror.com`、Go `GOPROXY=https://mirrors.aliyun.com/goproxy/,direct`。
- `deployed/0.2.19-spark-cancel-20261103b`：历史部署源码快照，提交 `a5bde7eaa`，同步上游 `4a4a83ee5` 并为本地 Spark 账号的 Messages 与 Chat Completions 流启用客户端断连即取消。2026-11-03 部署到 `192.168.31.254` Docker，镜像 `sub2api:0.2.19-spark-cancel-20261103b`；健康检查通过。账号 59、61 的 `extra.cancel_upstream_on_client_disconnect` 为 true；取消请求按零 token 入账。部署前数据库、环境文件与 Compose 备份位于服务器 `/mnt/data/appdata/sub2api/upgrade-spark-cancel-20261103/`；旧镜像保留供应用回滚。
- `deployed/0.2.16-42b2aa11`：历史部署源码快照，提交 `42b2aa11d3cdc873e514f3da7c30503a45c747ba`，基于上游 v0.2.16 并含 Composite Messages dispatch 修复。2026-10-16 部署到 `192.168.31.254` Docker；健康检查及 `/v1/messages`、Chat Completions 冒烟测试通过。部署记录和备份位于服务器 `/mnt/data/appdata/sub2api/upgrade-0.2.16-42b2aa11/`。
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

## v0.2.23 同步说明

- 基线 `e4bf73e8`（0.2.22-spark-cancel-20261117a）同步上游 `2af3f1e15`（v0.2.23，含 v0.2.20–v0.2.23 四个版本），保留 Composite 原生优先、精确模型声明候选、显式路由固定及响应提交后不回退的行为；客户端断连取消补丁（默认开启、`extra.cancel_upstream_on_client_disconnect=false` 退出、覆盖 `/v1/responses`）语义不变，上游仅重叠修改了同文件的 GPT-6.2 temperature/topP 处理。
- 上游 `2d20707f1` 重写了分组 Ultra Fast 说明文案并删除了 `service_tier=ultrafast` 字样，但未更新 `groupsOpenAIFast.spec.ts` 的断言；本地补丁 `7ea8fde33` 放宽该断言使其与文案一致，上游修复后应移除本地改动。
- 包含迁移 249–252：usage_logs api_key cost 索引、MCP OAuth、OpenAI cache write TTL、分组强制 Ultra Fast 门控、usage rollup 非阻塞重建、渠道 Ultra Fast 计价系数。部署前必须备份数据库及应用配置；迁移 249 为非事务索引创建。
- 后端全量测试（不带 -race，全绿）、Composite 三大回归、取消语义测试、golangci-lint、前端锁定安装、类型检查、全量 vitest（3147 用例）与构建均通过。上游 `internal/service` 包在 `-race` 下存在既有数据竞争（测试 stub 与 `gin.SetMode` 并发调用，纯净 upstream/main 同样复现，105 个用例受影响），非本次合并引入。

## 跟随上游

Codex 当前任务中的每日维护自动化负责检查 `upstream/main`，在维护分支合并更新、解决常规冲突并保留上述行为。通过检查后将更新合并到本仓库 `main`。若上游提供等价修复，应先验证行为与回归测试，再移除重复实现。

验证至少包含后端常规与 unit 测试、Composite 竞态检查、golangci-lint，以及涉及前端时的依赖锁定安装、类型检查、关键测试与构建；同时运行上游 CI 要求的可用检查。无法运行的检查必须说明，失败时不合并。

没有上游更新或需要处理的问题时保持安静；同步完成、出现测试失败或需要用户决策时通知。自动化由 Codex 调度，需要对应本机运行环境可用。

**同步仓库不会自动部署。每次部署必须先向用户说明版本、变更、验证与回滚方案，并获得确认。** 服务器凭据、访问令牌、生产数据和私有部署信息不得写入仓库或 Actions 日志。
