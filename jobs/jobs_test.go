package jobs

import (
	"context"
	"fmt"
	"os"
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

func testJobQueue(t *testing.T, newQueue func(string) JobQueue) {
	t.Run("simple", func(t *testing.T) {
		// Ugly :(
		rtJobs := newQueue(fmt.Sprintf("queue1:%d:%d", os.Getpid(), time.Now().UnixNano()))
		count := int64(0)
		testGetWorker := func(job Job) (JobWorker, error) {
			fmt.Println("testGetWorker")
			return &testWorker{count: &count}, nil
		}
		if err := rtJobs.AddWorker("", testGetWorker, 1); err != nil {
			t.Fatal(err)
		}
		for _, feed := range feeds {
			if err := rtJobs.AddJob(Job{JobType: "test", Unique: false, JobArgs: JobArgs{"feed_id": feed}}); err != nil {
				t.Fatal(err)
			}
		}
		go func() {
			time.Sleep(1000 * time.Millisecond)
			rtJobs.Stop()
		}()
		if err := rtJobs.Run(); err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, len(feeds), int(count))
	})
	// t.Run("unique", func(t *testing.T) {
	// 	// Abuse the job queue
	// 	rtJobs := newQueue(fmt.Sprintf("queue2:%d:%d", os.Getpid(), time.Now().UnixNano()))
	// 	count := int64(0)
	// 	testGetWorker := func(job Job) (JobWorker, error) {
	// 		w := testWorker{count: &count}
	// 		return &w, nil
	// 	}
	// 	for i := 0; i < 10; i++ {
	// 		// 1 job: j=0
	// 		for j := 0; j < 10; j++ {
	// 			job := Job{JobType: "testUnique", Unique: true, JobArgs: JobArgs{"test": fmt.Sprintf("n:%d", j/10)}}
	// 			rtJobs.AddJob(job)
	// 		}
	// 		// 3 jobs; j=3, j=6, j=9... j=0 is not unique
	// 		for j := 0; j < 10; j++ {
	// 			job := Job{JobType: "testUnique", Unique: true, JobArgs: JobArgs{"test": fmt.Sprintf("n:%d", j/3)}}
	// 			rtJobs.AddJob(job)
	// 		}
	// 		// 10 jobs: j=0, j=0, j=2, j=2, j=4, j=4, j=6 j=6, j=8, j=8
	// 		for j := 0; j < 10; j++ {
	// 			job := Job{JobType: "testNotUnique", Unique: false, JobArgs: JobArgs{"test": fmt.Sprintf("n:%d", j/2)}}
	// 			rtJobs.AddJob(job)
	// 		}
	// 	}
	// 	rtJobs.AddWorker("", testGetWorker, 4)
	// 	go func() {
	// 		time.Sleep(1000 * time.Millisecond)
	// 		rtJobs.Stop()
	// 	}()
	// 	rtJobs.Run()
	// 	assert.Equal(t, int64(104), count)
	// })
	// t.Run("deadline", func(t *testing.T) {
	// 	rtJobs := newQueue(fmt.Sprintf("queue3:%d:%d", os.Getpid(), time.Now().UnixNano()))
	// 	count := int64(0)
	// 	testGetWorker := func(job Job) (JobWorker, error) {
	// 		w := testWorker{count: &count}
	// 		return &w, nil
	// 	}
	// 	rtJobs.AddJob(Job{JobType: "testUnique", Unique: false, JobArgs: JobArgs{"test": "test"}, JobDeadline: 0})
	// 	rtJobs.AddJob(Job{JobType: "testUnique", Unique: false, JobArgs: JobArgs{"test": "test"}, JobDeadline: time.Now().Add(1 * time.Hour).Unix()})
	// 	rtJobs.AddJob(Job{JobType: "testUnique", Unique: false, JobArgs: JobArgs{"test": "test"}, JobDeadline: time.Now().Add(-1 * time.Hour).Unix()})
	// 	rtJobs.AddWorker("", testGetWorker, 1)
	// 	go func() {
	// 		time.Sleep(100 * time.Millisecond)
	// 		rtJobs.Stop()
	// 	}()
	// 	rtJobs.Run()
	// 	assert.Equal(t, int64(2), count)
	// })
}
