# yunai fork 改动清单

本仓库 fork 自 [QuantumNous/new-api](https://github.com/QuantumNous/new-api)。这份清单列出 fork 相对上游的全部行为差异，
每次同步上游后按「回归检查」逐项确认。

## 同步上游流程

```bash
git fetch upstream
git switch -c sync-upstream-$(date +%Y%m%d) main
git merge upstream/main          # 只 merge，不 rebase / cherry-pick 上游提交
go build ./... && go test ./service/ ./setting/ratio_setting/ ./relay/... ./controller/ ./middleware/ ./router/ ./plugins/...
git switch main && git merge --no-ff sync-upstream-$(date +%Y%m%d)
```

查看 fork 相对上游的全部差异：`git diff upstream/main...main --stat`

## 改动原则

- fork 逻辑放在新文件里（`*_fork.go`、`ext_*.go`、`ext-router.go`），上游文件里只加一行调用，并在行尾标 `// yunai fork`。
- 给上游结构体加字段时加在末尾，不要插在中间（避免 gofmt 重排相邻字段）。
- fork 自己的测试放在 `*_fork_test.go`；修改了上游测试的期望值时，在改动处注释 `yunai fork`。
- 提交前运行 `gofmt -l`。

## 行为差异

| 功能 | fork 代码 | 上游文件中的挂钩 | 回归检查 |
|---|---|---|---|
| `/api/ext` 外部业务系统管理接口（HMAC 签名 + RootAuth，按 key 建令牌、按游标拉日志、渠道摘要） | `controller/ext_api.go`、`middleware/ext_signature.go`、`model/ext_query.go`、`router/ext-router.go`、`middleware/access_token_routes_fork.go` | `router/api-router.go` 中的 `setExtRouter(apiRouter)` | `controller/ext_api_test.go`；上游调整访问令牌范围后，确认 `access_token_routes_fork.go` 中的范围名仍然存在 |
| 补全倍率：配置值优先于上游锁定的内置值，且不标锁定 | `setting/ratio_setting/completion_ratio_fork.go` | `ResolveCompletionRatio` 开头的 `forkResolveCompletionRatio` | `completion_ratio_fork_test.go`；`controller/model_management_test.go` 中 `gpt-4o-2024-05-13` 一行的期望值是 fork 改的 |
| 任务日志带计费事实：倍率计费路径写 `usage_facts`，异步任务提交日志标 `task_pending`，结算日志标 `task_settled`，差额为 0 和未走差额结算的成功任务也补写结算日志 | `service/task_billing_fork.go` | `service/task_billing.go` 3 处、`service/task_polling.go` 中的 `finalizeTerminalTask`、`relay/relay_task.go` 中的 `TaskUsageFactsProvider`、`model/task.go` 的 `UsageFacts`、`relay/common/relay_info.go` 末尾的 `TaskUsageFacts`、`controller/relay.go` 的 `UsageFacts` | `task_billing_fork_test.go`；`task_billing_test.go` 中的 `TestRecalculate_ZeroDelta` 是 fork 改的 |
| Doubao 插件：顶层 `duration` 也作为时长 | `plugins/tasks/doubao/plugin.js` | `buildSubmitRequest` | `plugins/doubao_responses_test.go` |
| Sora 插件（`1.1.2`）：兼容网关在 `/v1/videos/{id}` 返回 `metadata.url` 直链且缺少 `usage` 时，直接采用 `metadata.url` 并回查 `/v1/video/generations/{id}` 补齐 `usage.completion_tokens`、`resolution`、`duration`、`generate_audio`；结算与预占输出 `tokens`、`resolution`、`video_input`、`seconds`、`generate_audio`，使类型 1（OpenAI 兼容）渠道转发 Seedance 等视频模型时能按实际产出视频 Token 精算 | `plugins/tasks/sora/plugin.js` | `extractUsage`、`parseTaskResult`、`extractUsageOnComplete` | `plugins/sora_responses_test.go` |
| 阿里百炼语音转文字（qwen-audio-3.0-asr-flash、fun-asr-flash、qwen3-asr-flash） | `relay/channel/ali/audio.go`、`relay/channel/ali/constants.go`、`relay/helper/audio_request_fork.go` | `relay/channel/ali/adaptor.go`（URL / 请求头 / 请求转换 / 响应处理）、`relay/helper/valid_request.go` 中的 `forkUnmarshalAudioRequest`、`relaykit/dto/audio.go` 的 `Stream` 字段 | `relay/channel/ali/audio_test.go`、`relay/helper/audio_request_test.go` |
| Kling Omni 视频插件 `kling-omni`：可灵 3.0 Omni（`kling-v3-omni`）与可灵 O1（`kling-video-o1`），走旧版格式的 `/v1/videos/omni-video`；支持单个 API Key（Bearer）与 `accessKey\|secretKey`；绑定在类型 61「任务插件」渠道上，默认 Base URL `https://api-beijing.klingai.com`；O1 只有带参考图、参考视频或主体时才能用 5 秒和 10 秒以外的时长（可灵错误码 1201） | `plugins/tasks/kling-omni/plugin.js` | 无（新插件）；`plugins/builtin_plugins_test.go` 的 `expectedKeys` 加了 `kling-omni` | `plugins/kling_omni_fork_test.go`；上游若新增同名路径 `/kling-omni/...` 或同名模型，确认不冲突；单价表随可灵官方价格页更新 |
| 可灵 4.0（上游内置 `kling` 插件 1.2.0 的 `kling-4.0`，可灵原路径 `/text-to-video/kling-4.0`、`/image-to-video/kling-4.0`、`/omni-video/kling-4.0`、`GET /tasks?task_ids=`）在类型 61「任务插件」渠道上服务：渠道 `task_plugin_key=kling`，平台模型编码 `kling-video-v4` 经渠道模型映射到 `kling-4.0` | 无 fork 代码，只有测试 | 无 | `controller/plugin_kling4_fork_test.go`；类型 61 渠道只能绑一个插件，可灵 4.0 与 `kling-omni` 要分两条渠道；上游改了 `kling` 插件的模型名或路径时同步改平台目录与渠道映射 |
| 渠道的分组级调度策略：`channel_group_policies(channel_id, group, priority, weight)`，同一条渠道在不同分组可以有不同的优先级 / 权重（业务系统按分组配渠道顺序，不再复制渠道）；abilities 按分组取策略值；内存缓存选路在候选有策略时按分组分层、同层按权重精确比例抽签（不做上游的平滑放大）；没有策略时完全走上游逻辑；按标签批量改优先级 / 权重不覆盖策略；`PUT /api/ext/channel/group-policy` 整组替换，`/channels/digest` 加 `group_policy_sha256`，`/version` 加 `memory_cache_enabled`，契约版本 3 | `model/channel_group_policy_fork.go`、`controller/ext_api.go`、`router/ext-router.go`、`middleware/access_token_routes_fork.go` | `model/ability.go` 的 `AddAbilities` / `UpdateAbilities`（`forkApplyGroupPolicies`）与 `UpdateAbilityByTag`（`forkReapplyGroupPoliciesByTag`）；`model/channel_cache.go` 的 `InitChannelCache`（`forkLoadGroupPolicyCache`）与 `GetRandomSatisfiedChannel`（`forkSelectByGroupPolicy`）；`model/channel.go` 的 `Delete`；`model/main.go` 迁移 | `model/channel_group_policy_fork_test.go`、`controller/ext_api_test.go` 的 `TestExtChannelGroupPolicy`；上游改了选路（`GetRandomSatisfiedChannel` 的分层 / 重试语义）或 abilities 构造时，确认挂钩位置仍在、语义仍一致 |
| `/v0/*` Responses 流式无 `event:` 头兼容：入口将 `/v0/*` 透明改写为 `/v1/*`（同时支持 `X-No-SSE-Event: 1` 请求头、`?no_event=1` / `?sse_event=0` 查询参数），Responses 流式响应省略 `event: <type>` 行只发 `data: <json>`；`/v1/*` 默认行为不变 | `relay/helper/sse_compat_fork.go` | `main.go` 的 `ForkWrapV0CompatHandler(server)`、`relay/helper/common.go` 的 `ResponseChunkData`、`controller/plugin_protocol.go` 的 `writeTaskPluginProtocolEvent` | `relay/helper/sse_compat_fork_test.go` |
| Realtime WebSocket 无请求体与非 WebSocket 渠道防护：`doRequest` 关闭 `req.Body` / `c.Request.Body` 前判空，避免 `GET /v1/realtime` 等无 body 请求落到 HTTP 渠道时 nil panic；`WssHelper` 对非 `*websocket.Conn` 返回值做安全类型断言并关闭 `*http.Response.Body`，返回 `invalid_api_type` 错误而非 panic | `relay/websocket_fork.go` | `relay/channel/api_request.go` 的 `doRequest`、`relay/websocket.go` 的 `WssHelper` | `relay/websocket_fork_test.go` |
| 视频请求 `asset://` 渠道亲和过滤：双渠道并存时（火山官方 + `zchmy.top` 第三方兼容），根据请求体中的 `asset://om-a-*` 或 `asset://asset-*` 自动附加 `FilterAssetProvider` 过滤条件，确保携带 `asset://` 的视频生成请求精准落到创建该人像资产的上游渠道 | `middleware/task_asset_affinity_fork.go`、`model/channel_asset_affinity_fork.go` | `dto/channel_constraints.go`、`model/channel_constraint.go`、`middleware/distributor.go`、`middleware/task_plugin.go` | `middleware/task_asset_affinity_fork_test.go` |
| `VERSION` 文件 | `VERSION` | — | 同步上游 release 后更新 |
