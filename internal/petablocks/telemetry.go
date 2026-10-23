package petablocks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	storage "github.com/nickheyer/discopanel/internal/db"
	"github.com/nickheyer/discopanel/internal/events"
	"github.com/nickheyer/discopanel/pkg/logger"
	v1 "github.com/nickheyer/discopanel/pkg/proto/discopanel/v1"
)

// TelemetryConfig represents the JSON structure expected by the PETABLOCKS Telemetry Companion mod.
type TelemetryConfig struct {
	GatewayURL                     string `json:"gatewayUrl"`
	ServerID                       string `json:"serverId"`
	APISecretToken                 string `json:"apiSecretToken"`
	TelemetryIntervalSeconds       int    `json:"telemetryIntervalSeconds"`
	IdleIntervalSeconds            int    `json:"idleIntervalSeconds"`
	EnableChatLogging              bool   `json:"enableChatLogging"`
	EnableModSpecificMetrics       bool   `json:"enableModSpecificMetrics"`
	ReconnectBackoffInitialSeconds int    `json:"reconnectBackoffInitialSeconds"`
	ReconnectBackoffMaxSeconds     int    `json:"reconnectBackoffMaxSeconds"`
	AfkThresholdSeconds            int    `json:"afkThresholdSeconds"`
	LagSpikeThresholdMs            int    `json:"lagSpikeThresholdMs"`
}

// RegisterEventHandlers subscribes PETABLOCKS lifecycle handlers to DiscoPanel's event bus.
func RegisterEventHandlers(bus *events.Bus, store *storage.Store, log *logger.Logger) {
	if bus == nil {
		return
	}
	bus.Subscribe(func(ctx context.Context, evt events.Event) {
		if evt.Type == v1.TriggeredEventType_TRIGGERED_EVENT_TYPE_SERVER_START ||
			evt.Type == v1.TriggeredEventType_TRIGGERED_EVENT_TYPE_SERVER_RESTART {
			EnsureServerTelemetryConfig(ctx, store, evt.ServerID, log)
		}
	})
}

// EnsureServerTelemetryConfig ensures the server's data directory has a valid
// PETABLOCKS Telemetry Companion configuration file if API_SECRET_TOKEN is configured.
func EnsureServerTelemetryConfig(ctx context.Context, store *storage.Store, serverID string, log *logger.Logger) {
	secret := os.Getenv("API_SECRET_TOKEN")
	if secret == "" {
		secret = os.Getenv("DISCOPANEL_INTERNAL_API_TOKEN")
	}
	if secret == "" {
		return
	}

	server, err := store.GetServer(ctx, serverID)
	if err != nil || server == nil || server.DataPath == "" {
		return
	}

	gatewayURL := os.Getenv("TELEMETRY_INTERNAL_WS_URL")
	if gatewayURL == "" {
		gatewayURL = "ws://10.20.110.116:3000/ws/servers/bridge"
	}

	relConfigPath := resolveTelemetryConfigSubpath(string(server.ModLoader), server.Name)
	fullConfigPath := filepath.Join(server.DataPath, relConfigPath)

	// If config already exists, do not overwrite custom user flags
	if _, err := os.Stat(fullConfigPath); err == nil {
		return
	}

	if err := os.MkdirAll(filepath.Dir(fullConfigPath), 0755); err != nil {
		log.Warn("[PETABLOCKS] Could not create telemetry config dir for %s: %v", server.Name, err)
		return
	}

	cfg := TelemetryConfig{
		GatewayURL:                     gatewayURL,
		ServerID:                       server.ID,
		APISecretToken:                 secret,
		TelemetryIntervalSeconds:       5,
		IdleIntervalSeconds:            15,
		EnableChatLogging:              true,
		EnableModSpecificMetrics:       true,
		ReconnectBackoffInitialSeconds: 3,
		ReconnectBackoffMaxSeconds:     60,
		AfkThresholdSeconds:            300,
		LagSpikeThresholdMs:            100,
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}

	if err := os.WriteFile(fullConfigPath, data, 0644); err == nil {
		log.Info("[PETABLOCKS] Provisioned telemetry config at %s for server %s", fullConfigPath, server.ID)
	}
}

func resolveTelemetryConfigSubpath(loader string, name string) string {
	l := strings.ToLower(loader)
	n := strings.ToLower(name)
	if strings.Contains(l, "velocity") || strings.Contains(n, "velocity") || strings.Contains(n, "proxy") {
		return filepath.Join("plugins", "petablocks_telemetry", "config.json")
	}
	if strings.Contains(l, "paper") || strings.Contains(l, "purpur") || strings.Contains(l, "spigot") || strings.Contains(l, "bukkit") {
		return filepath.Join("plugins", "PetablocksTelemetry", "config.json")
	}
	return filepath.Join("config", "petablocks-telemetry.json")
}
