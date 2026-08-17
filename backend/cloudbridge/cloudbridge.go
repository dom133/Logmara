// Package cloudbridge is the Local Agent side of the Logmara Cloud
// mobile-access tunnel (see the Logmara Cloud repo's
// docs/architecture.md for the full design - this package is "the
// on-prem binary that dials in", referenced there as not-yet-existing at
// the time that doc was written).
//
// Entirely opt-in and off by default: gated by the CLOUD_BRIDGE_ENABLED
// env var, deliberately an env var and not a database-backed setting
// like relay_ingestion_enabled - this is "does this installation's
// operator want the cloud feature area to exist at all", decided once at
// deploy time, not a day-to-day admin toggle (same category as
// RELAY_CENTRAL_HOST, not relay_ingestion_enabled - see backend/db/db.go's
// envSettings for that distinction elsewhere in this codebase).
//
// When enabled, pairing itself is still an explicit admin action: an
// admin pastes a one-time pairing link (from Logmara Cloud's "Activate
// Cloud Bridge") into Admin > Cloud Bridge and submits it - see
// EnrollWithLink, called from handler.SubmitCloudBridgeLink. While paired,
// instance_id/broker_host are immutable - there's no path that changes
// them in place, only reconnecting with the same identity after a restart
// (see Start). The admin can still leave Cloud Bridge entirely via
// Disconnect, which deletes the identity outright so a later pairing
// starts fresh rather than reusing it.
//
// Single-server vs HA. An Agent is created per api replica. When the
// deployment has no shared state (sharedClient is nil - the single-server
// docker-compose path), the Agent runs the original single-process
// behavior: it dials the tunnel itself and keeps the connected flag in
// memory. When shared state is present (HA, docker-stack path), the Agent
// instead joins a Redis-backed coordination so that exactly one replica in
// the pool holds the broker tunnel at a time (leader election, see
// leaderLoop), the connected status is shared across replicas (see
// connectedNow / publishConnected), and an admin action handled by any
// replica (pairing, certificate save, disconnect) reaches the leader via a
// control broadcast (see publishControl / subscribeControl) instead of
// only taking effect on the replica that happened to receive the request.
// This mirrors the codebase's existing HA primitives in backend/sharedstate
// (LeaderElector, Broadcaster) and the tailer/rotation leader patterns.
package cloudbridge

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"logmara/db"
	"logmara/model"
	"logmara/sharedstate"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

// Enabled reports whether CLOUD_BRIDGE_ENABLED=true is set. Read live
// (not cached at startup) everywhere it's checked, so e.g. /auth/me
// always reflects the actual running configuration.
func Enabled() bool {
	return os.Getenv("CLOUD_BRIDGE_ENABLED") == "true"
}

// LockCertificates is true by default - for deployments that don't want
// an admin ever seeing or swapping the raw mTLS client key through the
// UI/API. Set CLOUD_BRIDGE_LOCK_CERTIFICATES=false to disable: the admin
// will then be able to review and replace certificates via the UI. When
// locked, EnrollWithLink persists and connects the certificates Logmara
// Cloud hands back immediately server-side instead of returning them to
// the caller for review, and SaveCertificates refuses any further call
// outright (there is then no repair/replace path at all - Disconnect +
// re-pairing is the only way to get new certificates). Read live, same
// as Enabled.
func LockCertificates() bool {
	return strings.ToLower(os.Getenv("CLOUD_BRIDGE_LOCK_CERTIFICATES")) != "false"
}

const (
	// Tunnel / reconnect tuning (single- and HA-mode alike).
	reconnectBaseDelay = 2 * time.Second
	reconnectMaxDelay  = 60 * time.Second
	pingPeriod         = 30 * time.Second
	readDeadline       = 45 * time.Second

	// HA coordination (only used when a shared client is configured).
	leaderKey      = "cloudbridge"
	leaderTTL      = 30 * time.Second
	statusKey      = "cloudbridge:connected"
	statusKeyTTL   = 90 * time.Second
	controlChannel = "cloudbridge:control"
	controlStop    = "stop"
	controlRefresh = "refresh"
	redisOpTimeout = 2 * time.Second
)

// HA coordination timing knobs. Variables (not constants) so tests can
// shrink them to keep election/handover scenarios fast; production values
// keep the leader well within leaderTTL and let standbys react promptly.
var (
	leaderRenewPeriod = 15 * time.Second
	standbyPoll       = 10 * time.Second
	watcherPeriod     = 10 * time.Second
)

// stateStore isolates this package's Postgres access behind a small
// interface so the coordination logic can be exercised in tests without a
// live database (see the fake store in cloudbridge_test.go). In production
// it's always the poolStore wrapping the deployment's dynamic pool.
type stateStore interface {
	GetCloudBridgeState() (*model.CloudBridgeState, error)
	SaveCloudBridgeState(instanceID, brokerHost string) error
	UpdateCloudBridgeCertificates(caCert, clientCert, clientKey string) error
	DeleteCloudBridgeState() error
}

// poolStore is the production stateStore - a thin adapter over the
// package's db.* helpers bound to a dynamic pool.
type poolStore struct {
	pool *db.DynamicPool
}

func (s poolStore) GetCloudBridgeState() (*model.CloudBridgeState, error) {
	return db.GetCloudBridgeState(s.pool.Get())
}

func (s poolStore) SaveCloudBridgeState(instanceID, brokerHost string) error {
	return db.SaveCloudBridgeState(s.pool.Get(), instanceID, brokerHost)
}

func (s poolStore) UpdateCloudBridgeCertificates(caCert, clientCert, clientKey string) error {
	return db.UpdateCloudBridgeCertificates(s.pool.Get(), caCert, clientCert, clientKey)
}

func (s poolStore) DeleteCloudBridgeState() error {
	return db.DeleteCloudBridgeState(s.pool.Get())
}

// Agent is one api replica's Cloud Bridge. The exported package-level
// functions (Start, EnrollWithLink, SaveCertificates, Disconnect,
// CurrentStatus) delegate to the single default Agent created by Start -
// see the package doc for the single-server vs HA split.
type Agent struct {
	store stateStore

	// id uniquely identifies this replica; it is stamped into the shared
	// connected-status key so a stepping-down leader only ever clears the
	// status it set itself (see publishConnected / statusDeleteIfMatch).
	id string

	// shared is nil for single-server deployments (no Redis), in which case
	// this Agent runs the original single-process behavior. Non-nil in HA,
	// where it coordinates leadership, shared status, and control broadcasts
	// with the other api replicas.
	shared      *sharedstate.Client
	broadcaster *sharedstate.Broadcaster

	// connMu guards connected - the live tunnel state for this replica.
	connMu    sync.RWMutex
	connected bool

	// tunnelMu guards tunnelCancel + tunnelState + tunnelActive - the
	// runTunnel goroutine this replica currently has active (if any) and
	// the state it was started with.
	tunnelMu     sync.Mutex
	tunnelCancel context.CancelFunc
	tunnelState  model.CloudBridgeState
	tunnelActive bool

	// leadership (HA only)
	elector  *sharedstate.LeaderElector
	isLeader atomic.Bool
}

var (
	agentMu      sync.RWMutex
	defaultAgent *Agent
)

func setDefaultAgent(a *Agent) {
	agentMu.Lock()
	defaultAgent = a
	agentMu.Unlock()
}

func getDefaultAgent() *Agent {
	agentMu.RLock()
	defer agentMu.RUnlock()
	return defaultAgent
}

// newAgent builds an Agent with a fresh identity and (in HA) its control
// broadcaster. Shared by Start and the tests.
func newAgent(store stateStore, shared *sharedstate.Client) *Agent {
	a := &Agent{store: store, shared: shared, id: uuid.NewString()}
	if shared != nil {
		a.broadcaster = sharedstate.NewBroadcaster(shared)
	}
	return a
}

// Start wires this package to the running instance and begins Cloud Bridge
// for this replica. Call exactly once from main(), only when Enabled().
//
// p is the shared dynamic Postgres pool (holds the persisted identity);
// shared is the deployment's shared-state client, or nil for a single-server
// deployment (see the package doc for the behavior split).
func Start(ctx context.Context, p *db.DynamicPool, shared *sharedstate.Client) {
	a := newAgent(poolStore{pool: p}, shared)
	setDefaultAgent(a)
	a.Start(ctx)
}

// Start begins Cloud Bridge for this replica for the lifetime of ctx. In
// single-server mode (shared nil) it keeps the original behavior: if already
// paired with certificates, reconnect immediately - there is no peer to
// coordinate with, so no election and no shared status. In HA mode it joins
// the leader election (see leaderLoop) and subscribes to the control channel
// (see subscribeControl) so an admin action handled by any replica reaches
// the leader. Exposed as a method so tests can drive several replicas in one
// process; the package-level Start above is the production entry point.
func (a *Agent) Start(ctx context.Context) {
	if a.shared == nil {
		a.bootSingle()
		return
	}
	go a.subscribeControl(ctx)
	go a.leaderLoop(ctx)
}

// bootSingle is the original single-process boot path: load any saved
// identity and, if it has certificates, reconnect the tunnel right away.
func (a *Agent) bootSingle() {
	state, err := a.store.GetCloudBridgeState()
	if err == sql.ErrNoRows {
		slog.Info("cloudbridge: enabled, not yet paired - waiting for a pairing link via Admin > Cloud Bridge")
		return
	}
	if err != nil {
		slog.Error("cloudbridge: failed to load saved state", "error", err)
		return
	}
	if state.CACert == "" {
		slog.Info("cloudbridge: paired but no certificates configured yet - waiting for a certificate save via Admin > Cloud Bridge", "instance_id", state.InstanceID)
		return
	}
	slog.Info("cloudbridge: reconnecting with existing identity", "instance_id", state.InstanceID)
	a.startTunnel(*state)
}

// leaderLoop is the HA control loop for this replica: try to become the
// cloud bridge leader, and while leader, keep the tunnel converged on the
// persisted identity (reconcile) and renew the leadership lock. When not
// leader, poll in standby until the lock is free. Modeled on the tailer's
// leader election (backend/tailer/tailer.go) and the rotation leader
// (backend/main.go), both of which use sharedstate.LeaderElector.
func (a *Agent) leaderLoop(ctx context.Context) {
	a.elector = sharedstate.NewLeaderElector(a.shared, leaderKey, leaderTTL)
	for {
		if ctx.Err() != nil {
			return
		}
		if !a.elector.Acquire(ctx) {
			// Another replica already holds leadership - wait and retry.
			select {
			case <-ctx.Done():
				return
			case <-time.After(standbyPoll):
			}
			continue
		}

		a.isLeader.Store(true)
		slog.Info("cloudbridge: this replica is the cloud bridge leader")
		leadCtx, cancel := context.WithCancel(ctx)

		// Converge on whatever identity is already persisted (e.g. a
		// restart of the leader, or a pairing that landed while no leader
		// was up), then keep converging + renewing until we step down.
		a.reconcile()
		a.runAsLeader(leadCtx)

		cancel()
		a.isLeader.Store(false)
		// Step down: stop our tunnel before releasing the lock so there is
		// at most a brief overlap with the next leader's tunnel.
		a.stopTunnel()
		a.setConnected(false)
		a.elector.Release(context.Background())
	}
}

// runAsLeader runs the periodic renew + reconcile loop while this replica
// is leader. Returns when leadership is definitively lost (renew reports
// lost=true) or the context is cancelled.
func (a *Agent) runAsLeader(ctx context.Context) {
	renewTicker := time.NewTicker(leaderRenewPeriod)
	defer renewTicker.Stop()
	reconcileTicker := time.NewTicker(watcherPeriod)
	defer reconcileTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-renewTicker.C:
			ok, lost := a.elector.Renew(ctx)
			if !ok && lost {
				slog.Warn("cloudbridge: lost leader lock, stepping down")
				return
			}
			if !ok {
				slog.Warn("cloudbridge: leader lock renew error (transient)")
			}
		case <-reconcileTicker.C:
			a.reconcile()
		}
	}
}

// reconcile converges this replica's tunnel on the persisted identity:
// start it when there's a paired identity with certificates (restarting it
// if the saved state changed underneath, e.g. a certificate replacement),
// and stop it when the identity is gone or has no certificates yet. The
// leader calls this on a timer and in response to a "refresh" control
// broadcast, so pairing/certificate changes performed on any replica take
// effect here without a restart.
func (a *Agent) reconcile() {
	state, err := a.store.GetCloudBridgeState()
	switch {
	case err == sql.ErrNoRows:
		a.stopTunnel()
	case err != nil:
		slog.Warn("cloudbridge: reconcile: failed to load saved state", "error", err)
	case state.CACert == "":
		a.stopTunnel()
	default:
		if !a.tunnelRunning() || !a.tunnelStateMatches(*state) {
			slog.Info("cloudbridge: starting tunnel", "instance_id", state.InstanceID, "broker_host", state.BrokerHost)
			a.startTunnel(*state)
		}
	}
}

func (a *Agent) tunnelRunning() bool {
	a.tunnelMu.Lock()
	defer a.tunnelMu.Unlock()
	return a.tunnelActive
}

func (a *Agent) tunnelStateMatches(state model.CloudBridgeState) bool {
	a.tunnelMu.Lock()
	defer a.tunnelMu.Unlock()
	s := a.tunnelState
	return s.InstanceID == state.InstanceID &&
		s.BrokerHost == state.BrokerHost &&
		s.CACert == state.CACert &&
		s.ClientCert == state.ClientCert &&
		s.ClientKey == state.ClientKey
}

// subscribeControl listens for control broadcasts for the lifetime of ctx.
// Every HA replica runs it; a message only has an effect on the replica it
// names for (the leader acts on "refresh"; any replica acting on "stop"
// just makes sure it has no tunnel, which is already true for non-leaders).
func (a *Agent) subscribeControl(ctx context.Context) {
	a.broadcaster.Subscribe(ctx, controlChannel, func(payload string) {
		switch payload {
		case controlStop:
			slog.Info("cloudbridge: received stop broadcast")
			a.stopTunnel()
			a.setConnected(false)
		case controlRefresh:
			if a.isLeader.Load() {
				slog.Info("cloudbridge: received refresh broadcast, reconciling")
				a.reconcile()
			}
		}
	})
}

// publishControl broadcasts a control message to every HA replica. No-op in
// single-server mode.
func (a *Agent) publishControl(msg string) {
	if a.shared == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()
	if err := a.broadcaster.Publish(ctx, controlChannel, msg); err != nil {
		slog.Warn("cloudbridge: failed to publish control message", "msg", msg, "error", err)
	}
}

// statusDeleteIfMatch clears the shared status key only if it still holds
// this replica's id - the same compare-and-delete pattern
// sharedstate.LeaderElector uses for its lock. This is what makes leader
// handover safe: a stepping-down leader never clears a status key that the
// incoming leader has already claimed, so "connected" doesn't flicker off
// during a handover.
var statusDeleteIfMatch = redis.NewScript(`
if redis.call('get', KEYS[1]) == ARGV[1] then
    return redis.call('del', KEYS[1])
else
    return 0
end
`)

// publishConnected mirrors this replica's connected flag into the shared
// Redis status key (HA only). Set stamps this replica's id into the key and
// refreshes its TTL so the status survives between the leader's periodic
// renewals; clear deletes it, but only if this replica still owns it (see
// statusDeleteIfMatch).
func (a *Agent) publishConnected(v bool) {
	if a.shared == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()
	r := a.shared.Raw()
	if v {
		if err := r.Set(ctx, statusKey, a.id, statusKeyTTL).Err(); err != nil {
			slog.Warn("cloudbridge: failed to publish connected status", "error", err)
		}
	} else if _, err := statusDeleteIfMatch.Run(ctx, r, []string{statusKey}, a.id).Result(); err != nil {
		slog.Warn("cloudbridge: failed to clear connected status", "error", err)
	}
}

// connectedNow reports whether the tunnel is currently up, from the source
// of truth for this deployment: the in-memory flag in single-server mode,
// or the shared Redis status key in HA mode (so every replica - whichever
// one the load balancer routed the status request to - answers the same).
func (a *Agent) connectedNow() bool {
	if a.shared == nil {
		a.connMu.RLock()
		defer a.connMu.RUnlock()
		return a.connected
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()
	// Any value (the owning leader's id) means some replica currently holds
	// a live tunnel.
	v, err := a.shared.Raw().Get(ctx, statusKey).Result()
	if err != nil {
		return false
	}
	return v != ""
}

// setConnected updates this replica's in-memory connected flag and, in HA,
// mirrors it to the shared status key. Called from the tunnel goroutine.
func (a *Agent) setConnected(v bool) {
	a.connMu.Lock()
	a.connected = v
	a.connMu.Unlock()
	a.publishConnected(v)
}

// startTunnel cancels any tunnel this replica is already running and starts
// a fresh one for state.
func (a *Agent) startTunnel(state model.CloudBridgeState) {
	ctx, cancel := context.WithCancel(context.Background())
	a.tunnelMu.Lock()
	if a.tunnelCancel != nil {
		a.tunnelCancel()
	}
	a.tunnelCancel = cancel
	a.tunnelState = state
	a.tunnelActive = true
	a.tunnelMu.Unlock()
	go a.runTunnel(ctx, state)
}

// stopTunnel cancels this replica's active tunnel (if any) and waits for
// the reconnect loop to notice the cancellation.
func (a *Agent) stopTunnel() {
	a.tunnelMu.Lock()
	cancel := a.tunnelCancel
	a.tunnelCancel = nil
	a.tunnelActive = false
	a.tunnelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Status is what the admin API/UI show - see handler.GetCloudBridgeStatus.
type Status struct {
	Enrolled               bool       `json:"enrolled"`
	InstanceID             string     `json:"instance_id,omitempty"`
	CertificatesConfigured bool       `json:"certificates_configured"`
	CertificatesLocked     bool       `json:"certificates_locked"`
	Connected              bool       `json:"connected"`
	EnrolledAt             *time.Time `json:"enrolled_at,omitempty"`
}

// CurrentStatus combines persisted enrollment state with the live
// connection flag - safe to call whether or not this installation has
// ever enrolled.
func (a *Agent) CurrentStatus() (Status, error) {
	state, err := a.store.GetCloudBridgeState()
	if err == sql.ErrNoRows {
		return Status{Enrolled: false, CertificatesLocked: LockCertificates()}, nil
	}
	if err != nil {
		return Status{}, err
	}

	enrolledAt := state.EnrolledAt
	return Status{
		Enrolled:               true,
		InstanceID:             state.InstanceID,
		CertificatesConfigured: state.CACert != "",
		CertificatesLocked:     LockCertificates(),
		Connected:              a.connectedNow(),
		EnrolledAt:             &enrolledAt,
	}, nil
}

// CurrentStatus reports this installation's Cloud Bridge status for the
// admin API/UI - see Agent.CurrentStatus. Delegates to the default Agent.
func CurrentStatus() (Status, error) {
	a := getDefaultAgent()
	if a == nil {
		return Status{}, nil
	}
	return a.CurrentStatus()
}

// EnrollWithLink is called by handler.SubmitCloudBridgeLink when an admin
// submits a pairing link. Refuses if this installation is already
// enrolled - there is deliberately no "re-pair with a different link"
// path while already paired; Disconnect must be called first.
//
// This only assigns identity (instance_id, broker_host) - it deliberately
// does not, in single-server mode, do more than save. In HA mode the
// actual tunnel start is the leader's job (see persistCertificates), so
// this only persists and lets the leader converge.
func EnrollWithLink(link string) (*model.CloudBridgeState, error) {
	a := getDefaultAgent()
	if a == nil {
		return nil, fmt.Errorf("cloud bridge is not enabled")
	}
	return a.EnrollWithLink(link)
}

func (a *Agent) EnrollWithLink(link string) (*model.CloudBridgeState, error) {
	if _, err := a.store.GetCloudBridgeState(); err == nil {
		return nil, fmt.Errorf("this installation is already paired - instance_id is permanent")
	} else if err != sql.ErrNoRows {
		return nil, fmt.Errorf("check existing enrollment: %w", err)
	}

	state, err := enroll(link)
	if err != nil {
		return nil, err
	}
	if err := a.store.SaveCloudBridgeState(state.InstanceID, state.BrokerHost); err != nil {
		return nil, fmt.Errorf("save enrollment: %w", err)
	}

	if LockCertificates() {
		if err := a.persistCertificates(*state); err != nil {
			return nil, fmt.Errorf("save certificates: %w", err)
		}
		slog.Info("cloudbridge: paired, certificates locked - saved and connecting automatically", "instance_id", state.InstanceID, "broker_host", state.BrokerHost)
		state.CACert, state.ClientCert, state.ClientKey = "", "", ""
		return state, nil
	}

	slog.Info("cloudbridge: paired, awaiting certificates", "instance_id", state.InstanceID, "broker_host", state.BrokerHost)
	return state, nil
}

// SaveCertificates persists this installation's mTLS certificate material
// and (re)connects the tunnel - see the package doc for how the tunnel
// start is attributed (this replica directly in single-server mode; the
// leader in HA mode). Can be called more than once (initial save, later
// repair), unless LockCertificates is set, in which case it refuses.
func SaveCertificates(caCert, clientCert, clientKey string) error {
	a := getDefaultAgent()
	if a == nil {
		return fmt.Errorf("cloud bridge is not enabled")
	}
	return a.SaveCertificates(caCert, clientCert, clientKey)
}

func (a *Agent) SaveCertificates(caCert, clientCert, clientKey string) error {
	if LockCertificates() {
		return fmt.Errorf("certificate management is locked (CLOUD_BRIDGE_LOCK_CERTIFICATES) - disconnect and re-pair to get new certificates")
	}
	if caCert == "" || clientCert == "" || clientKey == "" {
		return fmt.Errorf("ca_cert, client_cert, and client_key are all required")
	}

	state, err := a.store.GetCloudBridgeState()
	if err == sql.ErrNoRows {
		return fmt.Errorf("not paired yet - submit a pairing link first")
	}
	if err != nil {
		return fmt.Errorf("load enrollment: %w", err)
	}
	state.CACert, state.ClientCert, state.ClientKey = caCert, clientCert, clientKey

	if err := a.persistCertificates(*state); err != nil {
		return err
	}
	slog.Info("cloudbridge: certificates saved, connecting", "instance_id", state.InstanceID, "broker_host", state.BrokerHost)
	return nil
}

// persistCertificates validates and saves a fully-populated state's
// certificate material, then gets the tunnel running with it. In
// single-server mode that means starting the tunnel on this replica
// directly (the original behavior); in HA mode this replica may not be the
// leader, so it only persists and broadcasts a "refresh" for the leader to
// converge on (see reconcile) - starting a tunnel here too would produce a
// second, uncoordinated connection under the same identity.
func (a *Agent) persistCertificates(state model.CloudBridgeState) error {
	if _, _, err := parseCerts(state.CACert, state.ClientCert, state.ClientKey); err != nil {
		return err
	}
	if err := a.store.UpdateCloudBridgeCertificates(state.CACert, state.ClientCert, state.ClientKey); err != nil {
		return fmt.Errorf("save certificates: %w", err)
	}
	if a.shared == nil {
		a.startTunnel(state)
	} else {
		a.publishControl(controlRefresh)
	}
	return nil
}

// Disconnect leaves Cloud Bridge entirely: deletes this installation's
// identity and certificates from Postgres, stops this replica's tunnel, and
// (in HA) broadcasts a "stop" so the leader stops its tunnel immediately
// rather than waiting out its reconcile tick. Afterward CurrentStatus
// reports Enrolled: false again and a fresh pairing link can be submitted.
func Disconnect() error {
	a := getDefaultAgent()
	if a == nil {
		return fmt.Errorf("cloud bridge is not enabled")
	}
	return a.Disconnect()
}

func (a *Agent) Disconnect() error {
	if err := a.store.DeleteCloudBridgeState(); err != nil {
		return err
	}
	a.stopTunnel()
	a.setConnected(false)
	a.publishControl(controlStop)
	slog.Info("cloudbridge: disconnected - identity and certificates removed")
	return nil
}

// enrollResponse mirrors the Logmara Cloud broker's POST /broker/enroll
// JSON response exactly (see that repo's handler.EnrollAgent).
type enrollResponse struct {
	InstanceID string `json:"instance_id"`
	BrokerHost string `json:"broker_host"`
	CACert     string `json:"ca_cert"`
	ClientCert string `json:"client_cert"`
	ClientKey  string `json:"client_key"`
	Error      string `json:"error"`
}

// enroll redeems a pairing link's token for this installation's cloud
// identity. The link is the full enroll_url the cloud
// portal generated (e.g. "https://cloud.example.com/broker/enroll?token=...")
// - its query string carries the one-time token, which is moved into the
// POST body since that's what the cloud side's handler.EnrollAgent
// expects (the link is a copy-paste-friendly shape, not the literal
// wire request).
func enroll(link string) (*model.CloudBridgeState, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return nil, fmt.Errorf("invalid pairing link: %w", err)
	}
	token := u.Query().Get("token")
	if token == "" {
		return nil, fmt.Errorf("pairing link is missing its token")
	}
	u.RawQuery = ""

	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return nil, fmt.Errorf("build enrollment request: %w", err)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(u.String(), "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("could not reach Logmara Cloud: %w", err)
	}
	defer resp.Body.Close()

	var out enrollResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("invalid response from Logmara Cloud (status %s)", resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != "" {
			return nil, fmt.Errorf("pairing rejected by Logmara Cloud: %s", out.Error)
		}
		return nil, fmt.Errorf("pairing rejected by Logmara Cloud (status %s)", resp.Status)
	}

	return &model.CloudBridgeState{
		InstanceID: out.InstanceID,
		BrokerHost: out.BrokerHost,
		CACert:     out.CACert,
		ClientCert: out.ClientCert,
		ClientKey:  out.ClientKey,
		EnrolledAt: time.Now(),
	}, nil
}

// Frame is the wire protocol multiplexed over the tunnel - MUST match the
// Logmara Cloud broker's own Frame type field-for-field (that repo's
// backend/broker/broker.go) since both sides marshal/unmarshal the same
// JSON shape.
type Frame struct {
	ID      string              `json:"id"`
	Type    string              `json:"type"` // "request" (from broker) or "response" (from us)
	Method  string              `json:"method,omitempty"`
	Path    string              `json:"path,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
	Status  int                 `json:"status,omitempty"`
	Body    []byte              `json:"body,omitempty"`
}

// parseCerts validates and parses an installation's mTLS material, shared
// by connectOnce (dialing) and persistCertificates (validating a
// paste/upload before it's persisted, so a bad cert is rejected immediately
// with a clear error instead of only surfacing later in background retry
// logs).
func parseCerts(caCert, clientCert, clientKey string) (tls.Certificate, *x509.CertPool, error) {
	cert, err := tls.X509KeyPair([]byte(clientCert), []byte(clientKey))
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("parse client certificate: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM([]byte(caCert)) {
		return tls.Certificate{}, nil, fmt.Errorf("parse CA certificate")
	}
	return cert, caPool, nil
}

// runTunnel holds the connect/reconnect loop for one enrolled identity -
// runs until ctx is cancelled (see startTunnel/stopTunnel), started either
// from bootSingle (single-server already-configured case), reconcile (HA
// leader), or persistCertificates (single-server just-configured case).
func (a *Agent) runTunnel(ctx context.Context, state model.CloudBridgeState) {
	backoff := reconnectBaseDelay
	for {
		dialed, err := a.connectOnce(ctx, state)
		a.setConnected(false)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("cloudbridge: tunnel session ended", "error", err)
		}
		if dialed {
			backoff = reconnectBaseDelay
		} else if backoff < reconnectMaxDelay {
			backoff *= 2
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

// connectOnce dials the tunnel, serves frames until the connection drops
// or ctx is cancelled, and returns whether the dial itself succeeded (so
// runTunnel knows whether to reset its backoff) plus why the session ended.
func (a *Agent) connectOnce(ctx context.Context, state model.CloudBridgeState) (dialed bool, err error) {
	if state.CACert == "" || state.ClientCert == "" || state.ClientKey == "" {
		return false, fmt.Errorf("no certificates configured yet")
	}
	cert, caPool, err := parseCerts(state.CACert, state.ClientCert, state.ClientKey)
	if err != nil {
		return false, err
	}

	dialer := websocket.Dialer{
		TLSClientConfig:  &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: caPool},
		HandshakeTimeout: 15 * time.Second,
	}
	u := url.URL{Scheme: "wss", Host: state.BrokerHost, Path: "/broker/connect"}
	conn, resp, dialErr := dialer.DialContext(ctx, u.String(), nil)
	if dialErr != nil {
		if resp != nil {
			return false, fmt.Errorf("dial tunnel (%s): %w", resp.Status, dialErr)
		}
		return false, fmt.Errorf("dial tunnel: %w", dialErr)
	}
	defer conn.Close()

	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Time{})
		return nil
	})

	stopWatch := make(chan struct{})
	defer close(stopWatch)

	go func() {
		ticker := time.NewTicker(pingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// Refresh the shared connected status TTL alongside each
				// ping (no-op in single-server mode) so the status survives
				// as long as the tunnel is alive.
				a.publishConnected(true)
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
				conn.SetWriteDeadline(time.Time{})
			case <-stopWatch:
				return
			}
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-stopWatch:
		}
	}()

	a.setConnected(true)
	slog.Info("cloudbridge: tunnel connected", "broker_host", state.BrokerHost)

	conn.SetReadDeadline(time.Now().Add(readDeadline))

	var writeMu sync.Mutex
	for {
		var frame Frame
		if err := conn.ReadJSON(&frame); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				return true, fmt.Errorf("read frame: %w", err)
			}
			return true, nil
		}
		if frame.Type != "request" {
			continue
		}
		go handleRequestFrame(conn, &writeMu, frame)
	}
}

// handleRequestFrame replays one tunneled request against the local API
// and writes the response frame back. Runs in its own goroutine per
// frame so one slow local request can't block the others sharing this
// tunnel connection.
func handleRequestFrame(conn *websocket.Conn, writeMu *sync.Mutex, frame Frame) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("cloudbridge: panic replaying tunneled request", "panic", r, "path", frame.Path)
		}
	}()

	resp := replay(frame)

	writeMu.Lock()
	defer writeMu.Unlock()
	if err := conn.WriteJSON(resp); err != nil {
		slog.Warn("cloudbridge: failed to write response frame", "error", err)
	}
}

// frontendUpstream is where every tunneled request gets forwarded -
// deliberately the existing frontend container, not this backend's own
// router. That container already serves the SPA's static build AND
// reverse-proxies /api to this backend for ordinary LAN browser access
// (see frontend/nginx.conf) - so replaying a tunneled request against it
// is the SAME code path a real browser already takes, not a second one
// this package has to keep in sync.
func frontendUpstream() string {
	if v := os.Getenv("CLOUD_BRIDGE_FRONTEND_UPSTREAM"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://frontend"
}

// nginxExpectsProxyProtocol mirrors handler.nginxProxyProtocolEnabled's
// NGINX_PROXY_PROTOCOL check (duplicated rather than imported to avoid a
// handler<->cloudbridge import cycle - handler already imports cloudbridge
// to drive it). Needed here because frontendUpstream's plain-HTTP request
// gets 301-redirected straight to nginx's :443 the moment an admin turns on
// Settings > "Przekieruj HTTP na HTTPS" - and whenever NGINX_PROXY_PROTOCOL
// is also set (docker-stack.app.yml's HA topology), that :443 listener
// rejects any TLS ClientHello that isn't preceded by a PROXY protocol line.
// dialFrontendTLS below sends one itself so this replay survives that
// redirect instead of the connection getting reset mid-handshake.
func nginxExpectsProxyProtocol() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("NGINX_PROXY_PROTOCOL")), "true")
}

// dialFrontendTLS is replayClient's Transport.DialTLSContext, used only
// when a replayed request's scheme is https (i.e. after following nginx's
// forced-HTTPS redirect). InsecureSkipVerify is deliberate: this connection
// stays entirely inside the deployment's own overlay network to a container
// named "frontend", which the operator's uploaded certificate was never
// issued for - same "not a new trust boundary" reasoning as
// frontendUpstream's plain-HTTP hop above.
func dialFrontendTLS(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	if nginxExpectsProxyProtocol() {
		if _, err := conn.Write(proxyProtocolHeader(conn)); err != nil {
			conn.Close()
			return nil, fmt.Errorf("write proxy protocol header: %w", err)
		}
	}
	tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

// proxyProtocolHeader builds a PROXY protocol v1 line from conn's own
// endpoints. Its only job is to satisfy nginx's parser so it trusts the TLS
// ClientHello that follows instead of resetting the connection.
func proxyProtocolHeader(conn net.Conn) []byte {
	local, lok := conn.LocalAddr().(*net.TCPAddr)
	remote, rok := conn.RemoteAddr().(*net.TCPAddr)
	proto := "TCP4"
	if !lok || !rok {
		return []byte("PROXY UNKNOWN\r\n")
	}
	if local.IP.To4() == nil {
		proto = "TCP6"
	}
	return []byte(fmt.Sprintf("PROXY %s %s %s %d %d\r\n", proto, local.IP.String(), remote.IP.String(), local.Port, remote.Port))
}

// replayClient is reused across every replay() call - a fresh http.Client
// per request would open a fresh TCP connection to the frontend container
// each time; this one keeps a small connection pool warm.
var replayClient = &http.Client{
	Timeout:   30 * time.Second,
	Transport: &http.Transport{DialTLSContext: dialFrontendTLS},
}

// replay forwards a tunneled request to frontendUpstream() - whatever auth
// the caller's original request carried rides through in frame.Headers
// unchanged and is enforced by this installation's own existing middleware
// exactly as it would be for any other client of that container.
func replay(frame Frame) Frame {
	req, err := http.NewRequest(frame.Method, frontendUpstream()+frame.Path, bytes.NewReader(frame.Body))
	if err != nil {
		return errorResponse(frame.ID, fmt.Sprintf("build request: %v", err))
	}
	for k, values := range frame.Headers {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}

	resp, err := replayClient.Do(req)
	if err != nil {
		return errorResponse(frame.ID, fmt.Sprintf("reach local app: %v", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errorResponse(frame.ID, fmt.Sprintf("read response: %v", err))
	}

	return Frame{
		ID:      frame.ID,
		Type:    "response",
		Status:  resp.StatusCode,
		Headers: resp.Header,
		Body:    body,
	}
}

func errorResponse(id, message string) Frame {
	body, _ := json.Marshal(map[string]string{"error": message})
	return Frame{ID: id, Type: "response", Status: http.StatusBadGateway, Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: body}
}
