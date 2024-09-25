package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/interline-io/log"
)

func init() {
	var _ JobQueue = &LocalJobs{}
}

var jobCounter = uint64(0)

type LocalJobs struct {
	jobs           chan Job
	jobfuncs       []func(context.Context, Job) error
	running        bool
	middlewares    []JobMiddleware
	uniqueJobs     map[string]bool
	uniqueJobsLock sync.Mutex
	jobMapper      *jobMapper
	ctx            context.Context
	cancel         context.CancelFunc
}

func NewLocalJobs() *LocalJobs {
	f := &LocalJobs{
		jobs:       make(chan Job, 1000),
		uniqueJobs: map[string]bool{},
		jobMapper:  newJobMapper(),
	}
	return f
}

func (f *LocalJobs) Use(mwf JobMiddleware) {
	f.middlewares = append(f.middlewares, mwf)
}

func (f *LocalJobs) AddQueue(queue string, count int) error {
	for i := 0; i < count; i++ {
		f.jobfuncs = append(f.jobfuncs, f.RunJob)
	}
	return nil
}

func (f *LocalJobs) AddJobType(jobFn JobFn) error {
	return f.jobMapper.AddJobType(jobFn)
}

func (f *LocalJobs) AddJobs(ctx context.Context, jobs []Job) error {
	for _, job := range jobs {
		err := f.AddJob(ctx, job)
		if err != nil {
			return err
		}
	}
	return nil
}

func (f *LocalJobs) AddJob(ctx context.Context, job Job) error {
	if f.jobs == nil {
		return errors.New("closed")
	}
	if job.Unique {
		f.uniqueJobsLock.Lock()
		defer f.uniqueJobsLock.Unlock()
		key, err := job.HexKey()
		if err != nil {
			return err
		}
		if _, ok := f.uniqueJobs[key]; ok {
			log.Trace().Interface("job", job).Msgf("already locked: %s", key)
			return nil
		} else {
			f.uniqueJobs[key] = true
			log.Trace().Interface("job", job).Msgf("locked: %s", key)
		}
	}
	f.jobs <- job
	log.Info().Interface("job", job).Msg("jobs: added job")
	return nil
}

func (f *LocalJobs) RunJob(ctx context.Context, job Job) error {
	job = Job{
		JobType:     job.JobType,
		JobArgs:     job.JobArgs,
		JobDeadline: job.JobDeadline,
		Unique:      job.Unique,
		jobId:       fmt.Sprintf("%d", atomic.AddUint64(&jobCounter, 1)),
	}
	now := time.Now().In(time.UTC).Unix()
	if job.JobDeadline > 0 && job.JobDeadline < now {
		log.Trace().Int64("job_deadline", job.JobDeadline).Int64("now", now).Msg("job skipped - deadline in past")
		return nil
	}
	if job.Unique {
		f.uniqueJobsLock.Lock()
		defer f.uniqueJobsLock.Unlock()
		key, err := job.HexKey()
		if err != nil {
			return err
		}
		delete(f.uniqueJobs, key)
		log.Trace().Interface("job", job).Msgf("unlocked: %s", key)
	}
	w, err := f.jobMapper.GetRunner(job.JobType, job.JobArgs)
	if err != nil {
		return err
	}
	if w == nil {
		return errors.New("no job")
	}
	for _, mwf := range f.middlewares {
		w = mwf(w)
		if w == nil {
			return errors.New("no job")
		}
	}
	return w.Run(ctx, job)
}

func (f *LocalJobs) Run(ctx context.Context) error {
	if f.running {
		return errors.New("already running")
	}
	f.ctx, f.cancel = context.WithCancel(ctx)
	log.Infof("jobs: running")
	f.running = true
	for _, jobfunc := range f.jobfuncs {
		go func(jf func(context.Context, Job) error) {
			for job := range f.jobs {
				if err := jf(ctx, job); err != nil {
					log.Trace().Err(err).Msg("job failed")
				}
			}
		}(jobfunc)
	}
	<-f.ctx.Done()
	return nil
}

func (f *LocalJobs) Stop(ctx context.Context) error {
	if !f.running {
		return errors.New("not running")
	}
	log.Infof("jobs: stopping")
	close(f.jobs)
	f.cancel()
	f.running = false
	f.jobs = nil
	return nil
}
