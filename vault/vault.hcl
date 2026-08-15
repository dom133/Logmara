# Vault server configuration for HA deployment.
#
# Storage: raft (built-in, no external dependency)
# Listener: plain TCP, no per-service TLS — traffic runs over the encrypted
# overlay network (see `--opt encrypted` in scripts/swarm-bootstrap.sh)
# UI: enabled on port 8200
#
# This file is shared by all three vault-* services (same Docker config).
# node_id is intentionally left unset so each node generates and persists
# its own UUID under its (per-node) raft data volume. api_addr/cluster_addr
# are likewise left unset here and supplied per-node via the VAULT_API_ADDR
# / VAULT_CLUSTER_ADDR environment variables in docker-stack.vault.yml —
# hardcoding them here would make every node advertise the same address.

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = 1
  cluster_address = "0.0.0.0:8201"
}

# The container doesn't have (and Swarm services can't easily grant) the
# IPC_LOCK capability Vault wants for mlock (keeps secrets out of swap).
# Without this, Vault disables mlock itself anyway and just logs a
# warning every start - this silences that instead of fighting it.
disable_mlock = true

storage "raft" {
  path = "/vault/data"

  retry_join {
    leader_api_addr = "http://vault-1:8200"
  }

  retry_join {
    leader_api_addr = "http://vault-2:8200"
  }

  retry_join {
    leader_api_addr = "http://vault-3:8200"
  }
}

ui = true
