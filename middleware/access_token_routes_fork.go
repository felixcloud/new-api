package middleware

import "maps"

// yunai fork: router/ext-router.go 的 /api/ext(外部业务系统管理扩展接口, 签名 + RootAuth)。
// 业务系统在 root 名下建一个带这几个范围的访问令牌, 旧的单个访问令牌升级后 30 天停用。
func init() {
	maps.Copy(accessTokenRouteRules, map[string]accessTokenRouteRule{
		"GET /api/ext/version":    accessTokenAnyRule,
		"PUT /api/ext/user":       accessTokenScopeRule("user:write"),
		"GET /api/ext/users":      accessTokenScopeRule("user:read"),
		"PUT /api/ext/token":      accessTokenScopeRule("api_key:write"),
		"DELETE /api/ext/token":   accessTokenScopeRule("api_key:write"),
		"GET /api/ext/logs":       accessTokenScopeRule("log:read"),
		"GET /api/ext/logs/stats": accessTokenScopeRule("log:read"),
		// 只有密钥摘要, 不含密钥; 与原生渠道列表同一个范围。
		"GET /api/ext/channels/digest": accessTokenScopeRule("channel:read"),
		// 分组级优先级 / 权重属于渠道调度, 与原生改渠道同一个范围。
		"PUT /api/ext/channel/group-policy": accessTokenScopeRule("channel:write"),
	})
}
