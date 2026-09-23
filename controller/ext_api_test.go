package controller

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	sqlmysql "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const extTestSecret = "ext-test-secret"

// extTestDialects 是三库矩阵：SQLite 总是跑，MySQL / PostgreSQL 由环境变量给出 DSN 时才跑。
var extTestDialects = []struct{ kind, env string }{{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"}}

// extTestDB 用真实数据库走 model.InitDB 初始化 model.DB / LOG_DB（含 group / key
// 列名的方言初始化），非 SQLite 时先删表再建表，保证每次都是干净数据。
func extTestDB(t *testing.T, kind, dsn string) {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousSQLite := common.IsMasterNode, common.SQLitePath
	previousRedis, previousMemory := common.RedisEnabled, common.MemoryCacheEnabled
	previousOptions, previousQuotaForNewUser := common.OptionMap, common.QuotaForNewUser
	common.IsMasterNode = false
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	common.OptionMap = map[string]string{}
	common.QuotaForNewUser = 0
	switch kind {
	case "sqlite":
		common.SQLitePath = t.TempDir() + "/ext.db"
		dsn = "local"
	case "mysql":
		config, err := sqlmysql.ParseDSN(dsn)
		require.NoError(t, err)
		host, _, err := net.SplitHostPort(config.Addr)
		require.NoError(t, err)
		require.True(t, net.ParseIP(host).IsLoopback(), "database tests only permit loopback instances")
	default:
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback(), "database tests only permit loopback instances")
	}
	t.Setenv("SQL_DSN", dsn)
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitDB())
	model.LOG_DB = model.DB
	tables := []any{&model.User{}, &model.UserSession{}, &model.Token{}, &model.Log{}, &model.Option{}}
	if kind != "sqlite" {
		require.NoError(t, model.DB.Migrator().DropTable(tables...))
	}
	require.NoError(t, model.DB.AutoMigrate(tables...))
	var version string
	query := "SELECT version()"
	if kind == "sqlite" {
		query = "SELECT sqlite_version()"
	}
	require.NoError(t, model.DB.Raw(query).Scan(&version).Error)
	t.Logf("database version: %s", version)
	t.Cleanup(func() {
		connection, err := model.DB.DB()
		if err == nil {
			require.NoError(t, connection.Close())
		}
		common.OptionMap, common.QuotaForNewUser = previousOptions, previousQuotaForNewUser
		common.IsMasterNode, common.SQLitePath = previousMaster, previousSQLite
		common.RedisEnabled, common.MemoryCacheEnabled = previousRedis, previousMemory
		common.SetDatabaseTypes(previousMain, previousLog)
		model.DB, model.LOG_DB = previousDB, previousLogDB
	})
}

// extTestRouter 只挂签名中间件，RootAuth 的行为由既有鉴权测试覆盖。
func extTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/ext", middleware.ExtSignature())
	group.GET("/version", ExtVersion)
	group.PUT("/user", ExtUpsertUser)
	group.GET("/users", ExtGetUsers)
	group.PUT("/token", ExtUpsertToken)
	group.DELETE("/token", ExtDeleteToken)
	group.GET("/logs", ExtListLogs)
	group.GET("/logs/stats", ExtLogStats)
	return router
}

type extResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// extSignedRequest 按契约计算 X-Maas-Ts / X-Maas-Sign 后发起请求，ts 可偏移以模拟过期。
func extSignedRequest(t *testing.T, router *gin.Engine, secret string, tsOffset int64, method, target string, body any) (*httptest.ResponseRecorder, extResponse) {
	t.Helper()
	var request *http.Request
	encoded := []byte{}
	if body != nil {
		var err error
		encoded, err = common.Marshal(body)
		require.NoError(t, err)
		request = httptest.NewRequest(method, target, bytes.NewReader(encoded))
		request.Header.Set("Content-Type", "application/json")
	} else {
		request = httptest.NewRequest(method, target, nil)
	}
	ts := strconv.FormatInt(time.Now().Unix()+tsOffset, 10)
	bodyHash := sha256.Sum256(encoded)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(method + "\n" + request.URL.Path + "\n" + ts + "\n" + hex.EncodeToString(bodyHash[:])))
	request.Header.Set("X-Maas-Ts", ts)
	request.Header.Set("X-Maas-Sign", hex.EncodeToString(mac.Sum(nil)))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	var response extResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response), recorder.Body.String())
	return recorder, response
}

func TestExtSignatureGateAndVersion(t *testing.T) {
	extTestDB(t, "sqlite", "")
	router := extTestRouter()

	t.Setenv("MAAS_EXT_SECRET", "")
	recorder, response := extSignedRequest(t, router, extTestSecret, 0, http.MethodGet, "/api/ext/version", nil)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.False(t, response.Success)

	t.Setenv("MAAS_EXT_SECRET", extTestSecret)
	recorder, _ = extSignedRequest(t, router, "wrong-secret", 0, http.MethodGet, "/api/ext/version", nil)
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	recorder, _ = extSignedRequest(t, router, extTestSecret, -600, http.MethodGet, "/api/ext/version", nil)
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)

	recorder, response = extSignedRequest(t, router, extTestSecret, 0, http.MethodGet, "/api/ext/version", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, response.Success, response.Message)
	var version struct {
		Version    string `json:"version"`
		ExtVersion string `json:"ext_version"`
		InstanceId string `json:"instance_id"`
	}
	require.NoError(t, common.Unmarshal(response.Data, &version))
	assert.Equal(t, common.Version, version.Version)
	assert.Equal(t, "1", version.ExtVersion)
	assert.Len(t, version.InstanceId, 32)

	// 标识已落库，且清空内存后再次调用仍返回同一个值。
	var option model.Option
	require.NoError(t, model.DB.Where(&model.Option{Key: model.MaasExtInstanceIdOptionKey}).First(&option).Error)
	assert.Equal(t, version.InstanceId, option.Value)
	common.OptionMap = map[string]string{}
	_, response = extSignedRequest(t, router, extTestSecret, 0, http.MethodGet, "/api/ext/version", nil)
	var again struct {
		InstanceId string `json:"instance_id"`
	}
	require.NoError(t, common.Unmarshal(response.Data, &again))
	assert.Equal(t, version.InstanceId, again.InstanceId)
}

type extUserPayload struct {
	Id          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Group       string `json:"group"`
	Status      int    `json:"status"`
	Quota       int    `json:"quota"`
	UsedQuota   int    `json:"used_quota"`
}

func TestExtUserAndTokenUpsert(t *testing.T) {
	for _, dialect := range extTestDialects {
		t.Run(dialect.kind, func(t *testing.T) {
			if dialect.env != "" && os.Getenv(dialect.env) == "" {
				t.Skip("set " + dialect.env + " to run this database")
			}
			extTestDB(t, dialect.kind, os.Getenv(dialect.env))
			t.Setenv("MAAS_EXT_SECRET", extTestSecret)
			router := extTestRouter()

			// 用户 upsert 幂等：两次同名调用得到同一个 id，第二次更新字段与绝对额度。
			_, response := extSignedRequest(t, router, extTestSecret, 0, http.MethodPut, "/api/ext/user",
				gin.H{"username": "t_123456", "display_name": "客户甲", "group": "default", "status": 1, "quota": 123456789})
			require.True(t, response.Success, response.Message)
			var created extUserPayload
			require.NoError(t, common.Unmarshal(response.Data, &created))
			assert.Equal(t, "t_123456", created.Username)
			assert.Equal(t, "客户甲", created.DisplayName)
			assert.Equal(t, 123456789, created.Quota)
			assert.Equal(t, common.UserStatusEnabled, created.Status)

			_, response = extSignedRequest(t, router, extTestSecret, 0, http.MethodPut, "/api/ext/user",
				gin.H{"username": "t_123456", "display_name": "客户乙", "group": "vip", "status": 2, "quota": 42})
			require.True(t, response.Success, response.Message)
			var updated extUserPayload
			require.NoError(t, common.Unmarshal(response.Data, &updated))
			assert.Equal(t, created.Id, updated.Id)
			assert.Equal(t, "客户乙", updated.DisplayName)
			assert.Equal(t, "vip", updated.Group)
			assert.Equal(t, common.UserStatusDisabled, updated.Status)
			assert.Equal(t, 42, updated.Quota)
			stored, err := model.GetUserById(created.Id, true)
			require.NoError(t, err)
			assert.Equal(t, common.RoleCommonUser, stored.Role)
			assert.NotEmpty(t, stored.Password)

			recorder, _ := extSignedRequest(t, router, extTestSecret, 0, http.MethodPut, "/api/ext/user",
				gin.H{"username": "t_123456", "status": 1, "quota": common.MaxWalletQuota + 1})
			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			recorder, _ = extSignedRequest(t, router, extTestSecret, 0, http.MethodPut, "/api/ext/user",
				gin.H{"username": "t_123456", "status": 3, "quota": 1})
			assert.Equal(t, http.StatusBadRequest, recorder.Code)

			_, response = extSignedRequest(t, router, extTestSecret, 0, http.MethodPut, "/api/ext/user",
				gin.H{"username": "t_654321", "status": 1, "quota": 0})
			require.True(t, response.Success, response.Message)
			var other extUserPayload
			require.NoError(t, common.Unmarshal(response.Data, &other))
			assert.Equal(t, "t_654321", other.DisplayName)

			_, response = extSignedRequest(t, router, extTestSecret, 0, http.MethodGet, "/api/ext/users?usernames=t_123456,missing,t_654321", nil)
			require.True(t, response.Success, response.Message)
			var listed []extUserPayload
			require.NoError(t, common.Unmarshal(response.Data, &listed))
			require.Len(t, listed, 2)

			// 令牌按指定 key 创建、更新、跨用户冲突、删除幂等。
			key := "sk-" + "Ab3" + "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHI"
			require.Len(t, key, 51)
			_, response = extSignedRequest(t, router, extTestSecret, 0, http.MethodPut, "/api/ext/token",
				gin.H{"key": key, "username": "t_123456", "name": "k_1001", "status": 1, "expired_time": -1,
					"model_limits_enabled": true, "model_limits": "gpt-4o,claude-sonnet-4", "allow_ips": ""})
			require.True(t, response.Success, response.Message)
			var tokenView struct {
				Id     int `json:"id"`
				UserId int `json:"user_id"`
				Status int `json:"status"`
			}
			require.NoError(t, common.Unmarshal(response.Data, &tokenView))
			assert.Equal(t, created.Id, tokenView.UserId)
			assert.Equal(t, common.TokenStatusEnabled, tokenView.Status)
			token, err := model.GetTokenByKey(key[3:], true)
			require.NoError(t, err)
			assert.True(t, token.UnlimitedQuota)
			assert.Equal(t, "k_1001", token.Name)
			assert.True(t, token.ModelLimitsEnabled)
			assert.Equal(t, "", token.Group)

			_, response = extSignedRequest(t, router, extTestSecret, 0, http.MethodPut, "/api/ext/token",
				gin.H{"key": key, "username": "t_123456", "name": "k_1001_v2", "status": 2, "expired_time": 1893456000,
					"model_limits_enabled": false, "model_limits": "", "allow_ips": "10.0.0.0/8"})
			require.True(t, response.Success, response.Message)
			var tokenAgain struct {
				Id int `json:"id"`
			}
			require.NoError(t, common.Unmarshal(response.Data, &tokenAgain))
			assert.Equal(t, tokenView.Id, tokenAgain.Id)
			token, err = model.GetTokenByKey(key[3:], true)
			require.NoError(t, err)
			assert.Equal(t, "k_1001_v2", token.Name)
			assert.Equal(t, common.TokenStatusDisabled, token.Status)
			assert.Equal(t, int64(1893456000), token.ExpiredTime)
			assert.False(t, token.ModelLimitsEnabled)
			assert.True(t, token.UnlimitedQuota)
			require.NotNil(t, token.AllowIps)
			assert.Equal(t, "10.0.0.0/8", *token.AllowIps)

			recorder, response = extSignedRequest(t, router, extTestSecret, 0, http.MethodPut, "/api/ext/token",
				gin.H{"key": key, "username": "t_654321", "name": "steal", "status": 1, "expired_time": -1})
			assert.Equal(t, http.StatusConflict, recorder.Code)
			assert.NotContains(t, response.Message, key[3:])
			recorder, _ = extSignedRequest(t, router, extTestSecret, 0, http.MethodPut, "/api/ext/token",
				gin.H{"key": key, "username": "nobody", "name": "x", "status": 1, "expired_time": -1})
			assert.Equal(t, http.StatusNotFound, recorder.Code)
			recorder, _ = extSignedRequest(t, router, extTestSecret, 0, http.MethodPut, "/api/ext/token",
				gin.H{"key": "sk-too-short", "username": "t_123456", "name": "x", "status": 1, "expired_time": -1})
			assert.Equal(t, http.StatusBadRequest, recorder.Code)

			_, response = extSignedRequest(t, router, extTestSecret, 0, http.MethodDelete, "/api/ext/token?key="+key, nil)
			require.True(t, response.Success, response.Message)
			assert.JSONEq(t, `{"deleted":true}`, string(response.Data))
			_, response = extSignedRequest(t, router, extTestSecret, 0, http.MethodDelete, "/api/ext/token?key="+key, nil)
			require.True(t, response.Success, response.Message)
			assert.JSONEq(t, `{"deleted":false}`, string(response.Data))
			_, err = model.GetTokenByKey(key[3:], true)
			assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
		})
	}
}

func TestExtLogsCursorAndHourlyStats(t *testing.T) {
	for _, dialect := range extTestDialects {
		t.Run(dialect.kind, func(t *testing.T) {
			if dialect.env != "" && os.Getenv(dialect.env) == "" {
				t.Skip("set " + dialect.env + " to run this database")
			}
			extTestDB(t, dialect.kind, os.Getenv(dialect.env))
			t.Setenv("MAAS_EXT_SECRET", extTestSecret)
			router := extTestRouter()

			// 两天前的整点作为基准，避免与缺省 before_ts（当前时间）冲突。
			base := (time.Now().Unix()/3600 - 48) * 3600
			seeds := []model.Log{
				{UserId: 1, CreatedAt: base, Type: model.LogTypeConsume, Quota: 10, ModelName: "gpt-4o", Other: `{"admin_info":{"channel_id":7}}`},
				{UserId: 1, CreatedAt: base + 10, Type: model.LogTypeConsume, Quota: 20},
				{UserId: 1, CreatedAt: base + 3600, Type: model.LogTypeConsume, Quota: 30},
				{UserId: 1, CreatedAt: base + 3700, Type: model.LogTypeError, Quota: 0},
				{UserId: 2, CreatedAt: base + 7200, Type: model.LogTypeConsume, Quota: 40},
			}
			ids := make([]int, 0, len(seeds))
			for i := range seeds {
				require.NoError(t, model.LOG_DB.Create(&seeds[i]).Error)
				ids = append(ids, seeds[i].Id)
			}

			var page struct {
				Items       []model.Log `json:"items"`
				NextAfterId int64       `json:"next_after_id"`
			}
			_, response := extSignedRequest(t, router, extTestSecret, 0, http.MethodGet, "/api/ext/logs?after_id=0&limit=2", nil)
			require.True(t, response.Success, response.Message)
			require.NoError(t, common.Unmarshal(response.Data, &page))
			require.Len(t, page.Items, 2)
			assert.Equal(t, ids[0], page.Items[0].Id)
			assert.Equal(t, ids[1], page.Items[1].Id)
			assert.Equal(t, int64(ids[1]), page.NextAfterId)
			assert.JSONEq(t, `{"admin_info":{"channel_id":7}}`, page.Items[0].Other)

			target := "/api/ext/logs?after_id=" + strconv.FormatInt(page.NextAfterId, 10) + "&before_ts=" + strconv.FormatInt(base+3700, 10)
			_, response = extSignedRequest(t, router, extTestSecret, 0, http.MethodGet, target, nil)
			require.True(t, response.Success, response.Message)
			require.NoError(t, common.Unmarshal(response.Data, &page))
			require.Len(t, page.Items, 2)
			assert.Equal(t, ids[2], page.Items[0].Id)
			assert.Equal(t, ids[3], page.Items[1].Id)

			_, response = extSignedRequest(t, router, extTestSecret, 0, http.MethodGet, "/api/ext/logs?after_id="+strconv.Itoa(ids[4]), nil)
			require.True(t, response.Success, response.Message)
			require.NoError(t, common.Unmarshal(response.Data, &page))
			assert.Empty(t, page.Items)
			assert.Equal(t, int64(ids[4]), page.NextAfterId)

			recorder, _ := extSignedRequest(t, router, extTestSecret, 0, http.MethodGet, "/api/ext/logs?after_id=-1", nil)
			assert.Equal(t, http.StatusBadRequest, recorder.Code)

			// 小时聚合：桶起始 = created_at - created_at % 3600，按小时桶与类型排序。
			target = "/api/ext/logs/stats?from=" + strconv.FormatInt(base, 10) + "&to=" + strconv.FormatInt(base+3*3600, 10)
			_, response = extSignedRequest(t, router, extTestSecret, 0, http.MethodGet, target, nil)
			require.True(t, response.Success, response.Message)
			var stats []model.LogHourlyStat
			require.NoError(t, common.Unmarshal(response.Data, &stats))
			assert.Equal(t, []model.LogHourlyStat{
				{HourStart: base, Type: model.LogTypeConsume, Count: 2, Quota: 30, MaxId: int64(ids[1])},
				{HourStart: base + 3600, Type: model.LogTypeConsume, Count: 1, Quota: 30, MaxId: int64(ids[2])},
				{HourStart: base + 3600, Type: model.LogTypeError, Count: 1, Quota: 0, MaxId: int64(ids[3])},
				{HourStart: base + 7200, Type: model.LogTypeConsume, Count: 1, Quota: 40, MaxId: int64(ids[4])},
			}, stats)

			target = "/api/ext/logs/stats?from=" + strconv.FormatInt(base, 10) + "&to=" + strconv.FormatInt(base+8*24*3600, 10)
			recorder, _ = extSignedRequest(t, router, extTestSecret, 0, http.MethodGet, target, nil)
			assert.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}
