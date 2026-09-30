// Package repositories is LOADLINE's persistence layer: parameterized SQL
// against PostgreSQL, one repository per aggregate.
//
// Dependency direction (enforced by imports):
//
//	API layer → repositories → db → pgx
//
// Repositories know nothing about ConnectRPC, protobuf, HTTP, or the
// simulation engine. They take and return workspace model values, which is
// what makes them testable in isolation and keeps SQL out of API handlers.
//
// Every value that comes from a caller is passed as a query parameter. No
// statement is ever assembled by string formatting.
package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kekubhai/Loadline/apps/simulator/internal/workspace"
)

// Querier is the subset of pgx behavior the repositories use. Both
// *pgxpool.Pool and pgx.Tx satisfy it, which lets a repository method run
// either standalone or inside a transaction without duplicating its SQL.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// wrapForeignKey turns a foreign-key violation into a not-found error: the
// referenced row (a project for an architecture, a version for a workload)
// does not exist. Only the caller's mistake is reported; the raw database
// error would otherwise leak table and constraint names to clients.
func wrapForeignKey(err error, wrapper error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return fmt.Errorf("%w: referenced %s does not exist", workspace.ErrNotFound, pgErr.ConstraintName)
	}
	return wrapper
}
