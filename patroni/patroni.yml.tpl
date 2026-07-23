scope: syslog-pg
namespace: /syslog/
name: ${PATRONI_NAME}

restapi:
  listen: 0.0.0.0:8008
  connect_address: ${PATRONI_NAME}:8008

etcd3:
  hosts: etcd1:2379,etcd2:2379,etcd3:2379

bootstrap:
  dcs:
    ttl: 30
    loop_wait: 10
    retry_timeout: 10
    maximum_lag_on_failover: 1048576
    postgresql:
      use_pg_rewind: true
      parameters:
        # Mirrors the tuning previously passed via `command:` to postgres in
        # docker-compose.yml.
        shared_buffers: 1GB
        effective_cache_size: 3GB
        work_mem: 32MB
        maintenance_work_mem: 256MB
        wal_level: replica
        hot_standby: "on"
        max_wal_senders: 10
        max_replication_slots: 10
  initdb:
    - encoding: UTF8
    - data-checksums
  pg_hba:
    - host replication replicator 0.0.0.0/0 md5
    - host all all 0.0.0.0/0 md5
  # Creates the app role/database (POSTGRES_USER/POSTGRES_DB) once, right
  # after the first cluster member bootstraps. Patroni invokes this only on
  # the node that performs initdb, with PGPASSWORD already set to the
  # superuser password.
  post_init: /scripts/post_init.sh

postgresql:
  listen: 0.0.0.0:5432
  connect_address: ${PATRONI_NAME}:5432
  data_dir: /home/postgres/pgdata
  pgpass: /tmp/pgpass
  authentication:
    replication:
      username: replicator
      password: ${PATRONI_REPLICATION_PASSWORD}
    superuser:
      username: postgres
      password: ${PATRONI_SUPERUSER_PASSWORD}
    rewind:
      username: postgres
      password: ${PATRONI_SUPERUSER_PASSWORD}

tags:
  nofailover: false
  noloadbalance: false
  clonefrom: false
  nosync: false
