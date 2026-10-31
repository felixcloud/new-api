package controller

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// yunai fork: 可灵 4.0 由内置 kling 插件提供, 绑在类型 61「任务插件」渠道上 (task_plugin_key=kling)。
// 客户走可灵原路径 /text-to-video/kling-4.0, 或用平台模型编码 kling-video-v4 走宿主协议并由渠道模型映射到 kling-4.0,
// 两条路都应打到可灵原地址, 不带 /kling 前缀, 并按单个 API Key 发 Bearer。
func TestKling4OnTaskPluginChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousMemoryCache := common.MemoryCacheEnabled
	previousBatchUpdate := common.BatchUpdateEnabled
	previousLogConsume := common.LogConsumeEnabled
	previousRedisEnabled := common.RedisEnabled
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Channel{}, &model.Task{}, &model.Log{}))
	model.DB = database
	model.LOG_DB = database
	common.MemoryCacheEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = false
	common.RedisEnabled = false
	previousModelRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"kling-4.0":1,"kling-video-v4":1}`))
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.MemoryCacheEnabled = previousMemoryCache
		common.BatchUpdateEnabled = previousBatchUpdate
		common.LogConsumeEnabled = previousLogConsume
		common.RedisEnabled = previousRedisEnabled
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousModelRatios))
	})
	require.NoError(t, database.Create(&model.User{Id: 7, Username: "kling4-user", Group: "SSVIP", Quota: 100_000_000}).Error)

	var submitCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/text-to-video/kling-4.0" {
			http.NotFound(w, r)
			return
		}
		submitCalls.Add(1)
		assert.Equal(t, "Bearer kling-api-key", r.Header.Get("Authorization"))
		body, readErr := io.ReadAll(r.Body)
		assert.NoError(t, readErr)
		assert.Contains(t, string(body), `"prompt":"a lighthouse"`)
		_, _ = io.WriteString(w, `{"code":0,"message":"SUCCEED","data":{"id":"kling4-private","status":"submitted"}}`)
	}))
	defer upstream.Close()

	channel := model.Channel{
		Type:    constant.ChannelTypeTaskPlugin,
		Name:    "可灵-V4",
		Key:     "kling-api-key",
		BaseURL: &upstream.URL,
		Status:  common.ChannelStatusEnabled,
		Models:  "kling-4.0,kling-video-v4",
		Group:   "SSVIP",
	}
	modelMapping := `{"kling-video-v4":"kling-4.0"}`
	channel.ModelMapping = &modelMapping
	setting := channel.GetSetting()
	setting.TaskPluginKey = "kling"
	channel.SetSetting(setting)
	require.NoError(t, database.Create(&channel).Error)

	generation := pluginruntime.DefaultRegistry.Generation()
	require.NotNil(t, generation)

	newContext := func(t *testing.T, path, body string) (*gin.Context, *httptest.ResponseRecorder) {
		t.Helper()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		common.SetContextKey(c, constant.ContextKeyUserId, 7)
		common.SetContextKey(c, constant.ContextKeyUserGroup, "SSVIP")
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "SSVIP")
		common.SetContextKey(c, constant.ContextKeyTokenGroup, "SSVIP")
		common.SetContextKey(c, constant.ContextKeyUserQuota, 100_000_000)
		return c, recorder
	}
	submit := func(t *testing.T, c *gin.Context, originModel, publicID string) model.Task {
		t.Helper()
		relayInfo := &relaycommon.RelayInfo{
			UserId:          7,
			UserGroup:       "SSVIP",
			UsingGroup:      "SSVIP",
			UserQuota:       100_000_000,
			TokenGroup:      "SSVIP",
			OriginModelName: originModel,
			Billing:         &nativeRouteBilling{userID: 7},
			TaskRelayInfo: &relaycommon.TaskRelayInfo{
				Action:        c.GetString("task_action"),
				PublicTaskID:  publicID,
				LockedChannel: &channel,
			},
		}
		outcome, taskErr := executeTaskSubmissionWith(c, relayInfo, relay.RelayTaskSubmit)
		require.Nil(t, taskErr)
		require.NotNil(t, outcome)
		var persisted model.Task
		require.NoError(t, database.Where("task_id = ?", publicID).First(&persisted).Error)
		assert.Equal(t, constant.TaskPlatform("kling"), persisted.Platform)
		assert.Equal(t, channel.Id, persisted.ChannelId)
		assert.Equal(t, "kling4-private", persisted.PrivateData.UpstreamTaskID)
		return persisted
	}

	t.Run("vendor path", func(t *testing.T) {
		binding, found := generation.LookupDeclaredRoute(http.MethodPost, "/text-to-video/kling-4.0")
		require.True(t, found)
		require.Equal(t, "kling", binding.Plugin.Meta.Key)
		c, recorder := newContext(t, "/text-to-video/kling-4.0", `{"prompt":"a lighthouse","settings":{"resolution":"720p","duration":5}}`)
		c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Generation: generation, Plugin: binding.Plugin, Route: binding.Route})

		middleware.PrepareTaskPluginRoute()(c)
		require.False(t, c.IsAborted(), recorder.Body.String())
		require.Equal(t, "kling-4.0", c.GetString("resolved_task_model"))
		require.Nil(t, middleware.SetupContextForSelectedChannel(c, &channel, "kling-4.0"))
		assert.Equal(t, "kling", c.GetString("task_plugin_key"))

		persisted := submit(t, c, "kling-4.0", "task_kling4_vendor")
		assert.Equal(t, "kling-4.0", persisted.Properties.UpstreamModelName)
	})

	t.Run("platform model through the host protocol", func(t *testing.T) {
		c, recorder := newContext(t, "/v1/video/generations", `{"model":"kling-video-v4","prompt":"a lighthouse","seconds":"5","size":"1280x720"}`)
		c.Set("platform", string(constant.TaskPlatform("kling")))
		require.Nil(t, middleware.SetupContextForSelectedChannel(c, &channel, "kling-video-v4"), recorder.Body.String())

		persisted := submit(t, c, "kling-video-v4", "task_kling4_platform")
		assert.Equal(t, "kling-video-v4", persisted.Properties.OriginModelName)
		assert.Equal(t, "kling-4.0", persisted.Properties.UpstreamModelName)
	})

	assert.Equal(t, int32(2), submitCalls.Load())
}
