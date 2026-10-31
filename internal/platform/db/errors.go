package db

import (
	"errors"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// IsUniqueViolation indique qu'une écriture a heurté une contrainte UNIQUE.
func IsUniqueViolation(err error) bool {
	var e *sqlite.Error
	return errors.As(err, &e) && e.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

// IsForeignKeyViolation indique qu'une écriture désigne une ligne absente.
func IsForeignKeyViolation(err error) bool {
	var e *sqlite.Error
	return errors.As(err, &e) && e.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
}
