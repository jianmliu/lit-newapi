package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openSub2APIDistributeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	oldDB := model.DB
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Sub2APIPeerChannel{}, &model.ProviderEndpointDepositLock{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	return db
}

func TestSub2APIRuntimeSelectsEligiblePeerWhenNoManagedSourceConfigured(t *testing.T) {
	db := openSub2APIDistributeTestDB(t)
	gin.SetMode(gin.TestMode)
	suffix := common.GetUUID()
	user := model.User{Username: "peer-route-" + suffix, AffCode: suffix, Quota: 100}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	channel, _, err := model.CreateSub2APIPeerChannelWithDeposit(model.Sub2APIPeerChannelCreateRequest{
		ProviderUserId:     user.Id,
		ProviderAccountId:  user.Id,
		DisplayName:        "route peer",
		PeerEndpointURL:    "https://route-peer.example.com",
		BackendID:          "backend-route",
		PeerPublicKey:      "public-key",
		SignatureScheme:    "ed25519-v1",
		NonceWindowSeconds: 60,
		SupportedModels:    `["gpt-4o-mini"]`,
		ModelMapping:       `{}`,
		CapacityConfig:     `{}`,
		PricingTierID:      "tier-default",
		RequiredDeposit:    40,
		IdempotencyKey:     "route-peer-channel",
	})
	if err != nil {
		t.Fatalf("create peer channel: %v", err)
	}
	if err := model.MarkSub2APIPeerChannelOwnershipVerified(channel.Id, user.Id); err != nil {
		t.Fatalf("mark verified: %v", err)
	}
	if err := model.UpdateSub2APIPeerChannelRoutingStatus(channel.Id, user.Id, model.Sub2APIPeerChannelRoutingActive); err != nil {
		t.Fatalf("mark active: %v", err)
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeSub2API)
		common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{})
		c.Set("original_model", "gpt-4o-mini")
		c.Next()
	})
	r.Use(Sub2APIRuntime())
	r.GET("/test", func(c *gin.Context) {
		other, _ := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
		peerID := common.GetContextKeyInt(c, constant.ContextKeySub2APIPeerChannelId)
		baseURL := common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl)
		if other.Sub2APIEndpointID != "backend-route" || peerID != channel.Id || baseURL != "https://route-peer.example.com" {
			c.String(http.StatusInternalServerError, "bad peer route: endpoint=%s peer=%d base=%s", other.Sub2APIEndpointID, peerID, baseURL)
			return
		}
		c.String(http.StatusOK, "ok")
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}
