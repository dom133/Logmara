# Instrukcje dla AI

## Migracje bazy danych (backend/db/db.go)

`Migrate()` pomija ponowne wykonanie wszystkich instrukcji DDL, jeśli zapisana
w tabeli `schema_version` wersja jest już `>= schemaVersion`. Oznacza to, że:

- **Za każdym razem, gdy dodajesz, zmieniasz lub usuwasz cokolwiek w listach
  `statements`, `partitionStmts` lub `postStmts` w `backend/db/db.go`
  (nowa tabela, nowa kolumna, nowy indeks, nowy widok zmaterializowany itp.),
  MUSISZ zwiększyć stałą `schemaVersion` o 1.**
- Jeśli tego nie zrobisz, instancje z już zapisaną (równą lub wyższą) wersją
  schematu nigdy nie wykonają nowo dodanej instrukcji — zostanie po cichu
  pominięta przy starcie API.
- `schemaVersion` zwiększaj tylko raz na PR/commit z realną zmianą schematu —
  nie zwiększaj jej przy zmianach niedotyczących `Migrate()`.
