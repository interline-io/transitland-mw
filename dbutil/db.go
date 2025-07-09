package dbutil

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/interline-io/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/jmoiron/sqlx/reflectx"
	"go.nhat.io/otelsql"
	"go.opentelemetry.io/otel/attribute"
)

var matchFirstCap = regexp.MustCompile("(.)([A-Z][a-z]+)")
var matchAllCap = regexp.MustCompile("([a-z0-9])([A-Z])")

func toSnakeCase(str string) string {
	snake := matchFirstCap.ReplaceAllString(str, "${1}_${2}")
	snake = matchAllCap.ReplaceAllString(snake, "${1}_${2}")
	return strings.ToLower(snake)
}

// InitOTelDriver initializes the OpenTelemetry-wrapped driver
func InitOTelDriver(serviceName string) {
	opts := []otelsql.DriverOption{
		otelsql.WithDefaultAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.service", serviceName),
		),
	}
	otelsql.Register("pgx-otel", opts...)
}

// configureDB sets up common database configuration
func configureDB(db *sqlx.DB) error {
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.Ping(); err != nil {
		log.Error().Err(err).Msgf("could not connect to database")
		return err
	}
	db.Mapper = reflectx.NewMapperFunc("db", toSnakeCase)
	return nil
}

// OpenDBPoolWithOTel opens a database pool with OpenTelemetry tracing enabled
func OpenDBPoolWithOTel(ctx context.Context, url string, serviceName string, enableTracing bool) (*pgxpool.Pool, *sqlx.DB, error) {
	if enableTracing {
		InitOTelDriver(serviceName)
	}

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, nil, err
	}

	var underlyingDB *sql.DB
	if enableTracing {
		underlyingDB, err = sql.Open("pgx-otel", url)
		if err != nil {
			log.Error().Err(err).Msg("could not open database")
			return nil, nil, err
		}
	} else {
		underlyingDB = stdlib.OpenDBFromPool(pool)
	}

	db := sqlx.NewDb(underlyingDB, "pgx")
	if enableTracing {
		db = sqlx.NewDb(underlyingDB, "pgx-otel")
	}

	if err := configureDB(db); err != nil {
		return nil, nil, err
	}
	return pool, db.Unsafe(), nil
}

func OpenDBPool(ctx context.Context, url string) (*pgxpool.Pool, *sqlx.DB, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, nil, err
	}
	db := sqlx.NewDb(stdlib.OpenDBFromPool(pool), "pgx")
	if err := configureDB(db); err != nil {
		return nil, nil, err
	}
	return pool, db.Unsafe(), nil
}

// OpenDBWithOTel opens a database connection with OpenTelemetry tracing enabled
func OpenDBWithOTel(url string, serviceName string, enableTracing bool) (*sqlx.DB, error) {
	if enableTracing {
		InitOTelDriver(serviceName)
	}

	var underlyingDB *sql.DB
	var err error

	if enableTracing {
		underlyingDB, err = sql.Open("pgx-otel", url)
	} else {
		underlyingDB, err = sql.Open("pgx", url)
	}

	if err != nil {
		log.Error().Err(err).Msg("could not open database")
		return nil, err
	}

	db := sqlx.NewDb(underlyingDB, "pgx")
	if enableTracing {
		db = sqlx.NewDb(underlyingDB, "pgx-otel")
	}

	if err := configureDB(db); err != nil {
		return nil, err
	}
	return db.Unsafe(), nil
}

func OpenDB(url string) (*sqlx.DB, error) {
	db, err := sqlx.Open("pgx", url)
	if err != nil {
		log.Error().Err(err).Msg("could not open database")
		return nil, err
	}
	if err := configureDB(db); err != nil {
		return nil, err
	}
	return db.Unsafe(), nil
}

// Select runs a query and reads results into dest.
func Select(ctx context.Context, db sqlx.Ext, q sq.SelectBuilder, dest interface{}) error {
	useStatement := false
	q = q.PlaceholderFormat(sq.Dollar)
	qstr, qargs, err := q.ToSql()
	if err == nil {
		if a, ok := db.(sqlx.PreparerContext); ok && useStatement {
			stmt, prepareErr := sqlx.PreparexContext(ctx, a, qstr)
			if prepareErr != nil {
				err = prepareErr
			} else {
				err = stmt.SelectContext(ctx, dest, qargs...)
			}
		} else if a, ok := db.(sqlx.QueryerContext); ok {
			err = sqlx.SelectContext(ctx, a, dest, qstr, qargs...)
		} else {
			err = sqlx.Select(db, dest, qstr, qargs...)
		}
	}
	if ctx.Err() == context.Canceled {
		log.Trace().Err(err).Str("query", qstr).Interface("args", qargs).Msg("query canceled")
	} else if err != nil {
		log.Error().Err(err).Str("query", qstr).Interface("args", qargs).Msg("query failed")
	}
	return err
}

// Get runs a query and reads a single result into dest.
func Get(ctx context.Context, db sqlx.Ext, q sq.SelectBuilder, dest interface{}) error {
	useStatement := false
	q = q.PlaceholderFormat(sq.Dollar)
	qstr, qargs, err := q.ToSql()
	if err == nil {
		if a, ok := db.(sqlx.PreparerContext); ok && useStatement {
			stmt, prepareErr := sqlx.PreparexContext(ctx, a, qstr)
			if prepareErr != nil {
				err = prepareErr
			} else {
				err = stmt.GetContext(ctx, dest, qargs...)
			}
		} else if a, ok := db.(sqlx.QueryerContext); ok {
			err = sqlx.GetContext(ctx, a, dest, qstr, qargs...)
		} else {
			err = sqlx.Get(db, dest, qstr, qargs...)
		}
	}
	if ctx.Err() == context.Canceled {
		log.Trace().Err(err).Str("query", qstr).Interface("args", qargs).Msg("query canceled")
	} else if err != nil {
		log.Error().Err(err).Str("query", qstr).Interface("args", qargs).Msg("query failed")
	}
	return err
}
