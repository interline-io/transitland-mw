package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/interline-io/log"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/rs/zerolog"
)

func init() {
	var _ JobQueue = &RiverJobs{}
}

//////////////

type riverJobArgs struct {
	Job
}

func (r riverJobArgs) Kind() string {
	return "riverJobArgs"
}

//////////////

type RiverJobs struct {
	dbPool       *pgxpool.Pool
	queueName    string
	riverWorkers *river.Workers
	riverClient  *river.Client[pgx.Tx]
	middlewares  []JobMiddleware
	log          zerolog.Logger
}

func NewRiverJobs(dbPool *pgxpool.Pool, queueName string) (*RiverJobs, error) {
	riverWorkers := river.NewWorkers()
	riverClient, err := river.NewClient(riverpgxv5.New(dbPool), &river.Config{
		Queues: map[string]river.QueueConfig{
			queueName: {MaxWorkers: 100},
		},
		Workers: riverWorkers,
	})
	if err != nil {
		return nil, err
	}
	return &RiverJobs{
		dbPool:       dbPool,
		riverWorkers: riverWorkers,
		riverClient:  riverClient,
		queueName:    queueName,
		log:          log.Logger.With().Str("runner", "river").Str("queue", queueName).Logger(),
	}, nil
}

func (w *RiverJobs) Use(mwf JobMiddleware) {
	w.middlewares = append(w.middlewares, mwf)
}

func (w *RiverJobs) AddWorker(queue string, getWorker GetWorker, count int) error {
	// fmt.Println("RiverJobs AddWorker")
	// Use WorkFunc to simplify code as a closure vs. copying pointers
	river.AddWorker(w.riverWorkers, river.WorkFunc(func(ctx context.Context, outerJob *river.Job[riverJobArgs]) error {
		job := outerJob.Args.Job
		now := time.Now().In(time.UTC).Unix()
		if job.JobDeadline > 0 && now > job.JobDeadline {
			w.log.Trace().Int64("job_deadline", job.JobDeadline).Int64("now", now).Msg("job skipped - deadline in past")
			return nil
		}
		runner, err := getWorker(job)
		if err != nil {
			return river.JobCancel(err)
		}
		if runner == nil {
			return river.JobCancel(errors.New("no job"))
		}
		for _, mwf := range w.middlewares {
			runner = mwf(runner)
			if runner == nil {
				return river.JobCancel(errors.New("no job after middleware"))
			}
		}
		if err := runner.Run(context.TODO(), job); err != nil {
			w.log.Trace().Err(err).Msg("job failed")
			return river.JobCancel(err)
		}
		return nil
	}))
	return nil
}

func (w *RiverJobs) AddJob(job Job) error {
	w.log.Info().Interface("job", job).Msg("jobs: adding job")
	ctx := context.Background()
	tx, err := w.dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	insertOpts := river.InsertOpts{}
	insertOpts.Queue = w.queueName
	if job.Queue != "" {
		insertOpts.Queue = w.queueName
	}
	if job.Unique {
		insertOpts.UniqueOpts = river.UniqueOpts{
			ByArgs:   true,
			ByPeriod: 24 * time.Hour,
			ByState:  []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStateRunning, rivertype.JobStateScheduled},
		}
	}
	if _, err = w.riverClient.InsertTx(ctx, tx, riverJobArgs{Job: job}, &insertOpts); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (w *RiverJobs) Run() error {
	w.log.Info().Msg("jobs: run")
	ctx := context.Background()
	if err := w.riverClient.Start(ctx); err != nil {
		return err
	}
	<-w.riverClient.Stopped()
	return nil
}

func (w *RiverJobs) Stop() error {
	w.log.Info().Msg("jobs: stop")
	ctx := context.Background()
	if err := w.riverClient.Stop(ctx); err != nil {
		return err
	}
	<-w.riverClient.Stopped()
	return nil
}
