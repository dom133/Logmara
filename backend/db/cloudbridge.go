package db

import (
	"database/sql"
	"fmt"

	"logmara/model"
)

// GetCloudBridgeState returns this installation's cloud identity, or
// sql.ErrNoRows if it hasn't enrolled yet - see model.CloudBridgeState.
func GetCloudBridgeState(db *sql.DB) (*model.CloudBridgeState, error) {
	var s model.CloudBridgeState
	err := db.QueryRow(`SELECT id, instance_id, broker_host, ca_cert, client_cert, client_key, enrolled_at
		FROM cloud_bridge ORDER BY id LIMIT 1`).
		Scan(&s.ID, &s.InstanceID, &s.BrokerHost, &s.CACert, &s.ClientCert, &s.ClientKey, &s.EnrolledAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// SaveCloudBridgeState persists the result of a successful enrollment -
// called at most once in the lifetime of an installation (see
// cloudbridge.enroll, which checks GetCloudBridgeState first and never
// re-enrolls once a row exists). Rejected by the table having no
// meaningful composite key beyond its own id if called twice - callers
// must not rely on this for idempotency, only on checking
// GetCloudBridgeState themselves first, exactly as cloudbridge.enroll does.
func SaveCloudBridgeState(db *sql.DB, instanceID, brokerHost, caCert, clientCert, clientKey string) error {
	_, err := db.Exec(`INSERT INTO cloud_bridge (instance_id, broker_host, ca_cert, client_cert, client_key)
		VALUES ($1, $2, $3, $4, $5)`,
		instanceID, brokerHost, caCert, clientCert, clientKey)
	if err != nil {
		return fmt.Errorf("save cloud bridge state: %w", err)
	}
	return nil
}
