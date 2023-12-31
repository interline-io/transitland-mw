package testutil

import (
	"os"

	"github.com/go-redis/redis/v8"
	"github.com/interline-io/log"
	"github.com/interline-io/transitland-mw/internal/dbutil"
	"github.com/jmoiron/sqlx"
)

// Test helpers

var db *sqlx.DB

func CheckTestDB() (string, bool) {
	_, a, ok := CheckEnv("TL_TEST_SERVER_DATABASE_URL")
	return a, ok
}

func MustOpenTestDB() *sqlx.DB {
	if db != nil {
		return db
	}
	dburl := os.Getenv("TL_TEST_SERVER_DATABASE_URL")
	var err error
	db, err = dbutil.OpenDB(dburl)
	if err != nil {
		log.Fatal().Err(err).Msgf("database error")
		return nil
	}
	return db
}

func CheckTestRedisClient() (string, bool) {
	_, a, ok := CheckEnv("TL_TEST_REDIS_URL")
	return a, ok
}

func MustOpenTestRedisClient() *redis.Client {
	redisUrl := os.Getenv("TL_TEST_REDIS_URL")
	client := redis.NewClient(&redis.Options{Addr: redisUrl})
	return client
}
