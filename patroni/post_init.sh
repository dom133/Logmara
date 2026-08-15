#!/bin/sh
# Invoked once by Patroni right after the first cluster member bootstraps
# (see bootstrap.post_init in patroni.yml.tpl). Patroni calls this as
# `post_init.sh <cluster_name>` with PGPASSWORD already set to the
# superuser password and the new instance listening on localhost.
set -e

psql -U postgres -h localhost -v ON_ERROR_STOP=1 <<-SQL
    CREATE ROLE "${POSTGRES_USER}" WITH LOGIN PASSWORD '${POSTGRES_PASSWORD}';
    CREATE DATABASE "${POSTGRES_DB}" OWNER "${POSTGRES_USER}";
SQL
