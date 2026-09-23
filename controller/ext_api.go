package controller

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 本文件是 /api/ext 扩展接口：供外部业务系统把本系统当作「转发 + 记原始日志」
// 的数据面来驱动。外部系统按用户名维护客户用户、按自己生成的 key 维护令牌、
// 按 id 游标增量拉日志、按小时桶对账。鉴权见 router 中该路由组挂的
// ExtSignature + RootAuth 两层中间件。

const (
	// extVersion 是扩展接口契约版本，接口形状不兼容变更时递增。
	extVersion = "1"
	// extUsersQueryLimit 是 GET /api/ext/users 一次最多接受的用户名个数。
	extUsersQueryLimit = 500
	// extLogsDefaultLimit / extLogsMaxLimit 是日志游标拉取的缺省与上限条数。
	extLogsDefaultLimit = 1000
	extLogsMaxLimit     = 5000
	// extLogStatsMaxRangeSeconds 是小时聚合允许的最大时间跨度（7 天）。
	extLogStatsMaxRangeSeconds = 7 * 24 * 3600
	// extTokenNameMaxLength 与令牌页面的名称长度限制一致。
	extTokenNameMaxLength = 50
)

// extTokenKeyPattern 是去掉 sk- 前缀后的令牌 key 形状，与 common.GenerateKey 生成的一致。
var extTokenKeyPattern = regexp.MustCompile(`^[0-9A-Za-z]{48}$`)

// extFail 以统一的 {"success":false,"message":...} 形状返回指定 HTTP 状态码。
func extFail(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"success": false, "message": message})
}

// extUserView 是用户相关接口对外返回的统一形状。
func extUserView(user *model.User) gin.H {
	return gin.H{
		"id":           user.Id,
		"username":     user.Username,
		"display_name": user.DisplayName,
		"group":        user.Group,
		"status":       user.Status,
		"quota":        user.Quota,
		"used_quota":   user.UsedQuota,
	}
}

type extUserRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Group       string `json:"group"`
	Status      int    `json:"status"`
	Quota       int    `json:"quota"`
}

// ExtUpsertUser 按用户名创建或更新客户用户。quota 是绝对值（剩余额度），
// 走既有的 AdjustUserQuota override 路径写库并同步额度缓存。
func ExtUpsertUser(c *gin.Context) {
	var req extUserRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		extFail(c, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || len(req.Username) > model.UserNameMaxLength {
		extFail(c, http.StatusBadRequest, fmt.Sprintf("username 不能为空且不超过 %d 字符", model.UserNameMaxLength))
		return
	}
	if req.Status != common.UserStatusEnabled && req.Status != common.UserStatusDisabled {
		extFail(c, http.StatusBadRequest, "status 只接受 1（启用）或 2（禁用）")
		return
	}
	if req.Quota < 0 || req.Quota > common.MaxWalletQuota {
		extFail(c, http.StatusBadRequest, fmt.Sprintf("quota 必须在 0 到 %d 之间", common.MaxWalletQuota))
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = req.Username
	}
	if req.Group == "" {
		req.Group = "default"
	}

	existing, err := model.GetUsersByUsernames([]string{req.Username})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var user *model.User
	if len(existing) == 0 {
		// 随机强密码只用于满足建账约束，不返回、不记日志；客户不会用它登录。
		password, err := common.GenerateRandomCharsKey(32)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		user = &model.User{
			Username:    req.Username,
			Password:    password,
			DisplayName: req.DisplayName,
			Role:        common.RoleCommonUser,
			Status:      req.Status,
			Group:       req.Group,
		}
		if err := common.Validate.Struct(user); err != nil {
			extFail(c, http.StatusBadRequest, "用户字段不合法: "+err.Error())
			return
		}
		// Insert 会把 Quota 覆盖成新用户赠送额度，绝对额度在下面统一用 override 写入。
		if err := user.Insert(0); err != nil {
			common.ApiError(c, err)
			return
		}
	} else {
		user = existing[0]
		if user.Role != common.RoleCommonUser {
			extFail(c, http.StatusConflict, "该用户名属于管理员账号，扩展接口不予修改")
			return
		}
		statusOrGroupChanged := user.Status != req.Status || user.Group != req.Group
		user.DisplayName = req.DisplayName
		user.Group = req.Group
		user.Status = req.Status
		if err := common.Validate.StructExcept(user, "Password"); err != nil {
			extFail(c, http.StatusBadRequest, "用户字段不合法: "+err.Error())
			return
		}
		// Update 内部已刷新用户缓存并在状态/分组变化时提升 auth_version、吊销会话；
		// 令牌缓存需单独失效，否则已缓存的令牌在 TTL 内仍按旧用户状态放行。
		if err := user.Update(false); err != nil {
			common.ApiError(c, err)
			return
		}
		if statusOrGroupChanged {
			if err := model.InvalidateUserTokensCache(user.Id); err != nil {
				common.SysLog(fmt.Sprintf("failed to invalidate tokens cache for user %d: %s", user.Id, err.Error()))
			}
		}
	}
	if _, err := model.AdjustUserQuota(user.Id, common.RoleRootUser, "override", req.Quota); err != nil {
		common.ApiError(c, err)
		return
	}
	fresh, err := model.GetUserById(user.Id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, extUserView(fresh))
}

// ExtGetUsers 按逗号分隔的用户名批量取用户，找不到的名字不出现在结果里。
func ExtGetUsers(c *gin.Context) {
	usernames := make([]string, 0)
	for name := range strings.SplitSeq(c.Query("usernames"), ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			usernames = append(usernames, name)
		}
	}
	if len(usernames) == 0 {
		extFail(c, http.StatusBadRequest, "usernames 不能为空")
		return
	}
	if len(usernames) > extUsersQueryLimit {
		extFail(c, http.StatusBadRequest, fmt.Sprintf("usernames 一次最多 %d 个", extUsersQueryLimit))
		return
	}
	users, err := model.GetUsersByUsernames(usernames)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	views := make([]gin.H, 0, len(users))
	for _, user := range users {
		views = append(views, extUserView(user))
	}
	common.ApiSuccess(c, views)
}

type extTokenRequest struct {
	Key                string `json:"key"`
	Username           string `json:"username"`
	Name               string `json:"name"`
	Status             int    `json:"status"`
	ExpiredTime        int64  `json:"expired_time"`
	ModelLimitsEnabled bool   `json:"model_limits_enabled"`
	ModelLimits        string `json:"model_limits"`
	AllowIps           string `json:"allow_ips"`
}

// ExtUpsertToken 按外部系统指定的 key 创建或更新令牌。令牌恒为不限额度，
// 分组留空即跟随用户分组。响应与日志里都不出现完整 key。
func ExtUpsertToken(c *gin.Context) {
	var req extTokenRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		extFail(c, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	key := strings.TrimPrefix(strings.TrimSpace(req.Key), "sk-")
	if !extTokenKeyPattern.MatchString(key) {
		extFail(c, http.StatusBadRequest, "key 去掉 sk- 前缀后必须是 48 位字母数字")
		return
	}
	if len(req.Name) > extTokenNameMaxLength {
		extFail(c, http.StatusBadRequest, fmt.Sprintf("name 不能超过 %d 字符", extTokenNameMaxLength))
		return
	}
	if req.Status != common.TokenStatusEnabled && req.Status != common.TokenStatusDisabled {
		extFail(c, http.StatusBadRequest, "status 只接受 1（启用）或 2（禁用）")
		return
	}
	if req.ExpiredTime < -1 {
		extFail(c, http.StatusBadRequest, "expired_time 必须是 -1（永不过期）或 Unix 秒")
		return
	}
	users, err := model.GetUsersByUsernames([]string{strings.TrimSpace(req.Username)})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if len(users) == 0 {
		extFail(c, http.StatusNotFound, "用户不存在")
		return
	}
	user := users[0]

	token, err := model.GetTokenByKey(key, true)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiError(c, err)
		return
	}
	allowIps := req.AllowIps
	if err != nil {
		now := common.GetTimestamp()
		token = &model.Token{
			UserId:             user.Id,
			Key:                key,
			Name:               req.Name,
			Status:             req.Status,
			CreatedTime:        now,
			AccessedTime:       now,
			ExpiredTime:        req.ExpiredTime,
			RemainQuota:        0,
			UnlimitedQuota:     true,
			ModelLimitsEnabled: req.ModelLimitsEnabled,
			ModelLimits:        req.ModelLimits,
			AllowIps:           &allowIps,
		}
		if err := token.Insert(); err != nil {
			// 唯一键冲突一类的驱动错误会回显完整 key，返回前先脱敏。
			common.ApiErrorMsg(c, strings.ReplaceAll(err.Error(), key, model.MaskTokenKey(key)))
			return
		}
		common.ApiSuccess(c, gin.H{"id": token.Id, "user_id": token.UserId, "status": token.Status})
		return
	}
	if token.UserId != user.Id {
		extFail(c, http.StatusConflict, "该 key 已属于其他用户")
		return
	}
	// 只改外部系统可控的字段；Update 会先失效令牌缓存再写库。
	token.Name = req.Name
	token.Status = req.Status
	token.ExpiredTime = req.ExpiredTime
	token.ModelLimitsEnabled = req.ModelLimitsEnabled
	token.ModelLimits = req.ModelLimits
	token.AllowIps = &allowIps
	token.UnlimitedQuota = true
	if err := token.Update(); err != nil {
		common.ApiErrorMsg(c, strings.ReplaceAll(err.Error(), key, model.MaskTokenKey(key)))
		return
	}
	common.ApiSuccess(c, gin.H{"id": token.Id, "user_id": token.UserId, "status": token.Status})
}

// ExtDeleteToken 按 key 删除令牌；令牌不存在也返回成功，便于外部系统重试。
func ExtDeleteToken(c *gin.Context) {
	key := strings.TrimPrefix(strings.TrimSpace(c.Query("key")), "sk-")
	if !extTokenKeyPattern.MatchString(key) {
		extFail(c, http.StatusBadRequest, "key 去掉 sk- 前缀后必须是 48 位字母数字")
		return
	}
	token, err := model.GetTokenByKey(key, true)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiSuccess(c, gin.H{"deleted": false})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := token.Delete(); err != nil {
		common.ApiErrorMsg(c, strings.ReplaceAll(err.Error(), key, model.MaskTokenKey(key)))
		return
	}
	common.ApiSuccess(c, gin.H{"deleted": true})
}

// ExtListLogs 按 id 游标增量拉取日志：id > after_id 且 created_at <= before_ts，
// 按 id 升序。返回的是 root 视角的完整记录（含 other），不做脱敏裁剪。
func ExtListLogs(c *gin.Context) {
	afterId, err := strconv.ParseInt(c.DefaultQuery("after_id", "0"), 10, 64)
	if err != nil || afterId < 0 {
		extFail(c, http.StatusBadRequest, "after_id 必须是非负整数")
		return
	}
	beforeTs, err := strconv.ParseInt(c.DefaultQuery("before_ts", strconv.FormatInt(common.GetTimestamp(), 10)), 10, 64)
	if err != nil || beforeTs <= 0 {
		extFail(c, http.StatusBadRequest, "before_ts 必须是 Unix 秒")
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(extLogsDefaultLimit)))
	if err != nil || limit <= 0 {
		extFail(c, http.StatusBadRequest, "limit 必须是正整数")
		return
	}
	limit = min(limit, extLogsMaxLimit)
	logs, err := model.GetLogsAfterId(afterId, beforeTs, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	nextAfterId := afterId
	if len(logs) > 0 {
		nextAfterId = int64(logs[len(logs)-1].Id)
	}
	common.ApiSuccess(c, gin.H{"items": logs, "next_after_id": nextAfterId})
}

// ExtLogStats 按「小时桶 × 日志类型」聚合 [from, to) 区间的日志，区间不超过 7 天。
func ExtLogStats(c *gin.Context) {
	from, fromErr := strconv.ParseInt(c.Query("from"), 10, 64)
	to, toErr := strconv.ParseInt(c.Query("to"), 10, 64)
	if fromErr != nil || toErr != nil || from <= 0 || to <= from {
		extFail(c, http.StatusBadRequest, "from / to 必须是 Unix 秒且 from < to")
		return
	}
	if to-from > extLogStatsMaxRangeSeconds {
		extFail(c, http.StatusBadRequest, "from 到 to 的区间不能超过 7 天")
		return
	}
	stats, err := model.GetLogHourlyStats(from, to)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, stats)
}

// ExtVersion 返回程序版本、扩展接口契约版本与本实例的持久化随机标识，
// 外部系统据此识别对端是否被替换（标识变化意味着 options 表被重建）。
func ExtVersion(c *gin.Context) {
	instanceId, err := model.EnsureMaasExtInstanceId()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"version": common.Version, "ext_version": extVersion, "instance_id": instanceId})
}
