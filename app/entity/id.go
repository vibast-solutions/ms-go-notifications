// Package entity holds the domain structs mirroring the database schema.
// IDs are ULIDs stored as CHAR(26): sortable by creation time, URL-safe.
package entity

import "github.com/oklog/ulid/v2"

// NewID returns a new ULID string for use as a primary key.
func NewID() string {
	return ulid.Make().String()
}
