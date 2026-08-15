package rotation

import (
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

const (
	SecretJWT          = "jwt"
	SecretEncryption   = "encryption"
	SecretPostgreSQL   = "postgresql"
	SecretRabbitMQ     = "rabbitmq"
)

// SecretResult holds the outcome of a single secret rotation attempt.
type SecretResult struct {
	Result string    `json:"result"`
	Time   time.Time `json:"time"`
}

type SecretStatus struct {
	Name            string `json:"name"`
	LastRotatedAt   *time.Time `json:"last_rotated_at"`
	LastResult      string     `json:"last_result"`
	HasSecondaryKey bool       `json:"has_secondary_key"`
	RabbitMQHost    string     `json:"rabbitmq_host,omitempty"`
}

type Status struct {
	VaultEnabled     bool         `json:"vault_enabled"`
	RotationInterval time.Duration `json:"rotation_interval"`
	LastRotationAt   *time.Time   `json:"last_rotation_at"`
	NextRotationAt   *time.Time   `json:"next_rotation_at"`
	ManualTriggered  bool         `json:"manual_triggered"`
	Secrets          [4]SecretStatus `json:"secrets"`
	RabbitMQConnected bool        `json:"rabbitmq_connected"`
	RabbitMQHost     string       `json:"rabbitmq_host"`
}

type Tracker struct {
	vaultEnabled    atomic.Bool
	rotationInterval time.Duration
	lastRotationAt  atomic.Value
	nextRotationAt  atomic.Value
	manualTriggered atomic.Bool

	mu      sync.RWMutex
	secrets map[string]*SecretStatus
}

func NewTracker(interval time.Duration) *Tracker {
	t := &Tracker{
		rotationInterval: interval,
		secrets: map[string]*SecretStatus{
			SecretJWT:        {Name: "JWT Secret"},
			SecretEncryption: {Name: "Encryption Key"},
			SecretPostgreSQL: {Name: "PostgreSQL Credentials"},
			SecretRabbitMQ:   {Name: "RabbitMQ Credentials"},
		},
	}
	t.vaultEnabled.Store(false)
	return t
}

func (t *Tracker) SetVaultEnabled(enabled bool) {
	t.vaultEnabled.Store(enabled)
}

func (t *Tracker) IsVaultEnabled() bool {
	return t.vaultEnabled.Load()
}

func (t *Tracker) SetRotationTimestamps(last, next time.Time) {
	t.lastRotationAt.Store(&last)
	t.nextRotationAt.Store(&next)
}

func (t *Tracker) SetSecretResult(secret string, result string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	s, ok := t.secrets[secret]
	if !ok {
		return
	}

	now := time.Now()
	s.LastRotatedAt = &now
	s.LastResult = result

	if secret == SecretJWT || secret == SecretEncryption {
		s.HasSecondaryKey = true
	}
}

func (t *Tracker) ClearSecondaryKey(secret string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	s, ok := t.secrets[secret]
	if !ok {
		return
	}
	s.HasSecondaryKey = false
}

func (t *Tracker) SetRabbitMQHost(rawURL string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	host := ""
	if rawURL != "" {
		u, err := url.Parse(rawURL)
		if err == nil && u.Host != "" {
			user := ""
			if u.User != nil {
				user = u.User.Username()
			}
			if user != "" {
				host = user + "@" + u.Host
			} else {
				host = u.Host
			}
		}
	}
	t.secrets[SecretRabbitMQ].RabbitMQHost = host
}

func (t *Tracker) SetRabbitMQConnected(connected bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.secrets[SecretRabbitMQ].LastResult = "connected"
	if !connected {
		t.secrets[SecretRabbitMQ].LastResult = "disconnected"
	}
}

func (t *Tracker) MarkManualTrigger() {
	t.manualTriggered.Store(true)
}

func (t *Tracker) GetStatus() Status {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var lastRot *time.Time
	if v := t.lastRotationAt.Load(); v != nil {
		lastRot = v.(*time.Time)
	}

	var nextRot *time.Time
	if v := t.nextRotationAt.Load(); v != nil {
		nextRot = v.(*time.Time)
	}

	return Status{
		VaultEnabled:     t.vaultEnabled.Load(),
		RotationInterval: t.rotationInterval,
		LastRotationAt:   lastRot,
		NextRotationAt:   nextRot,
		ManualTriggered:  t.manualTriggered.Load(),
		Secrets: [4]SecretStatus{
			*t.secrets[SecretJWT],
			*t.secrets[SecretEncryption],
			*t.secrets[SecretPostgreSQL],
			*t.secrets[SecretRabbitMQ],
		},
		RabbitMQConnected: t.secrets[SecretRabbitMQ].LastResult == "connected",
		RabbitMQHost:      t.secrets[SecretRabbitMQ].RabbitMQHost,
	}
}
