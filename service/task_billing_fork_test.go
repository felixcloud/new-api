package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
)

// yunai fork: 倍率计费路径下, 成功任务完成时把提交时的估算事实与完成时的实际事实合并, 写进带 task_settled 的结算日志
func TestSettle_ForkMergesUsageFactsIntoSettlementLog(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 70, 70, 70
	const initQuota, preConsumed = 10000, 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-fork-facts", 8000)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatusSuccess
	task.PrivateData.BillingContext.PerCallBilling = true
	task.PrivateData.BillingContext.UsageFacts = map[string]any{"tokens": float64(9000), "resolution": "720p", "video_input": "video"}

	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, UsageFacts: map[string]any{"tokens": float64(50638), "resolution": "480p"}}
	settled := settleTaskBillingOnComplete(ctx, &mockAdaptor{}, task, taskResult)

	assert.False(t, settled)
	assert.Equal(t, initQuota, getUserQuota(t, userID), "按次计费不做差额结算")
	assert.Equal(t, int64(1), countLogs(t))
	log := getLastLog(t)
	assert.Equal(t, 0, log.Quota)
	assert.Contains(t, log.Other, `"task_settled":true`)
	assert.Contains(t, log.Other, `"tokens":50638`, "实际 Token 覆盖估算值")
	assert.Contains(t, log.Other, `"resolution":"480p"`, "实际分辨率覆盖请求值")
	assert.Contains(t, log.Other, `"video_input":"video"`, "提交时才知道的事实保留")
}

// 失败任务不写结算日志, 仍走原来的全额退款
func TestSettle_ForkFailureKeepsRefundOnly(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 71, 71, 71
	seedUser(t, userID, 10000)
	seedToken(t, tokenID, userID, "sk-fork-fail", 8000)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, 5000, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatusFailure
	task.PrivateData.BillingContext.UsageFacts = map[string]any{"tokens": float64(9000)}

	settled := settleTaskBillingOnComplete(ctx, &mockAdaptor{}, task, &relaycommon.TaskInfo{Status: model.TaskStatusFailure})
	assert.False(t, settled)
	assert.Equal(t, int64(0), countLogs(t), "失败任务由调用方 RefundTaskQuota 退款, 结算阶段不写日志")
}
