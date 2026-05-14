package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type sub2APIPeerHealthCheckResponse struct {
	Status string `json:"status"`
}

func CheckSub2APIPeerChannelsOnce(ctx context.Context) error {
	channels, err := model.GetRoutableSub2APIPeerChannels()
	if err != nil {
		return err
	}
	for _, channel := range channels {
		if err := checkSub2APIPeerChannelHealth(ctx, channel); err != nil {
			if markErr := model.MarkSub2APIPeerChannelHealthFailure(channel.Id, channel.ProviderUserId); markErr != nil {
				return markErr
			}
			if pauseErr := model.UpdateSub2APIPeerChannelRoutingStatus(channel.Id, channel.ProviderUserId, model.Sub2APIPeerChannelRoutingPaused); pauseErr != nil {
				return pauseErr
			}
			continue
		}
		if err := model.MarkSub2APIPeerChannelHealthSuccess(channel.Id, channel.ProviderUserId); err != nil {
			return err
		}
	}
	return nil
}

func checkSub2APIPeerChannelHealth(ctx context.Context, channel model.Sub2APIPeerChannel) error {
	endpoint := strings.TrimRight(channel.PeerEndpointURL, "/") + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("peer health returned status %d", resp.StatusCode)
	}
	var health sub2APIPeerHealthCheckResponse
	if err := common.DecodeJson(resp.Body, &health); err != nil {
		return err
	}
	if health.Status != model.Sub2APIPeerChannelHealthOnline {
		return fmt.Errorf("peer health status %q is not online", health.Status)
	}
	return nil
}
