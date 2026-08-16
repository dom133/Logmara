package db

import (
	"database/sql"
	"fmt"

	"logmara/model"
)

// GetCloudBridgeState returns this installation's cloud identity, or
// sql.ErrNoRows if it hasn't enrolled yet - see model.CloudBridgeState. The
// cert columns are nullable (a freshly paired row has none yet, until
// SaveCertificates is called), so they're COALESCEd to "" rather than
// scanned as NULL.
func GetCloudBridgeState(db *sql.DB) (*model.CloudBridgeState, error) {
	var s model.CloudBridgeState
	err := db.QueryRow(`SELECT id, instance_id, broker_host,
		COALESCE(ca_cert, ''), COALESCE(client_cert, ''), COALESCE(client_key, ''), enrolled_at
		FROM cloud_bridge ORDER BY id LIMIT 1`).
		Scan(&s.ID, &s.InstanceID, &s.BrokerHost, &s.CACert, &s.ClientCert, &s.ClientKey, &s.EnrolledAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// SaveCloudBridgeState persists the result of a successful pairing - called
// at most once in the lifetime of an installation (see cloudbridge.enroll,
// which checks GetCloudBridgeState first and never re-enrolls once a row
// exists). Rejected by the table having no meaningful composite key beyond
// its own id if called twice - callers must not rely on this for
// idempotency, only on checking GetCloudBridgeState themselves first,
// exactly as cloudbridge.enroll does. Certs aren't known yet at this point -
// see UpdateCloudBridgeCertificates for the separate step that adds them.
func SaveCloudBridgeState(db *sql.DB, instanceID, brokerHost string) error {
	_, err := db.Exec(`INSERT INTO cloud_bridge (instance_id, broker_host)
		VALUES ($1, $2)`,
		instanceID, brokerHost)
	if err != nil {
		return fmt.Errorf("save cloud bridge state: %w", err)
	}
	return nil
}

// UpdateCloudBridgeCertificates (re)writes this installation's mTLS
// certificate material - called by cloudbridge.SaveCertificates, both right
// after pairing and later as a repair path if a bad cert needs replacing.
// Unlike instance_id/broker_host, these columns are meant to be overwritable.
func UpdateCloudBridgeCertificates(db *sql.DB, caCert, clientCert, clientKey string) error {
	res, err := db.Exec(`UPDATE cloud_bridge SET ca_cert = $1, client_cert = $2, client_key = $3`,
		caCert, clientCert, clientKey)
	if err != nil {
		return fmt.Errorf("save cloud bridge certificates: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("save cloud bridge certificates: not paired yet")
	}
	return nil
}

// DeleteCloudBridgeState removes this installation's cloud identity and
// certificates entirely - called by cloudbridge.Disconnect. Unlike every
// other function here, this undoes SaveCloudBridgeState rather than
// building on it: afterward GetCloudBridgeState goes back to sql.ErrNoRows,
// so the installation can be paired again from scratch with a new link.
func DeleteCloudBridgeState(db *sql.DB) error {
	if _, err := db.Exec(`DELETE FROM cloud_bridge`); err != nil {
		return fmt.Errorf("delete cloud bridge state: %w", err)
	}
	return nil
}
