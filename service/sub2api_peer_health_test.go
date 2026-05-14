package service_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openSub2APIPeerHealthTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	oldDB := model.DB
	oldLOGDB := model.LOG_DB
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
	model.LOG_DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLOGDB
	})
	return db
}

func seedSub2APIPeerHealthUser(t *testing.T, db *gorm.DB, quota int) int {
	t.Helper()
	suffix := common.GetUUID()
	user := model.User{Username: "peer-health-" + suffix, AffCode: suffix, Quota: quota}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user.Id
}

func TestCheckSub2APIPeerChannelsOncePausesOfflineActiveChannel(t *testing.T) {
	db := openSub2APIPeerHealthTestDB(t)
	userID := seedSub2APIPeerHealthUser(t, db, 100)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"offline"}`))
	}))
	t.Cleanup(peer.Close)
	channel, _, err := model.CreateSub2APIPeerChannelWithDeposit(model.Sub2APIPeerChannelCreateRequest{
		ProviderUserId:     userID,
		ProviderAccountId:  userID,
		DisplayName:        "health peer",
		PeerEndpointURL:    peer.URL,
		BackendID:          "backend-health",
		PeerPublicKey:      "public-key",
		SignatureScheme:    "ed25519-v1",
		NonceWindowSeconds: 60,
		SupportedModels:    `["gpt-4o-mini"]`,
		ModelMapping:       `{}`,
		CapacityConfig:     `{}`,
		PricingTierID:      "tier-default",
		RequiredDeposit:    40,
		IdempotencyKey:     "health-peer-channel",
	})
	if err != nil {
		t.Fatalf("seed peer channel: %v", err)
	}
	if err := model.MarkSub2APIPeerChannelOwnershipVerified(channel.Id, userID); err != nil {
		t.Fatalf("mark verified: %v", err)
	}
	if err := model.UpdateSub2APIPeerChannelRoutingStatus(channel.Id, userID, model.Sub2APIPeerChannelRoutingActive); err != nil {
		t.Fatalf("mark active: %v", err)
	}

	if err := service.CheckSub2APIPeerChannelsOnce(context.Background()); err != nil {
		t.Fatalf("check peer channels: %v", err)
	}

	var reloaded model.Sub2APIPeerChannel
	if err := db.First(&reloaded, channel.Id).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.HealthStatus != model.Sub2APIPeerChannelHealthOffline || reloaded.RoutingStatus != model.Sub2APIPeerChannelRoutingPaused {
		t.Fatalf("channel after offline health check = %+v", reloaded)
	}
	if reloaded.LastHealthCheckAt == 0 || reloaded.LastFailureAt == 0 {
		t.Fatalf("failure timestamps not recorded: %+v", reloaded)
	}
}
