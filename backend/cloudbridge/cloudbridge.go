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
// EnrollWithLink, called from handler.SubmitCloudBridgeLink. That single
// call is the only time this installation is ever assigned an
// instance_id; once db.SaveCloudBridgeState has written it, it is
// treated as permanent for the lifetime of the installation - there is
// no re-enrollment path, only reconnecting with the same identity after
// a restart (see Start).
package cloudbridge

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"logmara/db"
	"logmara/model"

	"github.com/gorilla/websocket"
)

// Enabled reports whether CLOUD_BRIDGE_ENABLED=true is set. Read live
// (not cached at startup) everywhere it's checked, so e.g. /auth/me
// always reflects the actual running configuration.
func Enabled() bool {
	return os.Getenv("CLOUD_BRIDGE_ENABLED") == "true"
}

var (
	stateMu sync.Mutex
	pool    *db.DynamicPool

	connMu    sync.RWMutex
	connected bool
)

// frontendUpstream is where every tunneled request gets forwarded -
// deliberately the existing frontend container, not this backend's own
// router. That container already serves the SPA's static build AND
// reverse-proxies /api to this backend for ordinary LAN browser access
// (see frontend/nginx.conf) - so replaying a tunneled request against it
// is the SAME code path a real browser already takes, not a second one
// this package has to keep in sync. Whether a request is "the app" or
// "the API" is nginx's routing table's job here, exactly as it is for
// every other client - cloudbridge itself stays completely ignorant of
// it, so a future change to that routing (a new API prefix, a rewrite
// rule) never requires touching this package.
func frontendUpstream() string {
	if v := os.Getenv("CLOUD_BRIDGE_FRONTEND_UPSTREAM"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://frontend"
}

// Status is what the admin API/UI show - see handler.GetCloudBridgeStatus.
type Status struct {
	Enrolled   bool       `json:"enrolled"`
	InstanceID string     `json:"instance_id,omitempty"`
	Connected  bool       `json:"connected"`
	EnrolledAt *time.Time `json:"enrolled_at,omitempty"`
}

// CurrentStatus combines persisted enrollment state with the live
// connection flag - safe to call whether or not this installation has
// ever enrolled.
func CurrentStatus() (Status, error) {
	stateMu.Lock()
	p := pool
	stateMu.Unlock()
	if p == nil {
		return Status{}, nil
	}

	state, err := db.GetCloudBridgeState(p.Get())
	if err == sql.ErrNoRows {
		return Status{Enrolled: false}, nil
	}
	if err != nil {
		return Status{}, err
	}

	connMu.RLock()
	c := connected
	connMu.RUnlock()

	enrolledAt := state.EnrolledAt
	return Status{Enrolled: true, InstanceID: state.InstanceID, Connected: c, EnrolledAt: &enrolledAt}, nil
}

func setConnected(v bool) {
	connMu.Lock()
	connected = v
	connMu.Unlock()
}

// Start wires this package to the running instance and, if a previous
// run already enrolled (state persisted in Postgres, survives restarts -
// see model.CloudBridgeState), reconnects immediately. Call exactly once
// from main(), only when Enabled().
func Start(p *db.DynamicPool) {
	stateMu.Lock()
	pool = p
	stateMu.Unlock()

	state, err := db.GetCloudBridgeState(p.Get())
	if err == sql.ErrNoRows {
		slog.Info("cloudbridge: enabled, not yet paired - waiting for a pairing link via Admin > Cloud Bridge")
		return
	}
	if err != nil {
		slog.Error("cloudbridge: failed to load saved state", "error", err)
		return
	}
	slog.Info("cloudbridge: reconnecting with existing identity", "instance_id", state.InstanceID)
	go runTunnel(*state)
}

// EnrollWithLink is called by handler.SubmitCloudBridgeLink when an admin
// submits a pairing link. Refuses if this installation is already
// enrolled - instance_id is permanent by design (see package doc
// comment); there is deliberately no "re-pair with a different link"
// path here.
func EnrollWithLink(link string) error {
	stateMu.Lock()
	p := pool
	stateMu.Unlock()
	if p == nil {
		return fmt.Errorf("cloud bridge is not enabled")
	}

	if _, err := db.GetCloudBridgeState(p.Get()); err == nil {
		return fmt.Errorf("this installation is already paired - instance_id is permanent")
	} else if err != sql.ErrNoRows {
		return fmt.Errorf("check existing enrollment: %w", err)
	}

	state, err := enroll(link)
	if err != nil {
		return err
	}
	if err := db.SaveCloudBridgeState(p.Get(), state.InstanceID, state.BrokerHost, state.CACert, state.ClientCert, state.ClientKey); err != nil {
		return fmt.Errorf("save enrollment: %w", err)
	}

	slog.Info("cloudbridge: enrolled", "instance_id", state.InstanceID, "broker_host", state.BrokerHost)
	go runTunnel(*state)
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

// enroll redeems a pairing link's token for this installation's
// permanent cloud identity. The link is the full enroll_url the cloud
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

const (
	reconnectBaseDelay = 2 * time.Second
	reconnectMaxDelay  = 60 * time.Second
)

// runTunnel holds the connect/reconnect loop for one enrolled identity -
// runs for the lifetime of the process once started, either from Start
// (already-enrolled case) or EnrollWithLink (just-enrolled case).
func runTunnel(state model.CloudBridgeState) {
	backoff := reconnectBaseDelay
	for {
		dialed, err := connectOnce(state)
		setConnected(false)
		if err != nil {
			slog.Warn("cloudbridge: tunnel session ended", "error", err)
		}
		if dialed {
			backoff = reconnectBaseDelay
		} else if backoff < reconnectMaxDelay {
			backoff *= 2
		}
		time.Sleep(backoff)
	}
}

// connectOnce dials the tunnel, serves frames until the connection drops,
// and returns whether the dial itself succeeded (so runTunnel knows
// whether to reset its backoff) plus why the session ended.
func connectOnce(state model.CloudBridgeState) (dialed bool, err error) {
	cert, err := tls.X509KeyPair([]byte(state.ClientCert), []byte(state.ClientKey))
	if err != nil {
		return false, fmt.Errorf("parse client certificate: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM([]byte(state.CACert)) {
		return false, fmt.Errorf("parse CA certificate")
	}

	dialer := websocket.Dialer{
		TLSClientConfig:  &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: caPool},
		HandshakeTimeout: 15 * time.Second,
	}
	u := url.URL{Scheme: "wss", Host: state.BrokerHost, Path: "/broker/connect"}
	conn, resp, dialErr := dialer.Dial(u.String(), nil)
	if dialErr != nil {
		if resp != nil {
			return false, fmt.Errorf("dial tunnel (%s): %w", resp.Status, dialErr)
		}
		return false, fmt.Errorf("dial tunnel: %w", dialErr)
	}
	defer conn.Close()

	setConnected(true)
	slog.Info("cloudbridge: tunnel connected", "broker_host", state.BrokerHost)

	var writeMu sync.Mutex // gorilla/websocket allows only one concurrent writer per connection
	for {
		var frame Frame
		if err := conn.ReadJSON(&frame); err != nil {
			return true, fmt.Errorf("read frame: %w", err)
		}
		if frame.Type != "request" {
			continue // the broker only ever sends "request" frames; ignore anything else defensively
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

// replayClient is reused across every replay() call - a fresh http.Client
// per request would open a fresh TCP connection to the frontend container
// each time; this one keeps a small connection pool warm via the default
// Transport.
var replayClient = &http.Client{Timeout: 30 * time.Second}

// replay forwards a tunneled request to frontendUpstream() - see that
// function's doc comment for why this is a plain HTTP hop to the existing
// frontend container rather than anything specific to this package.
// Whatever auth the caller's original request carried (session cookie,
// API key) rides through in frame.Headers unchanged and is enforced by
// this installation's own existing middleware exactly as it would be for
// any other client of that container.
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
