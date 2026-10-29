#!/bin/sh
set -eu
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
  -v casecite_password="$CASECITE_DB_PASSWORD" -v goodmem_password="$GOODMEM_DB_PASSWORD" <<'SQL'
CREATE ROLE casecite LOGIN PASSWORD :'casecite_password';
CREATE DATABASE casecite OWNER casecite;
CREATE ROLE goodmem LOGIN PASSWORD :'goodmem_password';
CREATE DATABASE goodmem OWNER goodmem;
\connect goodmem
CREATE EXTENSION IF NOT EXISTS vector;
SQL
