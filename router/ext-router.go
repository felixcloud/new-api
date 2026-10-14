package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

// setExtRouter(yunai fork) 注册外部业务系统管理扩展接口：HMAC 签名先于 RootAuth 执行，
// 未配置 MAAS_EXT_SECRET 时整个路由组统一 403（未启用）。
// 访问令牌范围见 middleware/access_token_routes_fork.go。
func setExtRouter(apiRouter *gin.RouterGroup) {
	extRouter := apiRouter.Group("/ext")
	extRouter.Use(middleware.ExtSignature(), middleware.RootAuth())
	{
		extRouter.GET("/version", controller.ExtVersion)
		extRouter.PUT("/user", controller.ExtUpsertUser)
		extRouter.GET("/users", controller.ExtGetUsers)
		extRouter.PUT("/token", controller.ExtUpsertToken)
		extRouter.DELETE("/token", controller.ExtDeleteToken)
		extRouter.GET("/logs", controller.ExtListLogs)
		extRouter.GET("/logs/stats", controller.ExtLogStats)
		extRouter.GET("/channels/digest", controller.ExtChannelDigest)
	}
}
