package service

// yunai fork: 外部业务系统按任务日志里的计费事实与 task_settled 结算日志计价。
// 这里集中放 fork 对任务计费日志的补充, 上游文件里只留一行调用, 方便同步上游时解决冲突。

import (
	"context"
	"maps"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// forkAnnotateTaskSubmitLog 补充任务提交日志: 倍率计费路径下写插件给出的计费事实;
// 异步任务的提交日志只是预扣, 标 task_pending, 最终用量看完成时那条 task_settled 的结算日志。
func forkAnnotateTaskSubmitLog(c *gin.Context, info *relaycommon.RelayInfo, task *model.Task, other *model.LogOther) {
	if info.TieredBillingSnapshot == nil && len(info.TaskUsageFacts) > 0 {
		other.SetPublic("usage_facts", info.TaskUsageFacts)
	}
	if !taskDeliveredInline(c, task) {
		other.SetPublic("task_pending", true)
	}
}

// forkAnnotateTaskBillingOther 让倍率计费路径的差额/退款日志也带上计费上下文里的计费事实。
func forkAnnotateTaskBillingOther(task *model.Task, other *model.LogOther) {
	bc := task.PrivateData.BillingContext
	if bc == nil || bc.TieredSnapshot != nil || len(bc.UsageFacts) == 0 {
		return
	}
	other.SetPublic("usage_facts", bc.UsageFacts)
}

// forkRecordZeroDeltaSettlementLog 在差额为 0 时也写一条 0 额度的结算日志, 外部业务系统以结算日志计价。
// 字段与 RecalculateTaskQuota 末尾的结算日志保持一致。
func forkRecordZeroDeltaSettlementLog(task *model.Task, actualQuota int, reason *common.Message, clamps ...*common.QuotaClamp) {
	other := taskBillingOther(task)
	other.SetPublic("task_id", task.TaskID)
	other.SetPublic("pre_consumed_quota", task.Quota)
	other.SetPublic("actual_quota", actualQuota)
	other.SetPublic("task_settled", true)
	for _, clamp := range clamps {
		attachQuotaSaturationToOther(other, clamp)
	}
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   model.LogTypeConsume,
		Content:   []*common.Message{reason},
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     0,
		TokenId:   task.PrivateData.TokenId,
		Group:     task.Group,
		Other:     other,
		NodeName:  task.PrivateData.NodeName,
	})
}

// forkSettleTaskBillingOnComplete 包住上游的 settleTaskBillingOnComplete:
//   - 倍率计费路径把完成时的实际事实(实际 Token、分辨率 …)合并进计费上下文, 结算日志据此写 usage_facts;
//   - 成功任务没走差额结算(按次计费 / 上游没回 Token / 没配倍率)时补一条 0 额度的结算日志。
func forkSettleTaskBillingOnComplete(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, taskResult *relaycommon.TaskInfo) bool {
	if bc := task.PrivateData.BillingContext; bc != nil && bc.TieredSnapshot == nil && task.Status == model.TaskStatusSuccess && taskResult != nil && len(taskResult.UsageFacts) > 0 {
		merged := make(map[string]any, len(bc.UsageFacts)+len(taskResult.UsageFacts))
		maps.Copy(merged, bc.UsageFacts)
		maps.Copy(merged, taskResult.UsageFacts)
		bc.UsageFacts = merged
	}
	settled := settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)
	if !settled && task.Status == model.TaskStatusSuccess {
		RecalculateTaskQuota(ctx, task, task.Quota, common.NewMessage("Task completion settlement"))
	}
	return settled
}
