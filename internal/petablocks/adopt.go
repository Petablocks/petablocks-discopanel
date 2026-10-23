package petablocks

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/google/uuid"
	storage "github.com/nickheyer/discopanel/internal/db"
	"github.com/nickheyer/discopanel/internal/docker"
	"github.com/nickheyer/discopanel/internal/minecraft"
	"github.com/nickheyer/discopanel/pkg/logger"
	v1 "github.com/nickheyer/discopanel/pkg/proto/discopanel/v1"
)

// AdoptResult summarizes containers discovered and adopted on this node.
type AdoptResult struct {
	Adopted []AdoptedServerInfo `json:"adopted"`
	Updated []AdoptedServerInfo `json:"updated"`
	Skipped []string            `json:"skipped"`
}

// AdoptedServerInfo represents a single adopted or reconciled Minecraft container.
type AdoptedServerInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContainerID   string `json:"containerId"`
	ContainerName string `json:"containerName"`
	DataPath      string `json:"dataPath"`
	Port          int    `json:"port"`
	ModLoader     string `json:"modLoader"`
	MCVersion     string `json:"mcVersion"`
	MemoryMB      int    `json:"memoryMb"`
	Status        string `json:"status"`
}

// KnownPETABLOCKSContainers maps known container names to friendly display names and canonical IDs.
var KnownPETABLOCKSContainers = map[string]struct {
	ID          string
	Name        string
	Description string
}{
	"petablocks-create-2": {
		ID:          "create-2",
		Name:        "Just Create SMP 2",
		Description: "NeoForge 1.21.1 Create 2 SMP Server",
	},
	"petablocks-patreon-creative": {
		ID:          "patreon-creative",
		Name:        "PETABLOCKS Patreon Creative",
		Description: "NeoForge 1.21.1 Whitelisted Patreon Creative Server",
	},
	"pb-lobby-main": {
		ID:          "lobby-main",
		Name:        "Central Network Lobby Hub",
		Description: "Central Network Hub & Lobby (Paper 1.21.4)",
	},
	"pb-velocity-proxy": {
		ID:          "velocity-proxy",
		Name:        "Velocity Proxy Gateway",
		Description: "Velocity 4.2.1 Gateway & Routing Proxy",
	},
	"pb-paper-smp": {
		ID:          "paper-duo-smp",
		Name:        "Paper 26.3 Duo SMP",
		Description: "Private Co-Op Survival Server (Paper 26.3)",
	},
	"petablocks-modpack-main": {
		ID:          "fabric-main",
		Name:        "Official Modpack (Season 1 Archived)",
		Description: "Fabric 1.20.1 Season 1 Modpack Server",
	},
	"petablocks-modpack-s2-test": {
		ID:          "modpack-s2-test",
		Name:        "PETABLOCKS Modpack Season 2 (Test Server)",
		Description: "PETABLOCKS Modpack Season 2 Staging & Test Server",
	},
	"pb-modpack-s2-test": {
		ID:          "modpack-s2-test",
		Name:        "PETABLOCKS Modpack Season 2 (Test Server)",
		Description: "PETABLOCKS Modpack Season 2 Staging & Test Server",
	},
}

// DiscoverAndAdoptContainers scans the local Docker daemon for running or stopped
// Minecraft / Velocity containers and non-destructively registers or reconciles them
// in DiscoPanel's SQLite store. Never stops, restarts, or wipes any container or world.
func DiscoverAndAdoptContainers(ctx context.Context, store *storage.Store, dockerClient *docker.Client, log *logger.Logger) (*AdoptResult, error) {
	res := &AdoptResult{
		Adopted: []AdoptedServerInfo{},
		Updated: []AdoptedServerInfo{},
		Skipped: []string{},
	}

	rawDocker := dockerClient.GetDockerClient()
	containers, err := rawDocker.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	existingServers, err := store.ListServers(ctx)
	if err != nil {
		return nil, err
	}

	byContainerID := make(map[string]*storage.Server)
	byServerID := make(map[string]*storage.Server)
	byDataPath := make(map[string]*storage.Server)
	for _, s := range existingServers {
		if s.ContainerID != "" {
			byContainerID[s.ContainerID] = s
		}
		byServerID[s.ID] = s
		if s.DataPath != "" {
			byDataPath[s.DataPath] = s
		}
	}

	for _, c := range containers {
		containerName := ""
		if len(c.Names) > 0 {
			containerName = strings.TrimPrefix(c.Names[0], "/")
		}

		// Skip discopanel itself or module containers
		if strings.Contains(containerName, "discopanel") && !strings.HasPrefix(containerName, "discopanel-server-") {
			res.Skipped = append(res.Skipped, containerName)
			continue
		}
		if c.Labels["discopanel.module.id"] != "" {
			res.Skipped = append(res.Skipped, containerName)
			continue
		}

		inspect, err := rawDocker.ContainerInspect(ctx, c.ID)
		if err != nil {
			continue
		}

		if !isMinecraftOrProxyContainer(containerName, inspect) {
			res.Skipped = append(res.Skipped, containerName)
			continue
		}

		envMap := parseEnvSlice(inspect.Config.Env)
		dataPath := extractDataMountPath(inspect)
		if dataPath == "" {
			dataPath = "/home/user/data/servers/" + containerName
		}

		port, additionalPorts := extractPorts(inspect, envMap)
		modLoader := detectModLoader(containerName, inspect.Config.Image, envMap)
		mcVersion := detectMCVersion(envMap)
		memoryMB := detectMemoryMB(inspect, envMap)
		status := mapContainerState(inspect.State)

		// Determine canonical ID and friendly Name
		serverID := c.Labels["discopanel.server.id"]
		friendlyName := c.Labels["discopanel.server.name"]
		description := ""

		if known, ok := KnownPETABLOCKSContainers[containerName]; ok {
			if serverID == "" {
				serverID = known.ID
			}
			if friendlyName == "" {
				friendlyName = known.Name
			}
			description = known.Description
		} else {
			// Also detect any Season 2 test / modpack container by name pattern
			lowerName := strings.ToLower(containerName)
			if serverID == "" {
				if strings.Contains(lowerName, "s2") || strings.Contains(lowerName, "season-2") || strings.Contains(lowerName, "season2") {
					serverID = sanitizeID(containerName)
					friendlyName = "PETABLOCKS Modpack Season 2 (" + containerName + ")"
					description = "PETABLOCKS Modpack Season 2 Test Server"
				} else {
					serverID = sanitizeID(containerName)
				}
			}
			if friendlyName == "" {
				if serverNameEnv := envMap["SERVER_NAME"]; serverNameEnv != "" {
					friendlyName = serverNameEnv
				} else {
					friendlyName = formatFriendlyName(containerName)
				}
			}
		}

		// Check if already tracked
		existing := byContainerID[c.ID]
		if existing == nil {
			existing = byServerID[serverID]
		}
		if existing == nil && dataPath != "" {
			existing = byDataPath[dataPath]
		}

		if existing != nil {
			changed := false
			if existing.ContainerID != c.ID {
				existing.ContainerID = c.ID
				changed = true
			}
			if existing.Status != status {
				existing.Status = status
				changed = true
			}
			if existing.DataPath != dataPath && dataPath != "" {
				existing.DataPath = dataPath
				changed = true
			}
			if port > 0 && existing.Port != port {
				existing.Port = port
				changed = true
			}
			if changed {
				_ = store.UpdateServer(ctx, existing)
			}
			res.Updated = append(res.Updated, AdoptedServerInfo{
				ID:            existing.ID,
				Name:          existing.Name,
				ContainerID:   c.ID,
				ContainerName: containerName,
				DataPath:      existing.DataPath,
				Port:          existing.Port,
				ModLoader:     string(existing.ModLoader),
				MCVersion:     existing.MCVersion,
				MemoryMB:      existing.Memory,
				Status:        string(existing.Status),
			})
			continue
		}

		if serverID == "" {
			serverID = uuid.New().String()
		}

		imageTag := extractImageTag(inspect.Config.Image)
		javaVer := docker.GetRequiredJavaVersion(mcVersion, modLoader)

		newServer := &storage.Server{
			ID:              serverID,
			Name:            friendlyName,
			Description:     description,
			ModLoader:       modLoader,
			MCVersion:       mcVersion,
			ContainerID:     c.ID,
			Status:          status,
			Port:            port,
			MaxPlayers:      parseIntDefault(envMap["MAX_PLAYERS"], 20),
			Memory:          memoryMB,
			JavaVersion:     javaVer,
			DockerImage:     imageTag,
			DataPath:        dataPath,
			Detached:        true, // Mark adopted servers detached so DiscoPanel shutdown never stops them
			AutoStart:       true,
			TPSCommand:      minecraft.GetTPSCommand(modLoader),
			AdditionalPorts: additionalPorts,
		}

		if err := store.CreateServer(ctx, newServer); err != nil {
			log.Warn("[PETABLOCKS] Failed to adopt container %s (%s): %v", containerName, c.ID[:12], err)
			continue
		}

		// Populate ServerConfig from container env vars
		if cfg, err := store.GetServerConfig(ctx, newServer.ID); err == nil && cfg != nil {
			populateServerConfigFromEnv(cfg, envMap, memoryMB)
			_ = store.SaveServerConfig(ctx, cfg)
		}

		log.Info("[PETABLOCKS] Adopted existing container %s as server '%s' (ID=%s, port=%d, loader=%s, mc=%s, path=%s)",
			containerName, newServer.Name, newServer.ID, newServer.Port, newServer.ModLoader, newServer.MCVersion, newServer.DataPath)

		res.Adopted = append(res.Adopted, AdoptedServerInfo{
			ID:            newServer.ID,
			Name:          newServer.Name,
			ContainerID:   c.ID,
			ContainerName: containerName,
			DataPath:      newServer.DataPath,
			Port:          newServer.Port,
			ModLoader:     string(newServer.ModLoader),
			MCVersion:     newServer.MCVersion,
			MemoryMB:      newServer.Memory,
			Status:        string(newServer.Status),
		})
	}

	return res, nil
}

// NewAdoptHandler exposes POST / GET /api/v1/petablocks/adopt to trigger container discovery on demand.
func NewAdoptHandler(store *storage.Store, dockerClient *docker.Client, log *logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		token := strings.TrimPrefix(strings.TrimPrefix(authHeader, "Bearer "), "bearer ")
		if !ValidateInternalToken(token) && os.Getenv("DISCOPANEL_INTERNAL_API_TOKEN") != "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		result, err := DiscoverAndAdoptContainers(r.Context(), store, dockerClient, log)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}
}

func isMinecraftOrProxyContainer(name string, inspect container.InspectResponse) bool {
	if _, known := KnownPETABLOCKSContainers[name]; known {
		return true
	}
	img := strings.ToLower(inspect.Config.Image)
	if strings.Contains(img, "itzg/minecraft-server") ||
		strings.Contains(img, "itzg/mc-proxy") ||
		strings.Contains(img, "itzg/bungeecord") ||
		strings.Contains(img, "velocity") ||
		strings.Contains(img, "minecraft") {
		return true
	}
	lowerName := strings.ToLower(name)
	if strings.HasPrefix(lowerName, "petablocks-") || strings.HasPrefix(lowerName, "pb-") {
		// Check if mounted to /data or has EULA=TRUE
		for _, e := range inspect.Config.Env {
			if strings.HasPrefix(e, "EULA=") || strings.HasPrefix(e, "SERVER_PORT=") || strings.HasPrefix(e, "TYPE=") {
				return true
			}
		}
		for _, m := range inspect.Mounts {
			if m.Destination == "/data" || m.Destination == "/server" {
				return true
			}
		}
	}
	return false
}

func parseEnvSlice(envs []string) map[string]string {
	m := make(map[string]string, len(envs))
	for _, e := range envs {
		if idx := strings.IndexByte(e, '='); idx > 0 {
			m[e[:idx]] = e[idx+1:]
		}
	}
	return m
}

func extractDataMountPath(inspect container.InspectResponse) string {
	for _, m := range inspect.Mounts {
		if m.Destination == "/data" || m.Destination == "/server" {
			return m.Source
		}
	}
	return ""
}

func extractPorts(inspect container.InspectResponse, envMap map[string]string) (int, []*v1.AdditionalPort) {
	mainPort := 0
	var additional []*v1.AdditionalPort

	if sp, err := strconv.Atoi(envMap["SERVER_PORT"]); err == nil && sp > 0 {
		mainPort = sp
	}

	if inspect.HostConfig != nil && inspect.HostConfig.PortBindings != nil {
		for portProto, bindings := range inspect.HostConfig.PortBindings {
			if len(bindings) == 0 {
				continue
			}
			hostPort, err := strconv.Atoi(bindings[0].HostPort)
			if err != nil || hostPort <= 0 {
				continue
			}
			containerPort := portProto.Int()
			proto := portProto.Proto()

			if containerPort == 25575 {
				continue
			}
			if mainPort == 0 && (containerPort == 25565 || containerPort == 25577 || hostPort >= 11600) {
				mainPort = hostPort
				continue
			}
			if hostPort != mainPort {
				additional = append(additional, &v1.AdditionalPort{
					Name:          portProto.Port(),
					ContainerPort: int32(containerPort),
					HostPort:      int32(hostPort),
					Protocol:      proto,
				})
			}
		}
	}

	if mainPort == 0 {
		mainPort = 25565
	}
	return mainPort, additional
}

func detectModLoader(name, image string, envMap map[string]string) storage.ModLoader {
	t := strings.ToUpper(envMap["TYPE"])
	switch t {
	case "NEOFORGE":
		return storage.ModLoaderNeoForge
	case "FABRIC":
		return storage.ModLoaderFabric
	case "FORGE":
		return storage.ModLoaderForge
	case "PAPER":
		return storage.ModLoaderPaper
	case "PURPUR":
		return storage.ModLoaderPurpur
	case "SPIGOT":
		return storage.ModLoaderSpigot
	case "AUTO_CURSEFORGE":
		return storage.ModLoaderAutoCurseForge
	case "MODRINTH":
		return storage.ModLoaderModrinth
	case "CUSTOM":
		customJar := strings.ToLower(envMap["CUSTOM_SERVER"])
		if strings.Contains(customJar, "paper") {
			return storage.ModLoaderPaper
		}
		if strings.Contains(customJar, "neoforge") {
			return storage.ModLoaderNeoForge
		}
		if strings.Contains(customJar, "fabric") {
			return storage.ModLoaderFabric
		}
		return storage.ModLoaderCustom
	}

	lowerName := strings.ToLower(name)
	if strings.Contains(lowerName, "create") || strings.Contains(lowerName, "neoforge") || strings.Contains(lowerName, "s2") {
		return storage.ModLoaderNeoForge
	}
	if strings.Contains(lowerName, "fabric") || strings.Contains(lowerName, "modpack") {
		return storage.ModLoaderFabric
	}
	if strings.Contains(lowerName, "paper") || strings.Contains(lowerName, "lobby") {
		return storage.ModLoaderPaper
	}
	return storage.ModLoaderVanilla
}

func detectMCVersion(envMap map[string]string) string {
	if v := envMap["VERSION"]; v != "" && strings.ToUpper(v) != "LATEST" {
		return v
	}
	if customJar := envMap["CUSTOM_SERVER"]; customJar != "" {
		if strings.Contains(customJar, "1.21.4") {
			return "1.21.4"
		}
		if strings.Contains(customJar, "1.21.1") {
			return "1.21.1"
		}
		if strings.Contains(customJar, "1.20.1") {
			return "1.20.1"
		}
	}
	return "1.21.1"
}

func detectMemoryMB(inspect container.InspectResponse, envMap map[string]string) int {
	for _, key := range []string{"MEMORY", "MAX_MEMORY", "INIT_MEMORY"} {
		val := strings.TrimSpace(strings.ToUpper(envMap[key]))
		if val == "" {
			continue
		}
		if strings.HasSuffix(val, "G") {
			if g, err := strconv.Atoi(strings.TrimSuffix(val, "G")); err == nil && g > 0 {
				return g * 1024
			}
		}
		if strings.HasSuffix(val, "M") {
			if m, err := strconv.Atoi(strings.TrimSuffix(val, "M")); err == nil && m > 0 {
				return m
			}
		}
	}
	if inspect.HostConfig != nil && inspect.HostConfig.Memory > 0 {
		return int(inspect.HostConfig.Memory / (1024 * 1024))
	}
	return 8192
}

func mapContainerState(state *container.State) storage.ServerStatus {
	if state == nil {
		return storage.StatusStopped
	}
	if state.Running {
		return storage.StatusRunning
	}
	if state.Restarting {
		return storage.StatusStarting
	}
	return storage.StatusStopped
}

func extractImageTag(image string) string {
	if idx := strings.LastIndexByte(image, ':'); idx != -1 && idx < len(image)-1 {
		return image[idx+1:]
	}
	return "latest"
}

func sanitizeID(name string) string {
	s := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(name, "petablocks-"), "pb-"))
	s = strings.ReplaceAll(s, "_", "-")
	if s == "" {
		return name
	}
	return s
}

func formatFriendlyName(containerName string) string {
	parts := strings.FieldsFunc(containerName, func(r rune) bool {
		return r == '-' || r == '_'
	})
	for i, p := range parts {
		if strings.EqualFold(p, "pb") || strings.EqualFold(p, "petablocks") {
			parts[i] = "PETABLOCKS"
		} else if strings.EqualFold(p, "smp") {
			parts[i] = "SMP"
		} else if strings.EqualFold(p, "s2") {
			parts[i] = "Season 2"
		} else if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func parseIntDefault(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil && v > 0 {
		return v
	}
	return def
}

func populateServerConfigFromEnv(cfg *storage.ServerConfig, envMap map[string]string, memoryMB int) {
	strPtr := func(s string) *string { return &s }
	intPtr := func(i int) *int { return &i }
	boolPtr := func(b bool) *bool { return &b }

	if mem := envMap["MEMORY"]; mem != "" {
		cfg.Memory = strPtr(mem)
		cfg.MaxMemory = strPtr(mem)
	} else {
		memStr := strconv.Itoa(memoryMB) + "M"
		cfg.Memory = strPtr(memStr)
		cfg.MaxMemory = strPtr(memStr)
	}

	if motd := envMap["MOTD"]; motd != "" {
		cfg.MOTD = strPtr(motd)
	}
	if rconPass := envMap["RCON_PASSWORD"]; rconPass != "" {
		cfg.RCONPassword = strPtr(rconPass)
		cfg.EnableRCON = boolPtr(true)
	}
	if rconPort, err := strconv.Atoi(envMap["RCON_PORT"]); err == nil && rconPort > 0 {
		cfg.RCONPort = intPtr(rconPort)
	}
	if cfUrl := envMap["CF_PAGE_URL"]; cfUrl != "" {
		cfg.CFPageURL = strPtr(cfUrl)
	}
	if cfSlug := envMap["CF_SLUG"]; cfSlug != "" {
		cfg.CFSlug = strPtr(cfSlug)
	}
	if mrPack := envMap["MODRINTH_MODPACK"]; mrPack != "" {
		cfg.ModrinthModpack = strPtr(mrPack)
	}
	if jvmXx := envMap["JVM_XX_OPTS"]; jvmXx != "" {
		cfg.JVMXXOpts = strPtr(jvmXx)
	}
}
