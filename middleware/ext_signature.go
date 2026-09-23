package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

// extSignatureMaxSkewSeconds 是 X-Maas-Ts 与服务器时间允许的最大偏差（秒），
// 超出即视为重放或时钟不同步，直接拒绝。
const extSignatureMaxSkewSeconds = 300

// ExtSignature 为 /api/ext 扩展接口追加一层 HMAC-SHA256 请求签名校验，
// 叠加在 RootAuth 之上：外部业务系统除持有 root 访问令牌外，还必须持有
// 部署时通过环境变量 MAAS_EXT_SECRET 下发的共享密钥。
//
// 签名串 = method + "\n" + 请求路径(不含 query) + "\n" + ts + "\n" + hex(sha256(body))，
// 请求头 X-Maas-Ts 为 Unix 秒，X-Maas-Sign 为签名的小写十六进制。
// 环境变量为空表示扩展接口未启用，整个路由组一律返回 403。
func ExtSignature() gin.HandlerFunc {
	return func(c *gin.Context) {
		secret := os.Getenv("MAAS_EXT_SECRET")
		if secret == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "扩展接口未启用"})
			return
		}
		ts, err := strconv.ParseInt(c.GetHeader("X-Maas-Ts"), 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": "签名时间戳无效"})
			return
		}
		if skew := time.Now().Unix() - ts; skew > extSignatureMaxSkewSeconds || skew < -extSignatureMaxSkewSeconds {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": "签名时间戳已过期"})
			return
		}
		provided, err := hex.DecodeString(c.GetHeader("X-Maas-Sign"))
		if err != nil || len(provided) != sha256.Size {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": "签名格式无效"})
			return
		}

		// 请求体走既有的 BodyStorage，读完后把读取位置复位并挂回 Request.Body，
		// 后续 handler 仍可按常规方式 DecodeJson；GET / DELETE 无请求体时为空串。
		body := []byte{}
		if c.Request.Body != nil && c.Request.Body != http.NoBody {
			storage, err := common.GetBodyStorage(c)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": "读取请求体失败"})
				return
			}
			body, err = storage.Bytes()
			if err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": "读取请求体失败"})
				return
			}
			if _, err := storage.Seek(0, io.SeekStart); err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": "读取请求体失败"})
				return
			}
			c.Request.Body = io.NopCloser(storage)
		}
		bodyHash := sha256.Sum256(body)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(c.Request.Method + "\n" + c.Request.URL.Path + "\n" + strconv.FormatInt(ts, 10) + "\n" + hex.EncodeToString(bodyHash[:])))
		if !hmac.Equal(mac.Sum(nil), provided) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": "签名校验失败"})
			return
		}
		c.Next()
	}
}
