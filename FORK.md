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
| 任务请求校验放行未声明时长键的 `-1`（方舟 Seedance 智能时长） | `relay/channel/task/jsplugin/adaptor.go` 中的 `autoDurationSentinel` | `validateResolvedUsageValue` | `relay/channel/task/jsplugin/adaptor_test.go` |
| Doubao 插件：顶层 `duration` 也作为时长 | `plugins/tasks/doubao/plugin.js` | `buildSubmitRequest` | `plugins/doubao_responses_test.go` |
| 阿里百炼语音转文字（qwen-audio-3.0-asr-flash、fun-asr-flash、qwen3-asr-flash） | `relay/channel/ali/audio.go`、`relay/channel/ali/constants.go`、`relay/helper/audio_request_fork.go` | `relay/channel/ali/adaptor.go`（URL / 请求头 / 请求转换 / 响应处理）、`relay/helper/valid_request.go` 中的 `forkUnmarshalAudioRequest`、`relaykit/dto/audio.go` 的 `Stream` 字段 | `relay/channel/ali/audio_test.go`、`relay/helper/audio_request_test.go` |
| Kling Omni 视频插件 `kling-omni`：可灵 3.0 Omni（`kling-v3-omni`）与可灵 O1（`kling-video-o1`），走旧版格式的 `/v1/videos/omni-video`；支持单个 API Key（Bearer）与 `accessKey\|secretKey`；绑定在类型 61「任务插件」渠道上，默认 Base URL `https://api-beijing.klingai.com`；O1 只有带参考图、参考视频或主体时才能用 5 秒和 10 秒以外的时长（可灵错误码 1201） | `plugins/tasks/kling-omni/plugin.js` | 无（新插件）；`plugins/builtin_plugins_test.go` 的 `expectedKeys` 加了 `kling-omni` | `plugins/kling_omni_fork_test.go`；上游若新增同名路径 `/kling-omni/...` 或同名模型，确认不冲突；单价表随可灵官方价格页更新 |
| `VERSION` 文件 | `VERSION` | — | 同步上游 release 后更新 |
