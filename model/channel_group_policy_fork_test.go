package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 分组策略测试（yunai fork）：同一条渠道在不同分组有不同优先级 / 权重。
func setupGroupPolicyTest(t *testing.T) {
	t.Helper()
	original := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	require.NoError(t, DB.AutoMigrate(&Channel{}, &Ability{}, &ChannelGroupPolicy{}))
	for _, table := range []string{"abilities", "channels", "channel_group_policies"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	t.Cleanup(func() {
		for _, table := range []string{"abilities", "channels", "channel_group_policies"} {
			DB.Exec("DELETE FROM " + table)
		}
		common.MemoryCacheEnabled = original
		InitChannelCache()
	})
}

func newPolicyChannel(t *testing.T, id int, name string, group string) *Channel {
	t.Helper()
	priority := int64(0)
	weight := uint(0)
	ch := &Channel{Id: id, Type: 1, Key: "k-" + name, Status: common.ChannelStatusEnabled, Name: name, Models: "m1",
		Group: group, Priority: &priority, Weight: &weight}
	require.NoError(t, ch.Insert())
	return ch
}

func pickCounts(t *testing.T, group string, retry int, n int) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for i := 0; i < n; i++ {
		ch, err := GetRandomSatisfiedChannel(group, "m1", retry, nil)
		require.NoError(t, err)
		require.NotNil(t, ch)
		counts[ch.Name]++
	}
	return counts
}

func TestGroupPolicyOppositePrioritiesPerGroup(t *testing.T) {
	setupGroupPolicyTest(t)
	a := newPolicyChannel(t, 920001, "a", "ent,vip")
	b := newPolicyChannel(t, 920002, "b", "ent,vip")
	require.NoError(t, ReplaceChannelGroupPolicies(a.Id, []ChannelGroupPolicy{{Group: "ent", Priority: 100, Weight: 1}, {Group: "vip", Priority: 80, Weight: 1}}))
	require.NoError(t, ReplaceChannelGroupPolicies(b.Id, []ChannelGroupPolicy{{Group: "ent", Priority: 80, Weight: 1}, {Group: "vip", Priority: 100, Weight: 1}}))
	InitChannelCache()

	assert.Equal(t, map[string]int{"a": 50}, pickCounts(t, "ent", 0, 50))
	assert.Equal(t, map[string]int{"b": 50}, pickCounts(t, "vip", 0, 50))
	// 第一次重试降到第二层
	assert.Equal(t, map[string]int{"b": 50}, pickCounts(t, "ent", 1, 50))
	assert.Equal(t, map[string]int{"a": 50}, pickCounts(t, "vip", 1, 50))
	// 重试次数超过层数取最低层
	assert.Equal(t, map[string]int{"b": 20}, pickCounts(t, "ent", 5, 20))

	// abilities 行也带分组值（数据库选路路径与模型列表读它）
	var ab Ability
	require.NoError(t, DB.Where("channel_id = ? and "+commonGroupCol+" = ?", a.Id, "vip").First(&ab).Error)
	require.NotNil(t, ab.Priority)
	assert.Equal(t, int64(80), *ab.Priority)
}

func TestGroupPolicyWeightsSplitSameLayer(t *testing.T) {
	setupGroupPolicyTest(t)
	a := newPolicyChannel(t, 921001, "a", "ent")
	b := newPolicyChannel(t, 921002, "b", "ent")
	require.NoError(t, ReplaceChannelGroupPolicies(a.Id, []ChannelGroupPolicy{{Group: "ent", Priority: 100, Weight: 8}}))
	require.NoError(t, ReplaceChannelGroupPolicies(b.Id, []ChannelGroupPolicy{{Group: "ent", Priority: 100, Weight: 2}}))
	InitChannelCache()

	counts := pickCounts(t, "ent", 0, 5000)
	ratio := float64(counts["a"]) / 5000
	assert.InDelta(t, 0.8, ratio, 0.04, "counts=%v", counts)
}

func TestGroupPolicyFallbackWithoutPolicy(t *testing.T) {
	setupGroupPolicyTest(t)
	a := newPolicyChannel(t, 922001, "a", "default")
	b := newPolicyChannel(t, 922002, "b", "default")
	high := int64(10)
	a.Priority = &high
	require.NoError(t, DB.Model(a).Update("priority", 10).Error)
	require.NoError(t, a.UpdateAbilities(nil))
	_ = b
	InitChannelCache()
	// 没有策略走上游原逻辑：优先级高的 a 独占第一层
	assert.Equal(t, map[string]int{"a": 30}, pickCounts(t, "default", 0, 30))
	// 只给 b 在别的分组配策略，不影响 default 分组
	require.NoError(t, ReplaceChannelGroupPolicies(b.Id, []ChannelGroupPolicy{{Group: "other", Priority: 999, Weight: 1}}))
	InitChannelCache()
	assert.Equal(t, map[string]int{"a": 30}, pickCounts(t, "default", 0, 30))
}

func TestGroupPolicyMixedWithChannelValues(t *testing.T) {
	setupGroupPolicyTest(t)
	a := newPolicyChannel(t, 923001, "a", "ent")
	b := newPolicyChannel(t, 923002, "b", "ent")
	// a 有策略（优先级 5），b 没有（回退渠道值 0）
	require.NoError(t, ReplaceChannelGroupPolicies(a.Id, []ChannelGroupPolicy{{Group: "ent", Priority: 5, Weight: 1}}))
	_ = b
	InitChannelCache()
	assert.Equal(t, map[string]int{"a": 20}, pickCounts(t, "ent", 0, 20))
	assert.Equal(t, map[string]int{"b": 20}, pickCounts(t, "ent", 1, 20))
}

func TestGroupPolicySurvivesTagBatchEdit(t *testing.T) {
	setupGroupPolicyTest(t)
	a := newPolicyChannel(t, 924001, "a", "ent")
	tag := "t1"
	require.NoError(t, DB.Model(a).Update("tag", tag).Error)
	a.Tag = &tag
	require.NoError(t, ReplaceChannelGroupPolicies(a.Id, []ChannelGroupPolicy{{Group: "ent", Priority: 77, Weight: 3}}))
	p := int64(1)
	w := uint(1)
	require.NoError(t, UpdateAbilityByTag(tag, nil, &p, &w))
	var ab Ability
	require.NoError(t, DB.Where("channel_id = ?", a.Id).First(&ab).Error)
	assert.Equal(t, int64(77), *ab.Priority)
	assert.Equal(t, uint(3), ab.Weight)
}

func TestGroupPolicyDigestAndDelete(t *testing.T) {
	setupGroupPolicyTest(t)
	x := ChannelGroupPolicyDigest([]ChannelGroupPolicy{{Group: "b", Priority: 1, Weight: 2}, {Group: "a", Priority: 3, Weight: 4}})
	y := ChannelGroupPolicyDigest([]ChannelGroupPolicy{{Group: "a", Priority: 3, Weight: 4}, {Group: "b", Priority: 1, Weight: 2}})
	assert.Equal(t, x, y)
	// 口径：sha256("a\t3\t4\nb\t1\t2\n")，与业务系统一致
	assert.Equal(t, "339efb0b18883ce7a1d8a6da1d2962c676390430a613295aa3d7d06b3e74c87a", x)
	assert.Equal(t, "", ChannelGroupPolicyDigest(nil))

	a := newPolicyChannel(t, 925001, "a", "ent")
	require.NoError(t, ReplaceChannelGroupPolicies(a.Id, []ChannelGroupPolicy{{Group: "ent", Priority: 1, Weight: 1}}))
	digests, err := ChannelGroupPolicyDigests()
	require.NoError(t, err)
	assert.NotEmpty(t, digests[a.Id])
	require.NoError(t, a.Delete())
	rows, err := GetChannelGroupPolicies(a.Id)
	require.NoError(t, err)
	assert.Empty(t, rows)
}
