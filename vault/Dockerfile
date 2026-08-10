# Vault 1.16.0 with Redis secrets plugin for dynamic credentials
FROM hashicorp/vault:1.16.0

# Download the Redis secrets plugin binary
RUN curl -fsSL https://releases.hashicorp.com/vault-plugin-secrets-redis/0.15.0/vault-plugin-secrets-redis_0.15.0_linux_amd64.zip -o /tmp/redis-plugin.zip && \
    unzip /tmp/redis-plugin.zip -d /tmp/ && \
    mv /tmp/vault-plugin-secrets-redis /usr/local/libexec/vault/ && \
    chmod 755 /usr/local/libexec/vault/vault-plugin-secrets-redis && \
    rm -rf /tmp/redis-plugin.zip /tmp/vault-plugin-secrets-redis

COPY vault.hcl /vault/config/vault.hcl
COPY vault-agent.hcl /vault/config/vault-agent.hcl

EXPOSE 8200

ENTRYPOINT ["vault", "server"]
