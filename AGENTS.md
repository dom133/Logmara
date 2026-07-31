# Instructions

- ALWAYS ask the user for confirmation before running `git commit` and `git push`.
- Never commit or push changes unless the user explicitly says so.
- Before making any changes to the application, first present a plan of action and ask the user if it's correct.
- Whenever DDL statements (ALTER TABLE, CREATE TABLE, etc.) are added or modified in `backend/db/db.go` runSchemaMigration, ALWAYS bump `const schemaVersion` by +1. If schemaVersion is not bumped, deployed instances will skip the new migration silently.