package jobs

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var (
	feeds = []string{"BA", "SF", "AC", "CT"}
)

type testWorker struct {
	count *int64
}

func (t *testWorker) Run(ctx context.Context, _ Job) error {
	fmt.Printf("testWorker: %#v\n", t)
	time.Sleep(10 * time.Millisecond)
	atomic.AddInt64(t.count, 1)
	return nil
}

func checkErr(t testing.TB, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

func testJobQueue(t *testing.T, newQueue func(string) JobQueue) {
	queueName := func(t testing.TB) string {
		tName := strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
		return fmt.Sprintf("%s-%d-%d", tName, os.Getpid(), time.Now().UnixNano())
	}
	sleepyTime := 2 * time.Second
	t.Run("simple", func(t *testing.T) {
		// Ugly :(
		rtJobs := newQueue(queueName(t))
		// Add workers
		count := int64(0)
		testGetWorker := func(job Job) (JobWorker, error) {
			return &testWorker{count: &count}, nil
		}
		if err := rtJobs.AddWorker("", testGetWorker, 1); err != nil {
			t.Fatal(err)
		}
		// Add jobs
		for _, feed := range feeds {
			if err := rtJobs.AddJob(Job{JobType: "test", JobArgs: JobArgs{"feed_id": feed}}); err != nil {
				t.Fatal(err)
			}
		}
		// Run
		go func() {
			time.Sleep(sleepyTime)
			rtJobs.Stop()
		}()
		if err := rtJobs.Run(); err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, len(feeds), int(count))
	})
	t.Run("unique", func(t *testing.T) {
		// Abuse the job queue
		rtJobs := newQueue(queueName(t))
		// Add workers
		count := int64(0)
		testGetWorker := func(job Job) (JobWorker, error) {
			return &testWorker{count: &count}, nil
		}
		rtJobs.AddWorker("", testGetWorker, 4)
		// Add jobs
		for i := 0; i < 10; i++ {
			// 1 job: j=0
			for j := 0; j < 10; j++ {
				job := Job{JobType: "testUnique", Unique: true, JobArgs: JobArgs{"test": fmt.Sprintf("n:%d", j/10)}}
				checkErr(t, rtJobs.AddJob(job))
			}
			// 3 jobs; j=3, j=6, j=9... j=0 is not unique
			for j := 0; j < 10; j++ {
				job := Job{JobType: "testUnique", Unique: true, JobArgs: JobArgs{"test": fmt.Sprintf("n:%d", j/3)}}
				checkErr(t, rtJobs.AddJob(job))
			}
			// 10 jobs: j=0, j=0, j=2, j=2, j=4, j=4, j=6 j=6, j=8, j=8
			for j := 0; j < 10; j++ {
				job := Job{JobType: "testNotUnique", JobArgs: JobArgs{"test": fmt.Sprintf("n:%d", j/2)}}
				checkErr(t, rtJobs.AddJob(job))
			}
		}
		// Run
		go func() {
			time.Sleep(sleepyTime)
			rtJobs.Stop()
		}()
		rtJobs.Run()
		assert.Equal(t, int64(104), count)
	})
	t.Run("deadline", func(t *testing.T) {
		rtJobs := newQueue(queueName(t))
		// Add workers
		count := int64(0)
		testGetWorker := func(job Job) (JobWorker, error) {
			w := testWorker{count: &count}
			return &w, nil
		}
		rtJobs.AddWorker("", testGetWorker, 1)
		// Add jobs
		rtJobs.AddJob(Job{JobType: "testUnique", JobArgs: JobArgs{"test": "test"}, JobDeadline: 0})
		rtJobs.AddJob(Job{JobType: "testUnique", JobArgs: JobArgs{"test": "test"}, JobDeadline: time.Now().Add(1 * time.Hour).Unix()})
		rtJobs.AddJob(Job{JobType: "testUnique", JobArgs: JobArgs{"test": "test"}, JobDeadline: time.Now().Add(-1 * time.Hour).Unix()})
		// Run
		go func() {
			time.Sleep(sleepyTime)
			rtJobs.Stop()
		}()
		rtJobs.Run()
		assert.Equal(t, int64(2), count)
	})
	t.Run("middleware", func(t *testing.T) {
		rtJobs := newQueue(queueName(t))
		// Add middleware
		jwCount := int64(0)
		rtJobs.Use(func(w JobWorker) JobWorker {
			return &testJobMiddleware{
				JobWorker: w,
				jobCount:  &jwCount,
			}
		})
		// Add workers
		count := int64(0)
		testGetWorker := func(job Job) (JobWorker, error) {
			w := testWorker{count: &count}
			return &w, nil
		}
		rtJobs.AddWorker("", testGetWorker, 1)
		rtJobs.AddJob(Job{JobType: "testMiddleware", JobArgs: JobArgs{"mw": "ok1"}})
		rtJobs.AddJob(Job{JobType: "testMiddleware", JobArgs: JobArgs{"mw": "ok2"}})
		// Run
		go func() {
			time.Sleep(sleepyTime)
			rtJobs.Stop()
		}()
		rtJobs.Run()
		assert.Equal(t, int64(2), count)
		assert.Equal(t, int64(2*10), jwCount)
	})
}

type testJobMiddleware struct {
	jobCount *int64
	JobWorker
}

func (w *testJobMiddleware) Run(ctx context.Context, job Job) error {
	atomic.AddInt64(w.jobCount, 10)
	if err := w.JobWorker.Run(ctx, job); err != nil {
		return err
	}
	return nil
}
