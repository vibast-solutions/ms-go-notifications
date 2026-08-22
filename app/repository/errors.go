package repository

import (
	"errors"

	"github.com/go-sql-driver/mysql"
)

// mysqlErrDupEntry is MySQL's ER_DUP_ENTRY (unique constraint violation).
const mysqlErrDupEntry = 1062

// ErrDuplicateKey is returned by writes that violate a unique constraint (e.g.
// the (api_key_id, idempotency_key) index during an idempotency race). Callers
// detect it with errors.Is and re-read the existing row.
var ErrDuplicateKey = errors.New("duplicate key")

// isDuplicateKey reports whether err is a MySQL duplicate-entry error.
func isDuplicateKey(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == mysqlErrDupEntry
}
