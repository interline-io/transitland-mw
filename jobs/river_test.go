package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func getTestWorker(job Job) (JobWorker, error) {
	var r JobWorker
	class := job.JobType
	switch class {
	case "test":
		r = &testWorker{}
	default:
		return nil, errors.New("unknown job type")
	}
	// Load json
	jw, err := json.Marshal(job.JobArgs)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(jw, r); err != nil {
		return nil, err
	}
	return r, nil
}

type SortArgs struct {
	// Strings is a slice of strings to sort.
	Strings []string `json:"strings"`
}

func (SortArgs) Kind() string { return "sort" }

type SortWorker struct {
	// An embedded WorkerDefaults sets up default methods to fulfill the rest of
	// the Worker interface:
	river.WorkerDefaults[SortArgs]
}

func (w *SortWorker) Work(ctx context.Context, job *river.Job[SortArgs]) error {
	sort.Strings(job.Args.Strings)
	fmt.Printf("Sorted strings: %+v\n", job.Args.Strings)
	return nil
}

func TestRiverJobs(t *testing.T) {
	ctx := context.Background()
	dburl := os.Getenv("TL_DATABASE_URL")
	dbPool, err := pgxpool.New(ctx, dburl)
	if err != nil {
		t.Fatal(err)
	}
	defer dbPool.Close()

	workers := river.NewWorkers()
	// AddWorker panics if the worker is already registered or invalid:
	river.AddWorker(workers, &SortWorker{})

	riverClient, err := river.NewClient(riverpgxv5.New(dbPool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 100},
		},
		Workers: workers,
	})
	if err != nil {
		// handle error
	}

	_, err = riverClient.Insert(ctx, SortArgs{
		Strings: []string{
			"whale", "tiger", "bear",
		},
	}, nil)
	if err != nil {
		// handle error
	}

	// Run the client inline. All executed jobs will inherit from ctx:
	if err := riverClient.Start(ctx); err != nil {
		// handle error
	}
	time.Sleep(10 * time.Second)

	// q, err := NewRiverJobs(dbPool)
	// if err != nil {
	// 	t.Fatal(err)
	// }

	// count := int64(0)
	// testGetWorker := func(job Job) (JobWorker, error) {
	// 	fmt.Println("testGetWorker")
	// 	return &testWorker{count: &count}, nil
	// }
	// if err := q.AddWorker("test", getTestWorker, 1); err != nil {
	// 	t.Fatal(err)
	// }

	// go func() { q.Run() }()

	// fmt.Println("INSERT")
	// for i := 0; i < 10; i++ {
	// 	fmt.Println("\tinsert:", i)
	// 	if err := q.AddJob(Job{JobType: "test", JobArgs: map[string]any{"count": i}}); err != nil {
	// 		t.Fatal(err)
	// 	}
	// }

	// time.Sleep(30 * time.Second)

	// newQueue := func(prefix string) JobQueue {
	// 	q, err := NewRiverJobs(dbPool)
	// 	if err != nil {
	// 		panic(err)
	// 	}
	// 	return q
	// }
	// testJobQueue(t, newQueue)

}
