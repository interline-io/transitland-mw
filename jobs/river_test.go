package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/interline-io/transitland-dbutil/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRiverJobs(t *testing.T) {
	// Setup db pool
	dburl, v, ok := testutil.CheckEnv("TL_TEST_SERVER_DATABASE_URL")
	if !ok {
		t.Skipf("no database, set %s", v)
		return
	}

	// MustOpenTestDB

	ctx, cancelFunc := context.WithTimeout(context.Background(), time.Duration(10*time.Second))
	defer cancelFunc()
	dbPool, err := pgxpool.New(ctx, dburl)
	if err != nil {
		t.Fatal(err)
	}
	defer dbPool.Close()

	newQueue := func(queueName string) JobQueue {
		q, err := NewRiverJobs(dbPool, queueName)
		if err != nil {
			panic(err)
		}
		q.Use(newLog())
		return q
	}
	testJobQueue(t, newQueue)
}
