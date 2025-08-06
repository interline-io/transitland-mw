package river

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/interline-io/transitland-mw/jobs"
	"github.com/interline-io/transitland-mw/otel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/rs/zerolog"
)

func init() {
	var _ jobs.JobQueue = &RiverJobs{}
}

//////////////

type riverJobArgs struct {
	Queue       string       `json:"queue"`
	JobType     string       `json:"job_type" river:"unique"`
	JobArgs     jobs.JobArgs `json:"job_args" river:"unique"`
	JobDeadline int64        `json:"job_deadline"`
	Unique      bool         `json:"unique"`
}

func (r riverJobArgs) Kind() string {
	return "riverJobArgs"
}

func (r riverJobArgs) ToJob() jobs.Job {
	return jobs.Job{
		Queue:       r.Queue,
		JobType:     r.JobType,
		JobArgs:     r.JobArgs,
		JobDeadline: r.JobDeadline,
		Unique:      r.Unique,
	}
}

func newRiverJobArgsFromJob(job jobs.Job) riverJobArgs {
	return riverJobArgs{
		Queue:       job.Queue,
		JobType:     job.JobType,
		JobArgs:     job.JobArgs,
		JobDeadline: job.JobDeadline,
		Unique:      job.Unique,
	}
}

//////////////

type RiverJobs struct {
	queuePrefix  string
	jobMapper    *jobs.JobMapper
	pool         *pgxpool.Pool
	riverWorkers *river.Workers
	riverClient  *river.Client[pgx.Tx]
	periodicJobs []*river.PeriodicJob
	middlewares  []jobs.JobMiddleware
	log          zerolog.Logger
}

func NewRiverJobs(pool *pgxpool.Pool, queuePrefix string) (*RiverJobs, error) {
	return NewRiverJobsWithMiddleware(pool, queuePrefix)
}

// NewRiverJobsWithOTel creates a new River jobs instance with OpenTelemetry configuration
func NewRiverJobsWithOTel(pool *pgxpool.Pool, queuePrefix string, otelConfig *otel.Config) (*RiverJobs, error) {
	var middlewares []rivertype.Middleware
	if otelMiddleware := otel.NewRiverMiddleware(otelConfig); otelMiddleware != nil {
		middlewares = append(middlewares, otelMiddleware)
	}
	return NewRiverJobsWithMiddleware(pool, queuePrefix, middlewares...)
}

func NewRiverJobsWithMiddleware(pool *pgxpool.Pool, queuePrefix string, middlewares ...rivertype.Middleware) (*RiverJobs, error) {
	w := &RiverJobs{
		pool:        pool,
		jobMapper:   jobs.NewJobMapper(),
		queuePrefix: queuePrefix,
	}
	return w, w.initClient(middlewares...)
}

func (w *RiverJobs) RiverClient() *river.Client[pgx.Tx] {
	return w.riverClient
}

func (w *RiverJobs) initClient(middlewares ...rivertype.Middleware) error {
	var err error
	defaultQueue := w.queueName("default")
	w.riverWorkers = river.NewWorkers()

	w.riverClient, err = river.NewClient(riverpgxv5.New(w.pool), &river.Config{
		Queues:            map[string]river.QueueConfig{defaultQueue: {MaxWorkers: 4}},
		JobTimeout:        120 * time.Minute,
		Workers:           w.riverWorkers,
		FetchCooldown:     50 * time.Millisecond,
		FetchPollInterval: 100 * time.Millisecond,
		Middleware:        middlewares,
	})
	if err != nil {
		return err
	}
	workFunc := river.WorkFunc(func(ctx context.Context, outerJob *river.Job[riverJobArgs]) error {
		err := w.RunJob(ctx, outerJob.Args.ToJob())
		if err != nil {
			return river.JobCancel(err)
		}
		return err
	})
	err = river.AddWorkerSafely(w.riverWorkers, workFunc)
	if err != nil {
		return err
	}
	return nil

}

func (w *RiverJobs) Use(mwf jobs.JobMiddleware) {
	w.middlewares = append(w.middlewares, mwf)
}

func (w *RiverJobs) AddQueue(queue string, count int) error {
	return w.riverClient.Queues().Add(w.queueName(queue), river.QueueConfig{MaxWorkers: count})
}

func (w *RiverJobs) AddJobType(jobFn jobs.JobFn) error {
	jw := jobFn()
	if jw == nil {
		return errors.New("invalid job function")
	}
	return w.jobMapper.AddJobType(jobFn)
}

func (w *RiverJobs) queueName(queue string) string {
	if queue == "" {
		queue = "default"
	}
	if w.queuePrefix != "" {
		return fmt.Sprintf("%s-%s", w.queuePrefix, queue)
	}
	return queue
}

func (w *RiverJobs) AddJobs(ctx context.Context, jobs []jobs.Job) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var rparams []river.InsertManyParams
	for _, job := range jobs {
		rparams = append(rparams, w.makeRiverJobArgs(job))
	}
	if _, err = w.riverClient.InsertManyTx(ctx, tx, rparams); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (w *RiverJobs) makeRiverJobArgs(job jobs.Job) river.InsertManyParams {
	insertOpts := river.InsertOpts{}
	insertOpts.Queue = w.queueName(job.Queue)
	if job.Unique {
		insertOpts.UniqueOpts = river.UniqueOpts{
			ByArgs:   true,
			ByPeriod: 24 * time.Hour,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRetryable,
				rivertype.JobStateRunning,
				rivertype.JobStateScheduled,
			},
		}
	}
	return river.InsertManyParams{
		Args:       newRiverJobArgsFromJob(job),
		InsertOpts: &insertOpts,
	}
}

func (w *RiverJobs) AddJob(ctx context.Context, job jobs.Job) error {
	return w.AddJobs(ctx, []jobs.Job{job})
}

func (w *RiverJobs) AddPeriodicJob(ctx context.Context, jobFunc func() jobs.Job, period time.Duration, cronTab string) error {
	pj := river.NewPeriodicJob(
		river.PeriodicInterval(period),
		func() (river.JobArgs, *river.InsertOpts) {
			p := w.makeRiverJobArgs(jobFunc())
			return p.Args, p.InsertOpts
		},
		nil,
	)
	w.periodicJobs = append(w.periodicJobs, pj)
	return nil
}

func (w *RiverJobs) RunJob(ctx context.Context, job jobs.Job) error {
	now := time.Now().In(time.UTC).Unix()
	if job.JobDeadline > 0 && now > job.JobDeadline {
		w.log.Trace().Int64("job_deadline", job.JobDeadline).Int64("now", now).Msg("job skipped - deadline in past")
		return nil
	}
	runner, err := w.jobMapper.GetRunner(job.JobType, job.JobArgs)
	if err != nil {
		return errors.New("no job")
	}
	if runner == nil {
		return errors.New("no job")
	}
	for _, mwf := range w.middlewares {
		runner = mwf(runner, job)
		if runner == nil {
			return errors.New("no job after middleware")
		}
	}
	return runner.Run(ctx)
}

func (w *RiverJobs) Run(ctx context.Context) error {
	if err := w.riverClient.Start(ctx); err != nil {
		return err
	}
	for _, pj := range w.periodicJobs {
		w.riverClient.PeriodicJobs().Add(pj)
	}
	<-w.riverClient.Stopped()
	return nil
}

func (w *RiverJobs) Stop(ctx context.Context) error {
	if err := w.riverClient.Stop(ctx); err != nil {
		return err
	}
	<-w.riverClient.Stopped()
	return nil
}
