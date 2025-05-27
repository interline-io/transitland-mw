package river

import (
	"context"
	"testing"
	"time"

	"github.com/interline-io/transitland-mw/jobs"
	"github.com/interline-io/transitland-mw/jobs/jobtest"
	"github.com/interline-io/transitland-mw/testutil"
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

	newQueue := func(queuePrefix string) jobs.JobQueue {
		q, err := NewRiverJobs(dbPool, queuePrefix)
		if err != nil {
			panic(err)
		}
		q2 := jobs.NewJobLogger(q)
		q2.AddQueue("default", 8)
		return q2
	}
	jobtest.TestJobQueue(t, newQueue)
}
