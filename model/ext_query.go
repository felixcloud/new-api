package model

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

// 本文件承载 /api/ext 扩展接口专用的查询：外部业务系统把本系统当作
// 「转发 + 记原始日志」的数据面，按用户名批量对账用户、按 id 游标增量拉日志、
// 按小时桶聚合日志用于对账。

// MaasExtInstanceIdOptionKey 是 options 表中持久化本实例随机标识的键名，
// 首次调用 /api/ext/version 时生成，之后每次返回同一个值。
const MaasExtInstanceIdOptionKey = "MaasExtInstanceId"

var maasExtInstanceIdMutex sync.Mutex

// GetUsersByUsernames 按用户名批量取用户，不存在的名字不出现在结果里。
// 密码与管理访问令牌不查出。
func GetUsersByUsernames(usernames []string) ([]*User, error) {
	users := make([]*User, 0, len(usernames))
	if len(usernames) == 0 {
		return users, nil
	}
	err := DB.Omit("password", "access_token").Where("username IN ?", usernames).Find(&users).Error
	return users, err
}

// GetLogsAfterId 从日志库按 id 游标增量取日志：id > afterId 且 created_at <= beforeTimestamp，
// 按 id 升序，最多 limit 条。ClickHouse 日志库没有自增 id，沿用既有的排序帮助函数
// 保证 SQL 可执行，但该库下 id 游标本身不具备增量语义。
func GetLogsAfterId(afterId int64, beforeTimestamp int64, limit int) ([]*Log, error) {
	logs := make([]*Log, 0, limit)
	order := "id asc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("")
	}
	err := LOG_DB.Where("id > ? AND created_at <= ?", afterId, beforeTimestamp).Order(order).Limit(limit).Find(&logs).Error
	return logs, err
}

// LogHourlyStat 是「小时桶 × 日志类型」的聚合结果，供外部系统按小时对账。
type LogHourlyStat struct {
	HourStart int64 `json:"hour_start" gorm:"column:hour_start"`
	Type      int   `json:"type" gorm:"column:type"`
	Count     int64 `json:"count" gorm:"column:log_count"`
	Quota     int64 `json:"quota" gorm:"column:quota_sum"`
	MaxId     int64 `json:"max_id" gorm:"column:max_id"`
}

// GetLogHourlyStats 按小时桶与日志类型聚合 [from, to) 区间内的日志。
// 小时桶用 created_at - (created_at % 3600) 表达，取模运算符在 SQLite / MySQL /
// PostgreSQL / ClickHouse 上通用，不依赖 FROM_UNIXTIME 一类方言函数。
func GetLogHourlyStats(from int64, to int64) ([]LogHourlyStat, error) {
	stats := make([]LogHourlyStat, 0)
	err := LOG_DB.Model(&Log{}).
		Select("created_at - (created_at % 3600) AS hour_start, type, COUNT(*) AS log_count, SUM(quota) AS quota_sum, MAX(id) AS max_id").
		Where("created_at >= ? AND created_at < ?", from, to).
		Group("created_at - (created_at % 3600), type").
		Order("hour_start asc, type asc").
		Scan(&stats).Error
	return stats, err
}

// EnsureMaasExtInstanceId 返回本实例的随机标识；首次调用时生成 16 字节随机数的
// 十六进制串并持久化到 options 表。先看内存中的 OptionMap，再查库（多实例或
// 尚未加载时），都没有才生成；落库用 FirstOrCreate，两个实例同时首次调用时
// 也只会保留先写入的那个值。
func EnsureMaasExtInstanceId() (string, error) {
	maasExtInstanceIdMutex.Lock()
	defer maasExtInstanceIdMutex.Unlock()

	common.OptionMapRWMutex.RLock()
	cached := common.OptionMap[MaasExtInstanceIdOptionKey]
	common.OptionMapRWMutex.RUnlock()
	if cached != "" {
		return cached, nil
	}

	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	option := Option{Key: MaasExtInstanceIdOptionKey}
	if err := DB.Where(&Option{Key: MaasExtInstanceIdOptionKey}).Attrs(Option{Value: hex.EncodeToString(raw)}).FirstOrCreate(&option).Error; err != nil {
		return "", err
	}
	if err := updateOptionMap(MaasExtInstanceIdOptionKey, option.Value); err != nil {
		return "", err
	}
	return option.Value, nil
}
