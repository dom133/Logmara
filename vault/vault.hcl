# Vault server configuration for HA deployment.
#
# Storage: raft (built-in, no external dependency)
# Listener: TCP with TLS (certs must be pre-provisioned at /vault/tls/)
# UI: enabled on port 8200

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = 1
  cluster_address = "0.0.0.0:8201"
}

storage "raft" {
  path = "/vault/data"
  node_id = "vault-node"

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

api_addr = "http://vault:8200"
cluster_addr = "https://vault:8201"
