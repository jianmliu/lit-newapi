package middleware

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// Sub2APIRuntime is a relay-time middleware that mints a per-request Sub2API
// runtime key when the distributor has selected a Sub2API channel and revokes
// it once the relay completes. For every non-Sub2API channel it is a pure
// no-op; for Sub2API channels where minting fails (admin backend not
// configured, source row missing, or the upstream rejects the call) it
// gracefully falls back to whatever ContextKeyChannelKey already holds — the
// static env-var path the adapter resolves via FromChannelOther — so the
// request still has a chance to succeed.
//
// Wiring: registered immediately after middleware.Distribute() on the
// /v1/* relay routes in router/relay-router.go. Distribute() resolves the
// channel and populates ContextKeyChannelType + ContextKeyChannelOtherSetting
// + ContextKeyChannelKey; this middleware overrides ContextKeyChannelKey
// only when channel type matches Sub2API AND a runtime key was successfully
// minted.
func Sub2APIRuntime() gin.HandlerFunc {
	return func(c *gin.Context) {
		channelType, ok := common.GetContextKeyType[int](c, constant.ContextKeyChannelType)
		if !ok || channelType != constant.ChannelTypeSub2API {
			c.Next()
			return
		}
		other, ok := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
		if !ok {
			c.Next()
			return
		}
		endpointID := strings.TrimSpace(other.Sub2APIEndpointID)
		if endpointID == "" {
			c.Next()
			return
		}
		source, err := model.GetSub2APISourceByEndpointID(endpointID)
		if err != nil || source == nil || source.Id == 0 {
			c.Next()
			return
		}
		tokenID := c.GetInt("token_id")
		if tokenID == 0 {
			tokenID = common.GetContextKeyInt(c, constant.ContextKeyTokenId)
		}
		runtimeKey, keyID, err := IssueSub2APIRuntimeKey(c, &model.Token{Id: tokenID}, source)
		if err != nil {
			common.SysLog("Sub2API runtime key issue failed, falling back to static channel key: " + err.Error())
			c.Next()
			return
		}
		common.SetContextKey(c, constant.ContextKeyChannelKey, runtimeKey)
		defer RevokeSub2APIRuntimeKey(c, source, keyID)
		c.Next()
	}
}
