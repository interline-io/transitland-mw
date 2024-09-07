package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/interline-io/log"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func init() {
	var _ JobQueue = &RiverJobs{}
}

//////////////

type riverJobArgs struct {
	Job
}

func (r riverJobArgs) Kind() string {
	return "default"
}

// func (r riverJobArgs) InsertOpts() river.InsertOpts {
// 	opts := river.InsertOpts{}
// 	if r.Job.Queue != "" {
// 		opts.Queue = r.Job.Queue
// 	}
// 	if r.Job.Unique {
// 		// Unique for 24 hour lock
// 		// Allow to be rescheduled after completion
// 		opts.UniqueOpts = river.UniqueOpts{
// 			ByArgs:   true,
// 			ByPeriod: 24 * time.Hour,
// 			ByState:  []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStateRunning, rivertype.JobStateScheduled},
// 		}
// 	}
// 	return opts
// }

//////////////

type riverWorker struct {
	getWorker   GetWorker
	middlewares []JobMiddleware
	river.WorkerDefaults[riverJobArgs]
}

func (r *riverWorker) Work(ctx context.Context, outerJob *river.Job[riverJobArgs]) error {
	fmt.Println("WORK:", outerJob)
	job := outerJob.Args.Job
	now := time.Now().In(time.UTC).Unix()
	if job.JobDeadline > 0 && now > job.JobDeadline {
		log.Trace().Int64("job_deadline", job.JobDeadline).Int64("now", now).Msg("job skipped - deadline in past")
		return nil
	}
	runner, err := r.getWorker(job)
	if err != nil {
		return river.JobCancel(err)
	}
	if runner == nil {
		return river.JobCancel(errors.New("no job"))
	}
	for _, mwf := range r.middlewares {
		runner = mwf(runner)
		if runner == nil {
			return river.JobCancel(errors.New("no job after middleware"))
		}
	}
	if err := runner.Run(context.TODO(), job); err != nil {
		log.Trace().Err(err).Msg("job failed")
		return river.JobCancel(err)
	}
	return nil
}

//////////////

type RiverJobs struct {
	dbPool       *pgxpool.Pool
	riverWorkers *river.Workers
	riverClient  *river.Client[pgx.Tx]
	middlewares  []JobMiddleware
}

func NewRiverJobs(dbPool *pgxpool.Pool) (*RiverJobs, error) {
	riverWorkers := river.NewWorkers()
	return &RiverJobs{
		dbPool:       dbPool,
		riverWorkers: riverWorkers,
	}, nil
}

func (w *RiverJobs) Use(mwf JobMiddleware) {
	w.middlewares = append(w.middlewares, mwf)
}

func (w *RiverJobs) AddJob(job Job) error {
	fmt.Println("ADDJOB")
	ctx := context.Background()
	tx, err := w.dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = w.riverClient.InsertTx(ctx, tx, riverJobArgs{Job: job}, nil)
	if err != nil {
		panic(err)
	}
	return err
}

func (w *RiverJobs) AddWorker(queue string, getWorker GetWorker, count int) error {
	fmt.Println("ADDWORKER")
	return river.AddWorkerSafely(w.riverWorkers, &riverWorker{
		getWorker:   getWorker,
		middlewares: append([]JobMiddleware{}, w.middlewares...),
	})
}

func (w *RiverJobs) Run() error {
	fmt.Println("RUN")
	ctx := context.Background()
	riverClient, err := river.NewClient(riverpgxv5.New(w.dbPool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 100},
		},
		Workers: w.riverWorkers,
	})
	if err != nil {
		return err
	}
	w.riverClient = riverClient
	if err := w.riverClient.Start(ctx); err != nil {
		return err
	}
	<-w.riverClient.Stopped()
	return nil
}

func (w *RiverJobs) Stop() error {
	fmt.Println("STOP")
	ctx := context.Background()
	return w.riverClient.Stop(ctx)
}
