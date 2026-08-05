package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"logmara/db"
	"logmara/model"

	"github.com/gin-gonic/gin"
)

// The Health tab talks to a docker-socket-proxy sidecar (see docker-compose.yml
// / docker-stack.app.yml, "docker-proxy" service) instead of mounting
// /var/run/docker.sock into the api container directly: the proxy exposes a
// read-only, allow-listed subset of the Docker Engine API (containers,
// services, tasks, info - no exec/attach/POST), so a bug or compromise in
// this handler can't be turned into host-level control the way a raw socket
// mount would allow.
//
// In single-server deployments (docker-compose.yml) the proxy sees every
// container on that one host, which is the complete picture. In Swarm
// (docker-stack.app.yml) it's placed on manager nodes only (node.role ==
// manager) - Swarm's /services and /tasks endpoints return cluster-wide
// state from any manager regardless of which node runs api, so this also
// covers nodes api itself never runs on (e.g. the Postgres/Redis managers).
// A container-level (non-swarm) /containers/json call, by contrast, is
// always scoped to whichever single node answers it - fine for the
// single-server case (one node total) but only ever "one manager's view" in
// Swarm, which is why Swarm mode prefers /services+/tasks instead.
func dockerProxyBaseURL() string {
	if v := os.Getenv("DOCKER_PROXY_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://docker-proxy:2375"
}

var dockerProxyClient = &http.Client{Timeout: 4 * time.Second}

func dockerProxyGET(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dockerProxyBaseURL()+path, nil)
	if err != nil {
		return err
	}
	resp, err := dockerProxyClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker proxy %s returned status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type ContainerHealth struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`
	Status string `json:"status"`
	Health string `json:"health,omitempty"`
	Node   string `json:"node,omitempty"`
}

type ServiceHealth struct {
	Name            string            `json:"name"`
	Mode            string            `json:"mode"`
	Image           string            `json:"image"`
	ReplicasDesired int               `json:"replicas_desired"`
	ReplicasRunning int               `json:"replicas_running"`
	OverallState    string            `json:"overall_state"`
	NodeNames       []string          `json:"node_names"`
	Tasks           []ContainerHealth `json:"tasks"`
}

type RelayHealth struct {
	Label            string  `json:"label"`
	IPAddress        string  `json:"ip_address"`
	CertStatus       string  `json:"cert_status"`
	LastSeen         *string `json:"last_seen,omitempty"`
	SecondsSinceSeen *int64  `json:"seconds_since_seen,omitempty"`
	Status           string  `json:"status"`
}

type ContainersHealthResponse struct {
	DockerAvailable bool              `json:"docker_available"`
	Mode            string            `json:"mode"`
	Scope           string            `json:"scope"`
	Containers      []ContainerHealth `json:"containers,omitempty"`
	Services        []ServiceHealth   `json:"services,omitempty"`
	Relays          []RelayHealth     `json:"relays"`
	Message         string            `json:"message,omitempty"`
	RefreshedAt     string            `json:"refreshed_at"`
}

// relayStaleAfter is how long without a log from a whitelisted relay IP
// before it's reported "stale" instead of "online". mv_device_stats.last_seen
// itself already lags real time by up to the MV refresh interval (30s while
// someone's logged in - see main.go's fast refresh ticker - otherwise up to
// mv_refresh_interval_min), so this stays well above that to avoid false
// positives from refresh lag alone.
const relayStaleAfter = 10 * time.Minute

var healthDockerStatusRe = regexp.MustCompile(`\(([a-z: ]+)\)\s*$`)

// GetContainersHealth backs the Admin > Health tab: container/service status
// from the docker-socket-proxy sidecar (best-effort - docker_available:false
// with an explanatory message if it isn't reachable, never a hard error,
// since this tab is diagnostic rather than load-bearing) plus relay liveness
// derived from data the app already has (last_seen, certificate status),
// since the relay host itself isn't reachable this way at all (see the
// "relays" section below and README "Syslog Relay" / "Health").
func GetContainersHealth(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		defer cancel()

		resp := ContainersHealthResponse{
			RefreshedAt: time.Now().UTC().Format(time.RFC3339),
		}

		var infoOut struct {
			Swarm struct {
				LocalNodeState   string `json:"LocalNodeState"`
				ControlAvailable bool   `json:"ControlAvailable"`
			} `json:"Swarm"`
		}
		if err := dockerProxyGET(ctx, "/info", &infoOut); err != nil {
			resp.DockerAvailable = false
			resp.Message = "Docker proxy unreachable - the docker-proxy sidecar isn't running or isn't configured. See README \"Health\"."
			resp.Relays = fetchRelayHealth(database)
			c.JSON(http.StatusOK, resp)
			return
		}
		resp.DockerAvailable = true

		if infoOut.Swarm.LocalNodeState == "active" && infoOut.Swarm.ControlAvailable {
			resp.Mode = "swarm"
			resp.Scope = "cluster"
			services, err := fetchSwarmServices(ctx)
			if err != nil {
				resp.Message = fmt.Sprintf("Failed to read Swarm service status: %v", err)
			} else {
				resp.Services = services
			}
		} else if infoOut.Swarm.LocalNodeState == "active" {
			// Reachable, but this proxy sits on a worker, not a manager (a
			// misconfiguration for the swarm stack, which places it with
			// node.role==manager) - /services and /tasks would fail with
			// "node is not a manager", so fall back to this one node's own
			// containers rather than erroring out entirely.
			resp.Mode = "swarm"
			resp.Scope = "node"
			resp.Message = "docker-proxy is running on a Swarm worker, not a manager - showing only this node's containers. Move it to a manager node for cluster-wide status (see docker-stack.app.yml)."
			containers, err := fetchLocalContainers(ctx)
			if err != nil {
				resp.Message += fmt.Sprintf(" (also failed to list local containers: %v)", err)
			} else {
				resp.Containers = containers
			}
		} else {
			resp.Mode = "single"
			resp.Scope = "cluster"
			containers, err := fetchLocalContainers(ctx)
			if err != nil {
				resp.Message = fmt.Sprintf("Failed to list containers: %v", err)
			} else {
				resp.Containers = containers
			}
		}

		resp.Relays = fetchRelayHealth(database)
		c.JSON(http.StatusOK, resp)
	}
}

func fetchLocalContainers(ctx context.Context) ([]ContainerHealth, error) {
	var raw []struct {
		Names  []string `json:"Names"`
		Image  string   `json:"Image"`
		State  string   `json:"State"`
		Status string   `json:"Status"`
	}
	if err := dockerProxyGET(ctx, "/containers/json?all=true", &raw); err != nil {
		return nil, err
	}

	out := make([]ContainerHealth, 0, len(raw))
	for _, ct := range raw {
		name := ct.Image
		if len(ct.Names) > 0 {
			name = strings.TrimPrefix(ct.Names[0], "/")
		}
		out = append(out, ContainerHealth{
			Name:   name,
			Image:  ct.Image,
			State:  ct.State,
			Status: ct.Status,
			Health: parseDockerHealth(ct.Status),
		})
	}
	return out, nil
}

// parseDockerHealth pulls "healthy"/"unhealthy"/"health: starting" out of the
// trailing "(...)" Docker already appends to a container's Status text when
// it has a HEALTHCHECK defined (e.g. "Up 2 hours (healthy)") - the
// /containers/json list endpoint doesn't expose a separate structured health
// field, only /containers/<id>/json (inspect) does, and inspecting every
// container individually isn't worth the extra round trips just for this.
func parseDockerHealth(status string) string {
	m := healthDockerStatusRe.FindStringSubmatch(status)
	if m == nil {
		return ""
	}
	v := strings.TrimSpace(m[1])
	if v == "healthy" || v == "unhealthy" || strings.HasPrefix(v, "health:") {
		return v
	}
	return ""
}

func computeServiceState(states map[string]int, tasks []ContainerHealth) string {
	if len(tasks) == 0 {
		return "none"
	}
	total := 0
	running := 0
	for state, count := range states {
		total += count
		if state == "running" {
			running += count
		}
	}
	if running == 0 {
		return "degraded"
	}
	if running == total {
		return "running"
	}
	return "partial"
}

func fetchSwarmServices(ctx context.Context) ([]ServiceHealth, error) {
	var svcs []struct {
		ID   string `json:"ID"`
		Spec struct {
			Name string `json:"Name"`
			Mode struct {
				Replicated *struct {
					Replicas int `json:"Replicas"`
				} `json:"Replicated"`
				Global *struct{} `json:"Global"`
			} `json:"Mode"`
			TaskTemplate struct {
				ContainerSpec struct {
					Image string `json:"Image"`
				} `json:"ContainerSpec"`
			} `json:"TaskTemplate"`
		} `json:"Spec"`
	}
	if err := dockerProxyGET(ctx, "/services", &svcs); err != nil {
		return nil, err
	}

	var nodes []struct {
		ID          string `json:"ID"`
		Description struct {
			Hostname string `json:"Hostname"`
		} `json:"Description"`
	}
	_ = dockerProxyGET(ctx, "/nodes", &nodes) // best-effort; falls back to node IDs
	nodeNames := make(map[string]string, len(nodes))
	for _, n := range nodes {
		nodeNames[n.ID] = n.Description.Hostname
	}

	var tasks []struct {
		ID           string `json:"ID"`
		ServiceID    string `json:"ServiceID"`
		NodeID       string `json:"NodeID"`
		DesiredState string `json:"DesiredState"`
		Status       struct {
			State           string `json:"State"`
			Message         string `json:"Message"`
			ContainerStatus *struct {
				ContainerID string `json:"ContainerID"`
			} `json:"ContainerStatus"`
		} `json:"Status"`
		Spec struct {
			ContainerSpec struct {
				Image string `json:"Image"`
			} `json:"ContainerSpec"`
		} `json:"Spec"`
	}
	if err := dockerProxyGET(ctx, "/tasks", &tasks); err != nil {
		return nil, err
	}

	tasksByService := make(map[string][]ContainerHealth)
	stateCountByService := make(map[string]map[string]int)
	nodeSetByService := make(map[string]map[string]struct{})
	for _, t := range tasks {
		if t.DesiredState != "running" {
			continue
		}
		node := nodeNames[t.NodeID]
		if node == "" {
			node = t.NodeID
			if len(node) > 12 {
				node = node[:12]
			}
		}
		tasksByService[t.ServiceID] = append(tasksByService[t.ServiceID], ContainerHealth{
			Name:   node,
			Image:  t.Spec.ContainerSpec.Image,
			State:  t.Status.State,
			Status: t.Status.Message,
			Node:   node,
		})
		if stateCountByService[t.ServiceID] == nil {
			stateCountByService[t.ServiceID] = make(map[string]int)
		}
		stateCountByService[t.ServiceID][t.Status.State]++
		if nodeSetByService[t.ServiceID] == nil {
			nodeSetByService[t.ServiceID] = make(map[string]struct{})
		}
		nodeSetByService[t.ServiceID][node] = struct{}{}
	}

	out := make([]ServiceHealth, 0, len(svcs))
	for _, s := range svcs {
		mode := "replicated"
		desired := 0
		if s.Spec.Mode.Global != nil {
			mode = "global"
			desired = len(tasksByService[s.ID])
		} else if s.Spec.Mode.Replicated != nil {
			desired = s.Spec.Mode.Replicated.Replicas
		}

		overallState := computeServiceState(stateCountByService[s.ID], tasksByService[s.ID])

		var nodeNamesList []string
		for n := range nodeSetByService[s.ID] {
			nodeNamesList = append(nodeNamesList, n)
		}

		out = append(out, ServiceHealth{
			Name:            s.Spec.Name,
			Mode:            mode,
			Image:           s.Spec.TaskTemplate.ContainerSpec.Image,
			ReplicasDesired: desired,
			ReplicasRunning: stateCountByService[s.ID]["running"],
			OverallState:    overallState,
			NodeNames:       nodeNamesList,
			Tasks:           tasksByService[s.ID],
		})
	}
	return out, nil
}

// relayHeartbeatLastSeen reads back the mtime of the per-relay heartbeat
// files rsyslog/syslog.conf's relayAccept ruleset touches on the shared
// /data volume (see relayHeartbeatPath) - whichever of ips have never
// connected simply have no file yet and are absent from the result, not an
// error, same contract as the db.GetLastSeenByIPs this replaced.
func relayHeartbeatLastSeen(ips []string) map[string]time.Time {
	result := make(map[string]time.Time, len(ips))
	for _, ip := range ips {
		path := relayHeartbeatPath(ip)
		if path == "" {
			continue
		}
		if info, err := os.Stat(path); err == nil {
			result[ip] = info.ModTime()
		}
	}
	return result
}

// fetchRelayHealth can't reach into the relay host at all (see the package
// doc comment in relay.go: it lives in a separate, untrusted client VLAN
// with only outbound 6515/tcp to the central server allowed - nothing here
// can dial back in, and it isn't on syslog_net or in the Swarm). Instead
// this reports the best proxy signal already on hand: whether logs are
// still arriving from its whitelisted IP, and whether its certificate is
// still valid.
func fetchRelayHealth(database *sql.DB) []RelayHealth {
	entries, err := db.GetRelayWhitelist(database)
	if err != nil || len(entries) == 0 {
		return []RelayHealth{}
	}

	ips := make([]string, len(entries))
	for i, e := range entries {
		ips[i] = e.IPAddress
	}
	lastSeen := relayHeartbeatLastSeen(ips)

	certStatus := make(map[int64]string)
	if certs, err := db.GetRelayCertificates(database); err == nil {
		for _, cert := range certs {
			certStatus[cert.ID] = cert.Status
		}
	}

	now := time.Now()
	out := make([]RelayHealth, 0, len(entries))
	for _, e := range entries {
		rh := RelayHealth{
			Label:      e.Label,
			IPAddress:  e.IPAddress,
			CertStatus: "none",
		}
		if e.RelayCertID != nil {
			if st, ok := certStatus[*e.RelayCertID]; ok {
				rh.CertStatus = st
			}
		}

		seen, ok := lastSeen[e.IPAddress]
		switch {
		case rh.CertStatus == model.RelayCertStatusRevoked:
			rh.Status = "cert_revoked"
		case !ok:
			rh.Status = "never_seen"
		default:
			ts := seen.UTC().Format(time.RFC3339)
			secs := int64(now.Sub(seen).Seconds())
			rh.LastSeen = &ts
			rh.SecondsSinceSeen = &secs
			if now.Sub(seen) <= relayStaleAfter {
				rh.Status = "online"
			} else {
				rh.Status = "stale"
			}
		}
		out = append(out, rh)
	}
	return out
}
