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

// LockCertificates reports whether CLOUD_BRIDGE_LOCK_CERTIFICATES=true is
// set - for deployments that don't want an admin ever seeing or swapping
// the raw mTLS client key through the UI/API. When set, EnrollWithLink
// persists and connects the certificates Logmara Cloud hands back
// immediately server-side instead of returning them to the caller for
// review, and SaveCertificates refuses any further call outright (there is
// then no repair/replace path at all - Disconnect + re-pairing is the only
// way to get new certificates). Read live, same as Enabled.
func LockCertificates() bool {
	return os.Getenv("CLOUD_BRIDGE_LOCK_CERTIFICATES") == "true"
}

var (
	stateMu sync.Mutex
	pool    *db.DynamicPool

	connMu    sync.RWMutex
	connected bool

	// tunnelMu guards tunnelCancel, the cancel func for whichever runTunnel
	// goroutine is currently active (if any) - see startTunnel and
	// Disconnect. At most one tunnel goroutine runs at a time in practice
	// (Start runs once at boot, SaveCertificates only afterward), but
	// startTunnel cancels any previous one anyway before starting a new one.
	tunnelMu     sync.Mutex
	tunnelCancel context.CancelFunc
)

// startTunnel cancels whatever tunnel goroutine is currently running (if
// any) and starts a fresh one for state, tracking its cancel func so
// Disconnect can stop it immediately instead of waiting out the reconnect
// backoff.
func startTunnel(state model.CloudBridgeState) {
	ctx, cancel := context.WithCancel(context.Background())
	tunnelMu.Lock()
	if tunnelCancel != nil {
		tunnelCancel()
	}
	tunnelCancel = cancel
	tunnelMu.Unlock()
	go runTunnel(ctx, state)
}

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

// nginxExpectsProxyProtocol mirrors handler.nginxProxyProtocolEnabled's
// NGINX_PROXY_PROTOCOL check (duplicated rather than imported to avoid a
// handler<->cloudbridge import cycle - handler already imports cloudbridge
// to drive it). Needed here because frontendUpstream's plain-HTTP request
// gets 301-redirected straight to nginx's :443 the moment an admin turns on
// Settings > "Przekieruj HTTP na HTTPS" (handler.reloadNginx's
// redirect.conf has no exemption for this internal hop) - and whenever
// NGINX_PROXY_PROTOCOL is also set (docker-stack.app.yml's HA topology),
// that :443 listener rejects any TLS ClientHello that isn't preceded by a
// PROXY protocol line, since it normally only ever hears from
// haproxy-app's send-proxy hop, never a direct caller. dialFrontendTLS
// below sends one itself so this replay survives that redirect instead of
// the connection getting reset mid-handshake.
func nginxExpectsProxyProtocol() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("NGINX_PROXY_PROTOCOL")), "true")
}

// dialFrontendTLS is replayClient's Transport.DialTLSContext, used only
// when a replayed request's scheme is https (i.e. after following nginx's
// forced-HTTPS redirect - see nginxExpectsProxyProtocol). InsecureSkipVerify
// is deliberate, not an oversight: this connection stays entirely inside
// the deployment's own overlay network to a container named "frontend",
// which the operator's uploaded certificate (Admin > Settings > HTTPS) was
// never issued for - same "not a new trust boundary" reasoning as
// frontendUpstream's plain-HTTP hop above, just carried over the TLS leg
// nginx now insists on for this one request.
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
// endpoints (this hop has no real end-user address left to carry - that's
// already lost by the time a request reaches replay(), see
// stripCloudSessionCookies in the Logmara Cloud repo's broker package). Its
// only job is to satisfy nginx's parser so it trusts the TLS ClientHello
// that follows instead of resetting the connection; the frontend container
// doesn't otherwise use the address it carries for anything security-
// sensitive, only $remote_addr logging.
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
func CurrentStatus() (Status, error) {
	stateMu.Lock()
	p := pool
	stateMu.Unlock()
	if p == nil {
		return Status{}, nil
	}

	state, err := db.GetCloudBridgeState(p.Get())
	if err == sql.ErrNoRows {
		return Status{Enrolled: false, CertificatesLocked: LockCertificates()}, nil
	}
	if err != nil {
		return Status{}, err
	}

	connMu.RLock()
	c := connected
	connMu.RUnlock()

	enrolledAt := state.EnrolledAt
	return Status{
		Enrolled:               true,
		InstanceID:             state.InstanceID,
		CertificatesConfigured: state.CACert != "",
		CertificatesLocked:     LockCertificates(),
		Connected:              c,
		EnrolledAt:             &enrolledAt,
	}, nil
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
	if state.CACert == "" {
		slog.Info("cloudbridge: paired but no certificates configured yet - waiting for a certificate save via Admin > Cloud Bridge", "instance_id", state.InstanceID)
		return
	}
	slog.Info("cloudbridge: reconnecting with existing identity", "instance_id", state.InstanceID)
	startTunnel(*state)
}

// EnrollWithLink is called by handler.SubmitCloudBridgeLink when an admin
// submits a pairing link. Refuses if this installation is already
// enrolled - there is deliberately no "re-pair with a different link"
// path while already paired; Disconnect must be called first (see its
// doc comment).
//
// This only assigns identity (instance_id, broker_host) - it deliberately
// does not persist the certs Logmara Cloud handed back, nor start the
// tunnel. The returned state (including those certs) lets the caller show
// them to the admin once, to pre-fill the certificate panel in Admin >
// Cloud Bridge; SaveCertificates is what actually saves and connects, once
// the admin has reviewed and submitted them there.
//
// Unless LockCertificates is set, in which case there is no review step at
// all: the certs are persisted and the tunnel started right here, and the
// returned state has them zeroed out so the handler never has anything to
// send back to the frontend to display.
func EnrollWithLink(link string) (*model.CloudBridgeState, error) {
	stateMu.Lock()
	p := pool
	stateMu.Unlock()
	if p == nil {
		return nil, fmt.Errorf("cloud bridge is not enabled")
	}

	if _, err := db.GetCloudBridgeState(p.Get()); err == nil {
		return nil, fmt.Errorf("this installation is already paired - instance_id is permanent")
	} else if err != sql.ErrNoRows {
		return nil, fmt.Errorf("check existing enrollment: %w", err)
	}

	state, err := enroll(link)
	if err != nil {
		return nil, err
	}
	if err := db.SaveCloudBridgeState(p.Get(), state.InstanceID, state.BrokerHost); err != nil {
		return nil, fmt.Errorf("save enrollment: %w", err)
	}

	if LockCertificates() {
		if err := persistCertificates(p, *state); err != nil {
			return nil, fmt.Errorf("save certificates: %w", err)
		}
		slog.Info("cloudbridge: paired, certificates locked - saved and connecting automatically", "instance_id", state.InstanceID, "broker_host", state.BrokerHost)
		state.CACert, state.ClientCert, state.ClientKey = "", "", ""
		return state, nil
	}

	slog.Info("cloudbridge: paired, awaiting certificates", "instance_id", state.InstanceID, "broker_host", state.BrokerHost)
	return state, nil
}

// SaveCertificates is called by handler.SaveCloudBridgeCertificates,
// whether that's the initial certificate save right after pairing or later
// as a repair path (e.g. replacing a cert that fails TLS verification).
// Unlike instance_id/broker_host, these are deliberately overwritable -
// unless LockCertificates is set, in which case this refuses outright:
// EnrollWithLink already saved and connected the certs it received, and
// there is no further path to view or replace them short of Disconnect and
// re-pairing from scratch.
func SaveCertificates(caCert, clientCert, clientKey string) error {
	if LockCertificates() {
		return fmt.Errorf("certificate management is locked (CLOUD_BRIDGE_LOCK_CERTIFICATES) - disconnect and re-pair to get new certificates")
	}

	stateMu.Lock()
	p := pool
	stateMu.Unlock()
	if p == nil {
		return fmt.Errorf("cloud bridge is not enabled")
	}
	if caCert == "" || clientCert == "" || clientKey == "" {
		return fmt.Errorf("ca_cert, client_cert, and client_key are all required")
	}

	state, err := db.GetCloudBridgeState(p.Get())
	if err == sql.ErrNoRows {
		return fmt.Errorf("not paired yet - submit a pairing link first")
	}
	if err != nil {
		return fmt.Errorf("load enrollment: %w", err)
	}
	state.CACert, state.ClientCert, state.ClientKey = caCert, clientCert, clientKey

	if err := persistCertificates(p, *state); err != nil {
		return err
	}
	slog.Info("cloudbridge: certificates saved, connecting", "instance_id", state.InstanceID, "broker_host", state.BrokerHost)
	return nil
}

// persistCertificates validates, saves, and connects a fully-populated
// state's certificate material - the common tail of both SaveCertificates
// and EnrollWithLink's LockCertificates auto-save path.
func persistCertificates(p *db.DynamicPool, state model.CloudBridgeState) error {
	if _, _, err := parseCerts(state.CACert, state.ClientCert, state.ClientKey); err != nil {
		return err
	}
	if err := db.UpdateCloudBridgeCertificates(p.Get(), state.CACert, state.ClientCert, state.ClientKey); err != nil {
		return fmt.Errorf("save certificates: %w", err)
	}
	startTunnel(state)
	return nil
}

// Disconnect leaves Cloud Bridge entirely: stops the tunnel immediately
// (rather than waiting out runTunnel's reconnect backoff) and deletes this
// installation's identity and certificates from Postgres. Unlike every
// other operation in this package, this is meant to undo pairing, not
// build on it - afterward CurrentStatus reports Enrolled: false again, and
// a fresh pairing link can be submitted to start over with a new identity.
func Disconnect() error {
	stateMu.Lock()
	p := pool
	stateMu.Unlock()
	if p == nil {
		return fmt.Errorf("cloud bridge is not enabled")
	}

	tunnelMu.Lock()
	if tunnelCancel != nil {
		tunnelCancel()
		tunnelCancel = nil
	}
	tunnelMu.Unlock()
	setConnected(false)

	if err := db.DeleteCloudBridgeState(p.Get()); err != nil {
		return err
	}
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

const (
	reconnectBaseDelay = 2 * time.Second
	reconnectMaxDelay  = 60 * time.Second
)

// parseCerts validates and parses an installation's mTLS material, shared
// by connectOnce (dialing) and SaveCertificates (validating a paste/upload
// before it's persisted, so a bad cert is rejected immediately with a
// clear error instead of only surfacing later in background retry logs).
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
// runs until ctx is cancelled (see startTunnel/Disconnect), started either
// from Start (already-configured case) or SaveCertificates (just-configured
// case).
func runTunnel(ctx context.Context, state model.CloudBridgeState) {
	backoff := reconnectBaseDelay
	for {
		dialed, err := connectOnce(ctx, state)
		setConnected(false)
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
func connectOnce(ctx context.Context, state model.CloudBridgeState) (dialed bool, err error) {
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

	// ReadJSON below blocks indefinitely with no ctx awareness of its own -
	// this goroutine is what makes Disconnect's cancellation actually
	// interrupt it immediately, by closing the connection out from under
	// the blocked read, instead of only taking effect on the next
	// reconnect attempt.
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-stopWatch:
		}
	}()

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
// each time; this one keeps a small connection pool warm. DialTLSContext
// only kicks in for the https:// leg reached via nginx's forced-HTTPS
// redirect (see dialFrontendTLS) - the plain http:// request frontendUpstream
// normally builds still dials the ordinary way.
var replayClient = &http.Client{
	Timeout:   30 * time.Second,
	Transport: &http.Transport{DialTLSContext: dialFrontendTLS},
}

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
