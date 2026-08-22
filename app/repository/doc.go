// Package repository holds the MySQL data access layer — one file per
// aggregate, raw parameterized SQL, no business logic. Repositories return
// sql.ErrNoRows for missing rows; services translate to domain errors.
package repository
