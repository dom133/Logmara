package cloudbridge

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"logmara/model"
	"logmara/sharedstate"

	"github.com/alicebob/miniredis/v2"
	"github.com/gorilla/websocket"
)

// --- test infrastructure -------------------------------------------------

// testCA is a throwaway certificate authority for the tests: it signs the
// fake broker's server certificate, the agents' mTLS client certificates, and
// exposes its own PEM as the "cloud CA" an agent would be handed at pairing.
type testCA struct {
	key  *ecdsa.PrivateKey
	cert *x509.Certificate
	pem  []byte
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "cloudbridge test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatalf("self-sign CA: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	return &testCA{key: key, cert: cert, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue signs a leaf certificate for cn with the given extended key usage
// (ServerAuth for the fake broker, ClientAuth for the agents) and returns
// PEM-encoded cert + private key.
func (ca *testCA) issue(t *testing.T, cn string, eku x509.ExtKeyUsage, ips ...net.IP) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key for %s: %v", cn, err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{eku},
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, key.Public(), ca.key)
	if err != nil {
		t.Fatalf("sign cert for %s: %v", cn, err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key for %s: %v", cn, err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// fakeBroker stands in for the Logmara Cloud broker: an mTLS WebSocket
// endpoint at /broker/connect that requires the test CA's client
// certificate, and counts live connections so a test can assert exactly one
// replica is tunneling at a time.
type fakeBroker struct {
	mu     sync.Mutex
	active int
	total  int
	srv    *http.Server
	host   string
}

func newFakeBroker(t *testing.T, ca *testCA) *fakeBroker {
	t.Helper()
	serverCert, serverKey := ca.issue(t, "fake-broker", x509.ExtKeyUsageServerAuth, net.ParseIP("127.0.0.1"))
	cert, err := tls.X509KeyPair(serverCert, serverKey)
	if err != nil {
		t.Fatalf("parse broker cert: %v", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(ca.pem) {
		t.Fatal("append CA to pool")
	}

	b := &fakeBroker{}
	upgrader := websocket.Upgrader{}
	mux := http.NewServeMux()
	mux.HandleFunc("/broker/connect", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		b.mu.Lock()
		b.active++
		b.total++
		b.mu.Unlock()
		defer conn.Close()
		defer func() {
			b.mu.Lock()
			b.active--
			b.mu.Unlock()
		}()
		// Hold the connection open until the client closes it - the broker
		// never initiates frames in these tests.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	})
	b.srv = &http.Server{Handler: mux}
	b.host = ln.Addr().String()
	go func() { _ = b.srv.Serve(tlsLn) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = b.srv.Shutdown(ctx)
	})
	return b
}

func (b *fakeBroker) activeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.active
}

func (b *fakeBroker) totalCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total
}

// fakeCloud mimics the Logmara Cloud enrollment endpoint a pairing link is
// redeemed against.
type fakeCloud struct {
	srv *httptest.Server
}

func newFakeCloud(t *testing.T, brokerHost string, ca *testCA) *fakeCloud {
	t.Helper()
	clientCert, clientKey := ca.issue(t, "local-agent", x509.ExtKeyUsageClientAuth)
	fc := &fakeCloud{}
	fc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/broker/enroll" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"missing token"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"instance_id": "test-instance",
			"broker_host": brokerHost,
			"ca_cert":     string(ca.pem),
			"client_cert": string(clientCert),
			"client_key":  string(clientKey),
		})
	}))
	t.Cleanup(fc.srv.Close)
	return fc
}

// newTestShared points sharedstate.Connect() at a fresh miniredis and
// returns the resulting client, matching how the production HA path gets
// its shared state.
func newTestShared(t *testing.T) *sharedstate.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	t.Setenv("REDIS_SENTINEL_ADDRS", "")
	t.Setenv("REDIS_ADDR", mr.Addr())
	client, err := sharedstate.Connect()
	if err != nil {
		t.Fatalf("connect to miniredis: %v", err)
	}
	if client == nil {
		t.Fatal("expected a non-nil shared client")
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// fakeStore implements stateStore in memory so tests never touch Postgres.
type fakeStore struct {
	mu    sync.Mutex
	state *model.CloudBridgeState
}

func (s *fakeStore) GetCloudBridgeState() (*model.CloudBridgeState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		return nil, sql.ErrNoRows
	}
	out := *s.state
	return &out, nil
}

func (s *fakeStore) SaveCloudBridgeState(instanceID, brokerHost string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != nil {
		return fmt.Errorf("already paired")
	}
	s.state = &model.CloudBridgeState{InstanceID: instanceID, BrokerHost: brokerHost, EnrolledAt: time.Now()}
	return nil
}

func (s *fakeStore) UpdateCloudBridgeCertificates(caCert, clientCert, clientKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		return fmt.Errorf("not paired yet")
	}
	s.state.CACert = caCert
	s.state.ClientCert = clientCert
	s.state.ClientKey = clientKey
	return nil
}

func (s *fakeStore) DeleteCloudBridgeState() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = nil
	return nil
}

func (s *fakeStore) hasState() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state != nil
}

func (s *fakeStore) hasCerts() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state != nil && s.state.CACert != ""
}

// seededState returns a fully-paired identity (identity + certificates)
// pointed at the fake broker.
func seededState(t *testing.T, ca *testCA, brokerHost string) *model.CloudBridgeState {
	t.Helper()
	clientCert, clientKey := ca.issue(t, "local-agent", x509.ExtKeyUsageClientAuth)
	return &model.CloudBridgeState{
		InstanceID: "test-instance",
		BrokerHost: brokerHost,
		CACert:     string(ca.pem),
		ClientCert: string(clientCert),
		ClientKey:  string(clientKey),
		EnrolledAt: time.Now(),
	}
}

// fastTiming shrinks the HA coordination knobs so election/handover
// scenarios resolve in milliseconds instead of seconds; restored on cleanup.
func fastTiming(t *testing.T) {
	t.Helper()
	old := [3]time.Duration{leaderRenewPeriod, standbyPoll, watcherPeriod}
	leaderRenewPeriod = 200 * time.Millisecond
	standbyPoll = 50 * time.Millisecond
	watcherPeriod = 50 * time.Millisecond
	t.Cleanup(func() {
		leaderRenewPeriod, standbyPoll, watcherPeriod = old[0], old[1], old[2]
	})
}

// waitFor polls cond until it reports true or the deadline passes.
func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for: %s", timeout, what)
}

func exactlyOneLeader(a, b *Agent) bool {
	return a.isLeader.Load() != b.isLeader.Load()
}

// --- single-server mode (shared == nil, original behavior) ---------------

func TestSingleMode_ReconnectsExistingIdentity(t *testing.T) {
	ca := newTestCA(t)
	broker := newFakeBroker(t, ca)
	store := &fakeStore{state: seededState(t, ca, broker.host)}

	a := newAgent(store, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.Start(ctx)

	waitFor(t, "tunnel up", 5*time.Second, func() bool { return broker.activeCount() == 1 })

	st, err := a.CurrentStatus()
	if err != nil {
		t.Fatalf("CurrentStatus: %v", err)
	}
	if !st.Enrolled || !st.CertificatesConfigured || !st.Connected {
		t.Fatalf("unexpected status: %+v", st)
	}
}

func TestSingleMode_EnrollAndConnect(t *testing.T) {
	ca := newTestCA(t)
	broker := newFakeBroker(t, ca)
	cloud := newFakeCloud(t, broker.host, ca)
	store := &fakeStore{}

	a := newAgent(store, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.Start(ctx)

	link := cloud.srv.URL + "/broker/enroll?token=abc123"
	state, err := a.EnrollWithLink(link)
	if err != nil {
		t.Fatalf("EnrollWithLink: %v", err)
	}
	if state.InstanceID != "test-instance" {
		t.Fatalf("expected instance test-instance, got %q", state.InstanceID)
	}
	// LockCertificates is true by default: certificates are saved and
	// zeroed out of the returned state.
	if state.CACert != "" || state.ClientCert != "" || state.ClientKey != "" {
		t.Fatalf("expected locked-mode certs to be zeroed, got non-empty")
	}
	if !store.hasCerts() {
		t.Fatal("expected certificates persisted server-side")
	}

	waitFor(t, "tunnel up", 5*time.Second, func() bool { return broker.activeCount() == 1 })

	st, err := a.CurrentStatus()
	if err != nil {
		t.Fatalf("CurrentStatus: %v", err)
	}
	if !st.Enrolled || !st.Connected || !st.CertificatesLocked {
		t.Fatalf("unexpected status: %+v", st)
	}
}

// --- HA mode (shared != nil) ---------------------------------------------

func TestHAMode_OnlyOneLeaderTunnels(t *testing.T) {
	fastTiming(t)
	ca := newTestCA(t)
	broker := newFakeBroker(t, ca)
	store := &fakeStore{state: seededState(t, ca, broker.host)}
	shared := newTestShared(t)

	a1 := newAgent(store, shared)
	a2 := newAgent(store, shared)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a1.Start(ctx)
	a2.Start(ctx)

	waitFor(t, "exactly one leader", 5*time.Second, func() bool { return exactlyOneLeader(a1, a2) })
	waitFor(t, "exactly one broker connection", 5*time.Second, func() bool { return broker.activeCount() == 1 })

	if !exactlyOneLeader(a1, a2) {
		t.Fatalf("expected exactly one leader, got a1=%v a2=%v", a1.isLeader.Load(), a2.isLeader.Load())
	}
	if n := broker.activeCount(); n != 1 {
		t.Fatalf("expected exactly 1 broker connection (not %d) - only the leader should tunnel", n)
	}
}

func TestHAMode_LeaderHandoverOnShutdown(t *testing.T) {
	fastTiming(t)
	ca := newTestCA(t)
	broker := newFakeBroker(t, ca)
	store := &fakeStore{state: seededState(t, ca, broker.host)}
	shared := newTestShared(t)

	a1 := newAgent(store, shared)
	a2 := newAgent(store, shared)
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	t.Cleanup(cancel1)
	t.Cleanup(cancel2)
	a1.Start(ctx1)
	a2.Start(ctx2)

	waitFor(t, "exactly one leader", 5*time.Second, func() bool { return exactlyOneLeader(a1, a2) })
	waitFor(t, "broker connection", 5*time.Second, func() bool { return broker.activeCount() == 1 })

	var leader, standby *Agent
	if a1.isLeader.Load() {
		leader, standby = a1, a2
	} else {
		leader, standby = a2, a1
	}

	// Simulate the leader replica shutting down (its process ctx is
	// cancelled); it releases the lock and tears down its tunnel.
	if leader == a1 {
		cancel1()
	} else {
		cancel2()
	}

	waitFor(t, "standby to become leader", 5*time.Second, func() bool { return standby.isLeader.Load() })
	waitFor(t, "standby tunnel up", 5*time.Second, func() bool { return broker.activeCount() == 1 })

	if !standby.isLeader.Load() {
		t.Fatal("expected the standby to become leader after the old leader shut down")
	}
	if broker.totalCount() < 2 {
		t.Fatalf("expected the standby to open a fresh broker connection (total >= 2), got %d", broker.totalCount())
	}
}

func TestHAMode_StatusIsSharedAcrossReplicas(t *testing.T) {
	fastTiming(t)
	ca := newTestCA(t)
	broker := newFakeBroker(t, ca)
	store := &fakeStore{state: seededState(t, ca, broker.host)}
	shared := newTestShared(t)

	a1 := newAgent(store, shared)
	a2 := newAgent(store, shared)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a1.Start(ctx)
	a2.Start(ctx)

	waitFor(t, "exactly one leader", 5*time.Second, func() bool { return exactlyOneLeader(a1, a2) })
	waitFor(t, "broker connection", 5*time.Second, func() bool { return broker.activeCount() == 1 })

	// Both replicas must agree on status - whichever one a load balancer
	// routes an admin to gives the same answer.
	for name, a := range map[string]*Agent{"replica1": a1, "replica2": a2} {
		st, err := a.CurrentStatus()
		if err != nil {
			t.Fatalf("%s CurrentStatus: %v", name, err)
		}
		if !st.Enrolled || !st.CertificatesConfigured || !st.Connected {
			t.Fatalf("%s: expected enrolled+configured+connected, got %+v", name, st)
		}
		if st.InstanceID != "test-instance" {
			t.Fatalf("%s: unexpected instance %q", name, st.InstanceID)
		}
	}
}

func TestHAMode_DisconnectFromNonLeaderStopsLeader(t *testing.T) {
	fastTiming(t)
	ca := newTestCA(t)
	broker := newFakeBroker(t, ca)
	store := &fakeStore{state: seededState(t, ca, broker.host)}
	shared := newTestShared(t)

	a1 := newAgent(store, shared)
	a2 := newAgent(store, shared)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a1.Start(ctx)
	a2.Start(ctx)

	waitFor(t, "exactly one leader", 5*time.Second, func() bool { return exactlyOneLeader(a1, a2) })
	waitFor(t, "broker connection", 5*time.Second, func() bool { return broker.activeCount() == 1 })

	var leader, nonLeader *Agent
	if a1.isLeader.Load() {
		leader, nonLeader = a1, a2
	} else {
		leader, nonLeader = a2, a1
	}

	// Admin action lands on the non-leader replica (haproxy round-robin);
	// it must still take the tunnel down on the leader.
	if err := nonLeader.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	waitFor(t, "leader tunnel to stop", 5*time.Second, func() bool { return broker.activeCount() == 0 })

	if store.hasState() {
		t.Fatal("expected the identity to be deleted from the store")
	}
	_ = leader
	for name, a := range map[string]*Agent{"replica1": a1, "replica2": a2} {
		st, err := a.CurrentStatus()
		if err != nil {
			t.Fatalf("%s CurrentStatus: %v", name, err)
		}
		if st.Enrolled || st.Connected {
			t.Fatalf("%s: expected not enrolled and not connected after disconnect, got %+v", name, st)
		}
	}
}

func TestHAMode_EnrollFromNonLeaderStartsLeaderTunnel(t *testing.T) {
	fastTiming(t)
	ca := newTestCA(t)
	broker := newFakeBroker(t, ca)
	cloud := newFakeCloud(t, broker.host, ca)
	store := &fakeStore{}
	shared := newTestShared(t)

	a1 := newAgent(store, shared)
	a2 := newAgent(store, shared)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a1.Start(ctx)
	a2.Start(ctx)

	waitFor(t, "exactly one leader", 5*time.Second, func() bool { return exactlyOneLeader(a1, a2) })

	var leader, nonLeader *Agent
	if a1.isLeader.Load() {
		leader, nonLeader = a1, a2
	} else {
		leader, nonLeader = a2, a1
	}
	_ = leader

	link := cloud.srv.URL + "/broker/enroll?token=abc123"
	if _, err := nonLeader.EnrollWithLink(link); err != nil {
		t.Fatalf("EnrollWithLink: %v", err)
	}
	if !store.hasState() || !store.hasCerts() {
		t.Fatal("expected the enrollment + certificates to be persisted")
	}

	waitFor(t, "leader tunnel up", 5*time.Second, func() bool { return broker.activeCount() == 1 })

	if n := broker.activeCount(); n != 1 {
		t.Fatalf("expected exactly 1 broker connection after cross-replica pairing, got %d", n)
	}
}
