package jobs

import (
	"testing"

	"github.com/interline-io/transitland-dbutil/testutil"
)

func TestRedisJobs(t *testing.T) {
	// redis jobs and cache
	if a, ok := testutil.CheckTestRedisClient(); !ok {
		t.Skip(a)
		return
	}
	client := testutil.MustOpenTestRedisClient(t)
	newQueue := func(prefix string) JobQueue {
		q := NewRedisJobs(client, prefix)
		q.Use(newLog())
		return q
	}
	testJobQueue(t, newQueue)
}
