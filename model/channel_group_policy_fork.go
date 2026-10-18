package model

// yunai fork：渠道的分组级调度策略。
//
// 上游的优先级 / 权重挂在渠道上，一条渠道挂多个分组时各分组只能共用一套值；业务系统要让
// 「同一条渠道在 A 分组是首选、在 B 分组是兜底」，又不想把同一个上游 Key 复制成多条渠道
// （复制后自动禁用、多 Key 轮询、测速各自为政）。这里给 (渠道, 分组) 单独存一份优先级与权重：
//   - abilities 行按分组取策略值（数据库选路路径直接用）；
//   - 内存缓存选路路径按分组读策略值，同层内按权重精确比例抽签（不做上游的平滑放大）。
// 没有策略的 (渠道, 分组) 回退渠道自身的值，所以没配策略的部署行为与上游完全一致。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// ChannelGroupPolicy 是 (渠道, 分组) 的优先级与权重。
type ChannelGroupPolicy struct {
	ChannelId int    `json:"channel_id" gorm:"primaryKey;autoIncrement:false"`
	Group     string `json:"group" gorm:"type:varchar(64);primaryKey;autoIncrement:false"`
	Priority  int64  `json:"priority" gorm:"bigint;default:0"`
	Weight    uint   `json:"weight" gorm:"default:0"`
}

func (ChannelGroupPolicy) TableName() string {
	return "channel_group_policies"
}

// channelGroupPolicyCache：渠道 id → 分组 → 策略；随 InitChannelCache 整体重建，受 channelSyncLock 保护。
var channelGroupPolicyCache map[int]map[string]ChannelGroupPolicy

// forkMigrateChannelGroupPolicy 建策略表（在上游 AutoMigrate 之后调）。
func forkMigrateChannelGroupPolicy() error {
	return DB.AutoMigrate(&ChannelGroupPolicy{})
}

func loadChannelGroupPolicies(db *gorm.DB, channelIds ...int) (map[int]map[string]ChannelGroupPolicy, error) {
	var rows []ChannelGroupPolicy
	query := db.Model(&ChannelGroupPolicy{})
	if len(channelIds) > 0 {
		query = query.Where("channel_id IN ?", channelIds)
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int]map[string]ChannelGroupPolicy)
	for _, row := range rows {
		if out[row.ChannelId] == nil {
			out[row.ChannelId] = make(map[string]ChannelGroupPolicy)
		}
		out[row.ChannelId][row.Group] = row
	}
	return out, nil
}

// GetChannelGroupPolicies 返回一条渠道的全部分组策略（按分组排序）。
func GetChannelGroupPolicies(channelId int) ([]ChannelGroupPolicy, error) {
	var rows []ChannelGroupPolicy
	err := DB.Where("channel_id = ?", channelId).Order(commonGroupCol).Find(&rows).Error
	return rows, err
}

// ChannelGroupPolicyDigest 是一组策略的规范化摘要：按分组排序后逐行「分组\t优先级\t权重」，
// 没有策略返回空串。业务系统用同一口径比对（app-admin MaasChannelRenderer.policyDigest）。
func ChannelGroupPolicyDigest(policies []ChannelGroupPolicy) string {
	if len(policies) == 0 {
		return ""
	}
	sorted := append([]ChannelGroupPolicy(nil), policies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Group < sorted[j].Group })
	var b strings.Builder
	for _, p := range sorted {
		b.WriteString(p.Group)
		b.WriteByte('\t')
		b.WriteString(strconv.FormatInt(p.Priority, 10))
		b.WriteByte('\t')
		b.WriteString(strconv.FormatUint(uint64(p.Weight), 10))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// ChannelGroupPolicyDigests 返回全部渠道的策略摘要（渠道 id → 摘要），给 /api/ext/channels/digest 用。
func ChannelGroupPolicyDigests() (map[int]string, error) {
	all, err := loadChannelGroupPolicies(DB)
	if err != nil {
		return nil, err
	}
	out := make(map[int]string, len(all))
	for id, byGroup := range all {
		list := make([]ChannelGroupPolicy, 0, len(byGroup))
		for _, p := range byGroup {
			list = append(list, p)
		}
		out[id] = ChannelGroupPolicyDigest(list)
	}
	return out, nil
}

// ReplaceChannelGroupPolicies 整组替换一条渠道的分组策略，并按新策略重建它的 abilities；
// 内存缓存由调用方刷新（InitChannelCache）。
func ReplaceChannelGroupPolicies(channelId int, policies []ChannelGroupPolicy) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		channel := Channel{}
		if err := tx.First(&channel, "id = ?", channelId).Error; err != nil {
			return err
		}
		if err := tx.Where("channel_id = ?", channelId).Delete(&ChannelGroupPolicy{}).Error; err != nil {
			return err
		}
		if len(policies) > 0 {
			rows := make([]ChannelGroupPolicy, 0, len(policies))
			for _, p := range policies {
				p.ChannelId = channelId
				rows = append(rows, p)
			}
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		return channel.UpdateAbilities(tx)
	})
}

// DeleteChannelGroupPolicies 删渠道时一起删策略，免得 id 复用（SQLite）时被新渠道继承。
func DeleteChannelGroupPolicies(channelId int) error {
	return DB.Where("channel_id = ?", channelId).Delete(&ChannelGroupPolicy{}).Error
}

// forkDeleteGroupPoliciesQuiet 是 Channel.Delete 的挂钩：清策略失败只记日志，不影响删渠道。
func forkDeleteGroupPoliciesQuiet(channelId int) {
	if err := DeleteChannelGroupPolicies(channelId); err != nil {
		common.SysError(common.LogText("delete channel group policies failed: %s", err.Error()))
	}
}

// forkApplyGroupPolicies 在 abilities 落库前按分组套用策略（AddAbilities / UpdateAbilities 的挂钩）。
// 查询失败时保持渠道值，不阻断渠道保存。
func forkApplyGroupPolicies(db *gorm.DB, channelId int, abilities []Ability) {
	if len(abilities) == 0 {
		return
	}
	if db == nil {
		db = DB
	}
	byChannel, err := loadChannelGroupPolicies(db, channelId)
	if err != nil {
		common.SysError(common.LogText("load channel group policies failed: %s", err.Error()))
		return
	}
	byGroup := byChannel[channelId]
	if len(byGroup) == 0 {
		return
	}
	for i := range abilities {
		if p, ok := byGroup[abilities[i].Group]; ok {
			priority := p.Priority
			abilities[i].Priority = &priority
			abilities[i].Weight = p.Weight
		}
	}
}

// forkReapplyGroupPoliciesByTag 是 UpdateAbilityByTag 的挂钩：按标签批量改优先级 / 权重后，
// 把有分组策略的 abilities 改回策略值，控制台的批量编辑不会覆盖业务系统下发的分组策略。
func forkReapplyGroupPoliciesByTag(tag string) error {
	var ids []int
	if err := DB.Model(&Ability{}).Where("tag = ?", tag).Distinct("channel_id").Pluck("channel_id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	byChannel, err := loadChannelGroupPolicies(DB, ids...)
	if err != nil {
		return err
	}
	for channelId, byGroup := range byChannel {
		for group, p := range byGroup {
			err := DB.Model(&Ability{}).Where("channel_id = ? and "+commonGroupCol+" = ?", channelId, group).
				Updates(map[string]any{"priority": p.Priority, "weight": p.Weight}).Error
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// forkLoadGroupPolicyCache 在 InitChannelCache 里、拿锁之前读出全部策略。
func forkLoadGroupPolicyCache() map[int]map[string]ChannelGroupPolicy {
	all, err := loadChannelGroupPolicies(DB)
	if err != nil {
		common.SysError(common.LogText("load channel group policies failed: %s", err.Error()))
		return map[int]map[string]ChannelGroupPolicy{}
	}
	return all
}

// forkPriorityWeight 返回渠道在某分组下的优先级与权重；没有策略回退渠道值。调用方持有 channelSyncLock。
func forkPriorityWeight(group string, channel *Channel) (int64, int) {
	if byGroup := channelGroupPolicyCache[channel.Id]; byGroup != nil {
		if p, ok := byGroup[group]; ok {
			return p.Priority, int(p.Weight)
		}
	}
	return channel.GetPriority(), channel.GetWeight()
}

// forkHasGroupPolicy：候选里有任何一条渠道在该分组配了策略。
func forkHasGroupPolicy(group string, channelIds []int) bool {
	for _, id := range channelIds {
		if byGroup := channelGroupPolicyCache[id]; byGroup != nil {
			if _, ok := byGroup[group]; ok {
				return true
			}
		}
	}
	return false
}

// forkSelectByGroupPolicy 是 GetRandomSatisfiedChannel 的挂钩（已持 channelSyncLock 读锁）。
// 候选里没有分组策略时返回 handled=false，交回上游原逻辑；有策略时：
//   - 按分组优先级分层，第 retry 次取第 retry 高的层（超出取最低层），与上游语义一致；
//   - 同层按权重精确比例抽签；同层权重全为 0 时均分（上游的「平均权重 < 10 放大 100 倍」不再适用）。
func forkSelectByGroupPolicy(group string, channelIds []int, retry int) (channel *Channel, handled bool, err error) {
	if !forkHasGroupPolicy(group, channelIds) {
		return nil, false, nil
	}
	type candidate struct {
		channel  *Channel
		priority int64
		weight   int
	}
	candidates := make([]candidate, 0, len(channelIds))
	seen := make(map[int64]bool)
	priorities := make([]int64, 0)
	for _, id := range channelIds {
		ch, ok := channelsIDM[id]
		if !ok {
			return nil, true, fmt.Errorf("database consistency error: channel #%d does not exist, please contact the administrator", id)
		}
		priority, weight := forkPriorityWeight(group, ch)
		candidates = append(candidates, candidate{ch, priority, weight})
		if !seen[priority] {
			seen[priority] = true
			priorities = append(priorities, priority)
		}
	}
	sort.Slice(priorities, func(i, j int) bool { return priorities[i] > priorities[j] })
	if retry >= len(priorities) {
		retry = len(priorities) - 1
	}
	if retry < 0 {
		retry = 0
	}
	target := priorities[retry]
	layer := make([]candidate, 0, len(candidates))
	sum := 0
	for _, c := range candidates {
		if c.priority == target {
			layer = append(layer, c)
			if c.weight > 0 {
				sum += c.weight
			}
		}
	}
	if sum == 0 {
		return layer[rand.Intn(len(layer))].channel, true, nil
	}
	pick := rand.Intn(sum)
	for _, c := range layer {
		if c.weight <= 0 {
			continue
		}
		pick -= c.weight
		if pick < 0 {
			return c.channel, true, nil
		}
	}
	return layer[len(layer)-1].channel, true, nil
}
