package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/vault/api"
	"github.com/redis/go-redis/v9"

	"logmara/rotation"
	"logmara/sharedstate"
	"logmara/vaultclient"
)

func resetRotationGlobals(t *testing.T) {
	t.Helper()
	// atomic.Value cannot Store(nil) once initialized, so reset by assigning
	// fresh zero Values (copying an unused zero Value is safe).
	poolRef = nil
	rotationLeaderRef.Store(false)
	manualTriggeredRef.Store(false)
	rotationTriggerBroadcaster = nil
	rotationBroadcaster = nil
	vaultClientRef = nil
	vaultEnabled.Store(false)
	lastRotationAtRef = atomic.Value{}
	nextRotationAtRef = atomic.Value{}
	for i := 0; i < 4; i++ {
		secretResults[i] = atomic.Value{}
		secretLastRotatedAt[i] = atomic.Value{}
		secondaryKeyActive[i].Store(false)
	}
}

func newTestRedisClient(t *testing.T) *sharedstate.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return sharedstate.NewClientWith(rdb)
}

func newTestVaultClient(t *testing.T) *vaultclient.Client {
	t.Helper()
	cfg := api.DefaultConfig()
	cfg.Address = "http://127.0.0.1:9"
	vc, err := api.NewClient(cfg)
	if err != nil {
		t.Fatalf("creating vault api client: %v", err)
	}
	vc.SetToken("test-token")
	return vaultclient.NewForTest(vc)
}

// triggerMessages drains published rotation:trigger messages from a test
// subscriber started before the request.
func triggerMessages(t *testing.T, received chan string) int {
	t.Helper()
	time.Sleep(150 * time.Millisecond) // allow pub/sub delivery to settle
	n := 0
	for {
		select {
		case <-received:
			n++
		default:
			return n
		}
	}
}

// doRotationTrigger runs the TriggerRotation handler in a test context while
// a background goroutine "completes" the rotation (advances all four secret
// timestamps), so waitRotationComplete returns instead of timing out at 300s.
func doRotationTrigger(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/rotation/trigger", nil)

	go func() {
		time.Sleep(100 * time.Millisecond)
		for i := 0; i < 4; i++ {
			SetSecretRotationResult(i, "success")
		}
	}()

	TriggerRotation()(c)
	return rec
}

// The leader replica must run the rotation in its own goroutine and must not
// also publish to rotation:trigger - it subscribes to that channel too, so a
// self-delivered message would queue a second signal and rotate everything
// twice (the second JWT rotation would drop the user's current token out of
// the grace period).
func TestTriggerRotation_LeaderTriggersDirectlyWithoutPublishing(t *testing.T) {
	resetRotationGlobals(t)
	client := newTestRedisClient(t)
	SetRotationTriggerBroadcaster(sharedstate.NewBroadcaster(client))
	SetRotationLeader(true)
	vaultClientRef = newTestVaultClient(t)

	received := make(chan string, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sharedstate.NewBroadcaster(client).Subscribe(ctx, "rotation:trigger", func(string) {
		received <- "msg"
	})
	time.Sleep(50 * time.Millisecond) // let the subscription settle

	rec := doRotationTrigger(t)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !manualTriggeredRef.Load() {
		t.Fatal("leader path should have triggered the local rotation")
	}
	if n := triggerMessages(t, received); n != 0 {
		t.Fatalf("leader must not publish rotation:trigger, delivered %d messages", n)
	}
}

// A non-leader replica has no rotation goroutine, so a local trigger would
// have no consumer; it must forward via rotation:trigger (the leader is the
// only subscriber) and not set the local manual-triggered flag.
func TestTriggerRotation_NonLeaderPublishesOnly(t *testing.T) {
	resetRotationGlobals(t)
	client := newTestRedisClient(t)
	SetRotationTriggerBroadcaster(sharedstate.NewBroadcaster(client))
	SetRotationLeader(false)
	vaultClientRef = newTestVaultClient(t)

	received := make(chan string, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sharedstate.NewBroadcaster(client).Subscribe(ctx, "rotation:trigger", func(string) {
		received <- "msg"
	})
	time.Sleep(50 * time.Millisecond) // let the subscription settle

	rec := doRotationTrigger(t)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if manualTriggeredRef.Load() {
		t.Fatal("non-leader must not trigger a local rotation")
	}
	if n := triggerMessages(t, received); n != 1 {
		t.Fatalf("non-leader should deliver rotation:trigger exactly once, delivered %d messages", n)
	}
}

// With a nil pool (e.g. Vault disabled) SetSecretRotationResult must still
// update the in-memory state used by waitRotationComplete, and must not set
// the secondary-key flag for the PG secret (that only applies to JWT/enc).
func TestSetSecretRotationResult_NilPoolStillUpdatesMemory(t *testing.T) {
	resetRotationGlobals(t)

	SetSecretRotationResult(2, "success")

	if v := secretLastRotatedAt[2].Load(); v == nil {
		t.Fatal("in-memory last_rotated_at should be set even without a pool")
	}
	if r, _ := secretResults[2].Load().(rotation.SecretResult); r.Result != "success" {
		t.Fatalf("expected success result, got %q", r.Result)
	}
	if secondaryKeyActive[2].Load() {
		t.Fatal("secondary key flag must not be set for the PG secret")
	}
}
